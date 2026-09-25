package purchases

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/purchaseattempt"
	"playersense.dev/backend/ent/purchasechange"
	"playersense.dev/backend/ent/verifiedpurchase"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/stores"
)

// Shipper moves purchase_changes into ClickHouse's purchases table, in the worker.
//
// A pass first re-sends any batch claimed and not marked shipped (its process died between the
// insert and the mark), then claims the next: the oldest unshipped changes, up to BatchSize, with
// FOR UPDATE SKIP LOCKED so two workers take different rows. The batch's id is the insert's
// deduplication token, and the rows are read back in id order, so a batch sent twice is the same
// block twice and ClickHouse stores it once, in the ledger and in every rollup chained from it.
type Shipper struct {
	DB         *pgdb.DB
	ClickHouse interface{ DB() (driver.Conn, error) }
	BatchSize  int
	Every      time.Duration
	Log        *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

// Housekeeping terms.
const (
	// ShippedDays is how long a shipped change is kept, for a look back at what was sent.
	ShippedDays = 7
	// AttemptDays is how long the verification log is kept.
	AttemptDays = 30
	// reclaimAfter is how long a claimed batch may sit unshipped before another pass sends it.
	reclaimAfter = time.Minute
)

// eventNamespace makes a verified purchase's ClickHouse event id from its uid: stable, so every
// change to one purchase replaces the same ledger row.
var eventNamespace = uuid.MustParse("5f1d4c1e-3b9a-4c63-9f63-5e0f8a1d2c77")

func (s *Shipper) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Name identifies the component in start-up and shutdown logs.
func (s *Shipper) Name() string { return "purchase-shipper" }

// Start begins shipping in the background.
func (s *Shipper) Start(context.Context) error {
	if s.Every <= 0 {
		s.Every = 2 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(s.Every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for {
					n, err := s.Ship(ctx)
					if err != nil {
						s.log().Error("shipping purchases to ClickHouse failed; retried next tick", "error", err)
						break
					}
					if n < s.size() {
						break // caught up
					}
				}
			}
		}
	}()
	return nil
}

// Stop ends shipping, waiting for a batch in flight.
func (s *Shipper) Stop(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
	case <-ctx.Done():
	}
	return nil
}

func (s *Shipper) size() int {
	if s.BatchSize <= 0 {
		return 1000
	}
	return s.BatchSize
}

// Ship sends one batch: a stale one if there is any, else the next. It answers how many changes
// the batch held.
func (s *Shipper) Ship(ctx context.Context) (int, error) {
	db := s.DB.Client
	var batch uuid.UUID
	stale, err := db.PurchaseChange.Query().Where(purchasechange.ShippedAtIsNil(), purchasechange.BatchNotNil(),
		purchasechange.UpdatedAtLT(time.Now().Add(-reclaimAfter))).First(ctx)
	switch {
	case err == nil:
		batch = *stale.Batch
	case ent.IsNotFound(err):
		batch = uuid.New()
		res, err := s.DB.DB().ExecContext(ctx, `
			UPDATE purchase_changes SET batch = $1, updated_at = now()
			WHERE id IN (
				SELECT id FROM purchase_changes
				WHERE shipped_at IS NULL AND batch IS NULL
				ORDER BY id LIMIT $2
				FOR UPDATE SKIP LOCKED)`, batch, s.size())
		if err != nil {
			return 0, fmt.Errorf("claim a batch: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, nil
		}
	default:
		return 0, err
	}

	changes, err := db.PurchaseChange.Query().Where(purchasechange.Batch(batch)).
		Order(ent.Asc(purchasechange.FieldID)).All(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := s.rows(ctx, changes)
	if err != nil {
		return 0, err
	}
	if len(rows) > 0 {
		if err := s.insert(ctx, batch, rows); err != nil {
			return 0, err
		}
	}
	if _, err := db.PurchaseChange.Update().Where(purchasechange.Batch(batch)).SetShippedAt(time.Now()).Save(ctx); err != nil {
		return 0, fmt.Errorf("mark a shipped batch: %w", err)
	}
	return len(changes), nil
}

// chRow is one row of ClickHouse's purchases table, as a verified change writes it.
type chRow struct {
	projectSeq                            uint32
	environmentSeq, retentionTier         uint16
	purchaseDate, eventTime               time.Time
	eventID, installID                    uuid.UUID
	playerID                              *string
	productID, store, currency            string
	revenue                               int64
	platform, country                     string
	status, change                        string
	version                               uint64
	transactionID, refundReason, rejected string
	refundedAt                            *time.Time
	verifyMs                              uint32
}

// rows reads each change's purchase or attempt as the row it writes.
func (s *Shipper) rows(ctx context.Context, changes []*ent.PurchaseChange) ([]chRow, error) {
	db := s.DB.Client
	var purchaseIDs, attemptIDs []int
	for _, c := range changes {
		if c.PurchaseID != nil {
			purchaseIDs = append(purchaseIDs, *c.PurchaseID)
		}
		if c.AttemptID != nil {
			attemptIDs = append(attemptIDs, *c.AttemptID)
		}
	}
	purchases := map[int]*ent.VerifiedPurchase{}
	if len(purchaseIDs) > 0 {
		found, err := db.VerifiedPurchase.Query().Where(verifiedpurchase.IDIn(purchaseIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range found {
			purchases[p.ID] = p
		}
	}
	attempts := map[int]*ent.PurchaseAttempt{}
	if len(attemptIDs) > 0 {
		found, err := db.PurchaseAttempt.Query().Where(purchaseattempt.IDIn(attemptIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range found {
			attempts[a.ID] = a
		}
	}

	// The projects, by uid for purchases and by id for attempts: a retention tier each.
	projects := map[uuid.UUID]*ent.Project{}
	byID := map[int]*ent.Project{}
	var uids []uuid.UUID
	var ids []int
	for _, p := range purchases {
		uids = append(uids, p.ProjectUID)
	}
	for _, a := range attempts {
		ids = append(ids, a.ProjectID)
	}
	if len(uids)+len(ids) > 0 {
		found, err := db.Project.Query().Where(project.Or(project.UIDIn(uids...), project.IDIn(ids...))).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range found {
			projects[p.UID], byID[p.ID] = p, p
		}
	}

	var out []chRow
	for _, c := range changes {
		switch {
		case c.PurchaseID != nil:
			p, proj := purchases[*c.PurchaseID], (*ent.Project)(nil)
			if p != nil {
				proj = projects[p.ProjectUID]
			}
			if p == nil || proj == nil {
				continue // erased, or its project purged: nothing to count
			}
			out = append(out, purchaseRow(c, p, proj))
		case c.AttemptID != nil:
			a := attempts[*c.AttemptID]
			if a == nil || byID[a.ProjectID] == nil {
				continue
			}
			out = append(out, rejectionRow(c, a, byID[a.ProjectID]))
		}
	}
	return out, nil
}

// installOf is the install a purchase is counted against: the one the game named, or a stand-in
// derived from the player, so one player's purchases land on one device.
func installOf(projectUID uuid.UUID, install, player string) uuid.UUID {
	if id, err := uuid.Parse(install); err == nil {
		return id
	}
	return uuid.NewSHA1(eventNamespace, []byte(projectUID.String()+"/player/"+player+"/"+install))
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func purchaseRow(c *ent.PurchaseChange, p *ent.VerifiedPurchase, proj *ent.Project) chRow {
	platform := stores.Platform(p.Platform)
	status := "verified"
	switch {
	case c.Kind == purchasechange.KindRefund:
		status = "refunded"
	case p.Sandbox:
		status = "sandbox"
	}
	row := chRow{
		projectSeq: uint32(proj.ID), environmentSeq: uint16(p.EnvironmentID), retentionTier: uint16(proj.RetentionDays),
		purchaseDate: p.PurchasedAt.UTC(), eventTime: p.PurchasedAt.UTC(),
		eventID:   uuid.NewSHA1(eventNamespace, []byte("purchase/"+p.UID.String())),
		installID: installOf(p.ProjectUID, p.InstallID, p.PlayerID), playerID: nullable(p.PlayerID),
		productID: p.ProductID, store: platform.StoreName(), currency: p.Currency, revenue: p.AmountMicros,
		platform: platform.DevicePlatform(), country: p.Country,
		status: status, change: string(c.Kind), version: uint64(c.ID),
		transactionID: p.StoreTransactionID, verifyMs: uint32(max(p.VerifyMs, 0)),
	}
	if c.Kind == purchasechange.KindRefund || c.Kind == purchasechange.KindReinstate {
		at := c.At.UTC()
		row.refundedAt = &at
	}
	if c.Kind == purchasechange.KindRefund {
		row.refundReason = p.RefundReason
	}
	return row
}

func rejectionRow(c *ent.PurchaseChange, a *ent.PurchaseAttempt, proj *ent.Project) chRow {
	platform := stores.Platform(a.Platform)
	return chRow{
		projectSeq: uint32(proj.ID), environmentSeq: uint16(a.EnvironmentID), retentionTier: uint16(proj.RetentionDays),
		purchaseDate: a.CreatedAt.UTC(), eventTime: a.CreatedAt.UTC(),
		eventID:   uuid.NewSHA1(eventNamespace, []byte("attempt/"+a.UID.String())),
		installID: installOf(proj.UID, a.InstallID, a.PlayerID), playerID: nullable(a.PlayerID),
		productID: a.ProductID, store: platform.StoreName(),
		platform: platform.DevicePlatform(),
		status:   "rejected", change: string(c.Kind), version: uint64(c.ID),
		transactionID: a.StoreTransactionID, rejected: a.Code, verifyMs: uint32(max(a.DurationMs, 0)),
	}
}

const insertStatement = `INSERT INTO purchases (
	project_seq, environment_seq, retention_tier, purchase_date, event_time, event_id,
	install_id, player_id, session_id, event_name, product_id, store, revenue_micros, currency,
	platform, country_code, app_version, cohort_date,
	source, status, change, version, store_transaction_id, refunded_at, refund_reason, rejection, verify_ms)`

func (s *Shipper) insert(ctx context.Context, batch uuid.UUID, rows []chRow) error {
	conn, err := s.ClickHouse.DB()
	if err != nil {
		return err
	}
	// The batch is the token: a batch sent again is the same block, stored once, and the views
	// chained from purchases are deduplicated with it, so the rollups count it once too.
	ctx = clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"insert_deduplication_token":                         batch.String(),
		"deduplicate_blocks_in_dependent_materialized_views": 1,
	}))
	block, err := conn.PrepareBatch(ctx, insertStatement)
	if err != nil {
		return fmt.Errorf("prepare the purchases insert: %w", err)
	}
	for _, r := range rows {
		if err := block.Append(
			r.projectSeq, r.environmentSeq, r.retentionTier, r.purchaseDate, r.eventTime, r.eventID,
			r.installID, r.playerID, "", "purchase", r.productID, r.store, r.revenue, r.currency,
			r.platform, r.country, "", (*time.Time)(nil),
			"verified", r.status, r.change, r.version, r.transactionID, r.refundedAt, r.refundReason, r.rejected, r.verifyMs,
		); err != nil {
			return fmt.Errorf("append a purchase: %w", err)
		}
	}
	if err := block.Send(); err != nil {
		return fmt.Errorf("send the purchases: %w", err)
	}
	return nil
}

// Prune drops shipped changes past their week and attempts past their month.
func (s *Shipper) Prune(ctx context.Context, now time.Time) (int, error) {
	db := s.DB.Client
	n, err := db.PurchaseChange.Delete().Where(purchasechange.ShippedAtLT(now.AddDate(0, 0, -ShippedDays))).Exec(ctx)
	if err != nil {
		return n, err
	}
	m, err := db.PurchaseAttempt.Delete().Where(purchaseattempt.CreatedAtLT(now.AddDate(0, 0, -AttemptDays))).Exec(ctx)
	return n + m, err
}
