// Package export answers the access and portability rights (project.md §101;
// docs/compliance/05).
//
// Everything held about one actor, in one documented file, scoped to one project. It is the
// mirror of §61's erasure and shares its resolution — `subject.Resolve` decides who the
// request is about for both, because an export and a deletion that disagreed about that
// would be a pair of answers about two different people.
//
// # Where the events are read from
//
// From ClickHouse, not from the archive, and that is a deliberate reversal of what §10.4's
// package comment anticipated. The archive was expected to serve exports because §33's hot
// window was a fraction of the retention tier — but ADR 0024 moved `events_raw` onto a
// tiered storage policy, so the cold half of the tier is still *in the table*, merely on
// object storage. The two copies now cover the same span, and one of them answers a scoped
// query while the other would have to be downloaded object by object across the whole span.
//
// The residue is worth naming: an event that reached the archive and not ClickHouse — the
// two consume the bus through independent subscriptions (§20) — is not in the bundle. §34's
// cold-query path is where that is closed, and it is not built.
//
// # Why a bundle is bounded and expires
//
// It is written as one object, so it is held in memory while it is assembled, so it is
// bounded by a row count: a person with more history than the bound is refused with a number
// rather than served a truncated file that claims to be complete — §05 is explicit that a
// partial export presented as a complete one is the wrong failure.
//
// And it expires. The bundle is a second copy of everything held about someone, sitting in a
// bucket, so it is the last thing that should linger: the same job that writes it removes it
// when its term is up, and §61's purge removes it sooner if the person asks to be erased.
package export

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/dataexportrequest"
	"playersense.dev/backend/ent/verifiedpurchase"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/subject"
)

// Defaults for the knobs a deployment is unlikely to want to think about.
const (
	// DefaultTTL is how long a completed bundle stays collectable.
	//
	// Long enough for a studio to fetch it inside a working week, short enough that an
	// uncollected copy of a person's history is not a permanent fixture of the bucket.
	DefaultTTL = 7 * 24 * time.Hour

	// DefaultMaxRows is the ceiling on one bundle.
	//
	// A bound on memory expressed in the unit the operator can reason about. At roughly
	// half a kilobyte of JSON an event, a million rows is a few hundred megabytes of
	// buffer and a file a studio can actually open.
	DefaultMaxRows = 1_000_000

	// DefaultMaxRequests bounds one pass.
	DefaultMaxRequests = 10

	// DefaultRetryAfter is how long a failed export rests before the next attempt.
	DefaultRetryAfter = time.Hour
)

// Pool supplies the ClickHouse handle, resolved per run for the reason scheduler.Pool gives.
type Pool interface {
	DB() (*sql.DB, error)
}

// Exporter fills every open export request and expires the bundles that are past their term.
//
// An export is one project's players' data, so the bundle is written to the archive bucket
// and expires there (§03).
type Exporter struct {
	DB    *pgdb.DB
	CH    Pool
	Store blobstore.Store
	Log   *slog.Logger

	// TTL is how long a completed bundle survives. Zero takes DefaultTTL.
	TTL time.Duration
	// RetryAfter is how long a failed request rests. Zero takes DefaultRetryAfter.
	RetryAfter time.Duration
	// MaxRows bounds one bundle. Zero takes DefaultMaxRows.
	MaxRows uint64
	// MaxRequests bounds one pass. Zero takes DefaultMaxRequests.
	MaxRequests int
}

// Result reports what one pass did.
type Result struct {
	Considered int
	Completed  int
	Failed     int
	Expired    int
	Rows       uint64
	Bytes      uint64
}

// Run fills every open request and expires what is past its term.
func (e *Exporter) Run(ctx context.Context, now time.Time) (Result, error) {
	var out Result

	if !e.DB.Ready() {
		return out, pgdb.ErrNotStarted
	}
	log := e.Log
	if log == nil {
		log = slog.Default()
	}
	retry := e.RetryAfter
	if retry <= 0 {
		retry = DefaultRetryAfter
	}
	limit := e.MaxRequests
	if limit <= 0 {
		limit = DefaultMaxRequests
	}

	// Expiry first, and cheaply. A pass that fails to build a bundle must not also fail to
	// remove yesterday's: the two have nothing to do with each other, and the removal is
	// the one with a deadline attached to it.
	expired, err := e.expire(ctx, now, log)
	out.Expired = expired
	if err != nil {
		return out, err
	}

	open, err := e.DB.Client.DataExportRequest.Query().
		Where(
			dataexportrequest.StatusIn(
				dataexportrequest.StatusReceived,
				dataexportrequest.StatusRunning,
				dataexportrequest.StatusFailed,
			),
			dataexportrequest.Or(
				dataexportrequest.StatusNEQ(dataexportrequest.StatusFailed),
				dataexportrequest.UpdatedAtLTE(now.Add(-retry)),
			),
		).
		Order(ent.Asc(dataexportrequest.FieldCreatedAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return out, fmt.Errorf("find open export requests: %w", err)
	}
	out.Considered = len(open)
	if out.Considered == 0 {
		return out, nil
	}

	db, err := e.CH.DB()
	if err != nil {
		return out, fmt.Errorf("clickhouse: %w", err)
	}

	for _, req := range open {
		rows, bytes, err := e.fill(ctx, db, req, now, log)
		if err != nil {
			out.Failed++
			log.Error("an export failed",
				"request", req.UID, "project_uid", req.ProjectUID, "error", err)
			if markErr := e.DB.Client.DataExportRequest.UpdateOne(req).
				SetStatus(dataexportrequest.StatusFailed).
				SetFailureReason(pgdb.Reason(err.Error())).
				Exec(ctx); markErr != nil {
				return out, fmt.Errorf("record the failure of %s: %w", req.UID, markErr)
			}
			continue
		}
		out.Completed++
		out.Rows += rows
		out.Bytes += bytes
	}
	return out, nil
}

// fill assembles one bundle and records where it landed.
func (e *Exporter) fill(
	ctx context.Context,
	db *sql.DB,
	req *ent.DataExportRequest,
	now time.Time,
	log *slog.Logger,
) (uint64, uint64, error) {
	if req.Status != dataexportrequest.StatusRunning {
		saved, err := e.DB.Client.DataExportRequest.UpdateOne(req).
			SetStatus(dataexportrequest.StatusRunning).
			ClearFailureReason().
			Save(ctx)
		if err != nil {
			return 0, 0, fmt.Errorf("record the start: %w", err)
		}
		req = saved
	}

	// The narrow reading of the identifier. Widening it to the install would return every
	// event produced by a second person who signed in on that device, which is a disclosure
	// rather than a more complete answer (subject.Match).
	who, err := subject.Resolve(ctx, db, req.ProjectSeq, req.ActorKey, subject.Actor)
	if err != nil {
		return 0, 0, err
	}

	b := newBundle()
	if err := b.write(line{Section: "meta", Meta: &Meta{
		BundleVersion: BundleVersion,
		Request:       req.UID.String(),
		ProjectID:     req.ProjectUID.String(),
		ActorKey:      req.ActorKey,
		GeneratedAt:   now.UTC(),
		Limits:        limits,
	}}); err != nil {
		return 0, 0, err
	}

	if err := e.writeEvents(ctx, db, req, b); err != nil {
		return 0, 0, err
	}
	if err := e.writeDevices(ctx, db, req, who, b); err != nil {
		return 0, 0, err
	}
	if err := e.writeDays(ctx, db, req, who, b); err != nil {
		return 0, 0, err
	}
	if err := e.writePurchases(ctx, req, b); err != nil {
		return 0, 0, err
	}

	// The summary closes the file, so a bundle without one is visibly incomplete. Its own
	// line is counted: `rows` is what the reader should find, not what was found before it
	// was written.
	b.counts.Rows = b.rows + 1
	if err := b.write(line{Section: "summary", Summary: &b.counts}); err != nil {
		return 0, 0, err
	}

	path := bundlePath(req)
	object, err := e.Store.Put(ctx, path, b.buf.Bytes())
	if err != nil {
		return 0, 0, fmt.Errorf("write the export bundle: %w", err)
	}

	expires := now.Add(e.ttl())
	if err := e.DB.Client.DataExportRequest.UpdateOne(req).
		SetStatus(dataexportrequest.StatusCompleted).
		SetBucket(e.Store.Bucket()).
		SetObjectPath(object.Path).
		SetObjectSizeBytes(object.Size).
		SetRowCount(b.counts.Events).
		SetExpiresAt(expires).
		SetCompletedAt(now).
		ClearFailureReason().
		Exec(ctx); err != nil {
		return 0, 0, fmt.Errorf("record the bundle: %w", err)
	}

	log.Info("an export completed",
		"request", req.UID,
		"project_uid", req.ProjectUID,
		"events", b.counts.Events,
		"devices", b.counts.Devices,
		"bytes", object.Size,
		"expires_at", expires.Format(time.RFC3339))
	return b.rows, object.Size, nil
}

// expire removes the bundles whose term is up.
//
// The object goes and the row stays. That an export was produced, for whom and when, is a
// fact about processing and belongs in the same audit record as the deletion requests (§06)
// — but the copy of the person's history does not need to outlive its own deadline to prove
// it existed.
func (e *Exporter) expire(ctx context.Context, now time.Time, log *slog.Logger) (int, error) {
	due, err := e.DB.Client.DataExportRequest.Query().
		Where(
			dataexportrequest.StatusEQ(dataexportrequest.StatusCompleted),
			dataexportrequest.ExpiresAtLTE(now),
		).
		Limit(100).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("find expired export bundles: %w", err)
	}

	removed := 0
	for _, req := range due {
		if req.Bucket != e.Store.Bucket() {
			continue
		}
		if err := e.Store.Delete(ctx, req.ObjectPath); err != nil {
			return removed, fmt.Errorf("delete expired bundle %s: %w", req.ObjectPath, err)
		}
		if err := e.DB.Client.DataExportRequest.UpdateOne(req).
			SetStatus(dataexportrequest.StatusExpired).
			Exec(ctx); err != nil {
			return removed, fmt.Errorf("record the expiry of %s: %w", req.UID, err)
		}
		removed++
		log.Info("expired an export bundle", "request", req.UID, "object_path", req.ObjectPath)
	}
	return removed, nil
}

func (e *Exporter) ttl() time.Duration {
	if e.TTL > 0 {
		return e.TTL
	}
	return DefaultTTL
}

func (e *Exporter) maxRows() uint64 {
	if e.MaxRows > 0 {
		return e.MaxRows
	}
	return DefaultMaxRows
}

// bundlePath names the object.
//
// Its own top-level prefix, deliberately outside the `org=…/project=…` tree §14.1
// defines for events. Three things follow from that and all three are wanted: §34's cold
// queries prune that tree and must never find a bundle in it, §60's expiry walks manifests
// and a bundle has none, and a bucket lifecycle rule can be pointed at exports alone as a
// backstop for the term this job enforces.
func bundlePath(req *ent.DataExportRequest) string {
	return fmt.Sprintf("export/project=%s/%s.ndjson", req.ProjectUID, req.UID)
}

// limits are the caveats that travel inside every bundle.
//
// Written out rather than linked, because a file handed to a person is read where the
// documentation is not.
var limits = []string{
	"Scoped to one project. A studio can ask about its own players and no one else's.",
	"Covers the identifier named in the request. A player who reinstalled has more than " +
		"one install_id and PlayerSense cannot link them; ask again with each identifier.",
	"Events reach back as far as this project's retention tier and no further.",
	"Device-level records describe the installation. Where a second account signed in on " +
		"the same device, its activity contributed to those totals.",
	"Aggregates that identify nobody — a project's daily counts — are not part of this " +
		"answer, because they are not about this person.",
}

// writeEvents streams the raw events into the bundle (§23).
//
// Matched on the actor alone. The maps are rendered to JSON by ClickHouse rather than
// scanned as maps: `toJSONString` produces exactly the shape the bundle wants, and it keeps
// one nested structure out of the driver's type mapping on a path where a surprise would be
// a corrupted answer to a legal request.
func (e *Exporter) writeEvents(ctx context.Context, db *sql.DB, req *ent.DataExportRequest, b *bundle) error {
	where, args := subject.Actor.Predicate(req.ProjectSeq, req.ActorKey)
	args = append(args, e.maxRows()+1)

	// ifNull at the edge rather than *string in the bundle: NULL is how absence is stored,
	// and "" plus `omitempty` is how absence is rendered in JSON. The bundle is a document a
	// person reads, and an omitted field says "we hold nothing here" better than `null` does.
	rows, err := db.QueryContext(ctx, `
		SELECT
			event_time, ingest_time, received_at,
			toString(event_id), toString(batch_id), session_id, ifNull(player_id, ''), toString(install_id),
			event_name, event_type,
			platform, country_code, app_version, sdk_name, sdk_version,
			consent_analytics, ifNull(consent_policy_version, ''), ifNull(att_status, ''),
			toJSONString(props_string), toJSONString(props_int),
			toJSONString(props_float), toJSONString(props_bool),
			revenue_micros, ifNull(currency, ''), playtime_ms
		FROM events_raw
		WHERE `+where+`
		ORDER BY event_time
		LIMIT ?`, args...)
	if err != nil {
		return fmt.Errorf("read the subject's events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ev EventRow
		var propsString, propsInt, propsFloat, propsBool string
		if err := rows.Scan(
			&ev.EventTime, &ev.IngestTime, &ev.ReceivedAt,
			&ev.EventID, &ev.BatchID, &ev.SessionID, &ev.PlayerID, &ev.InstallID,
			&ev.EventName, &ev.EventType,
			&ev.Platform, &ev.CountryCode, &ev.AppVersion, &ev.SDKName, &ev.SDKVersion,
			&ev.ConsentAnalytics, &ev.ConsentPolicyVersion, &ev.ATTStatus,
			&propsString, &propsInt, &propsFloat, &propsBool,
			&ev.RevenueMicros, &ev.Currency, &ev.PlaytimeMS,
		); err != nil {
			return fmt.Errorf("read the subject's events: %w", err)
		}
		ev.PropsString = rawOrNil(propsString)
		ev.PropsInt = rawOrNil(propsInt)
		ev.PropsFloat = rawOrNil(propsFloat)
		ev.PropsBool = rawOrNil(propsBool)

		b.counts.Events++
		// One past the bound, which is how the bound is detected at all: asking for one
		// more row than may be returned turns "possibly truncated" into "definitely too
		// large", and §05 would rather refuse than hand over a partial answer.
		if b.counts.Events > e.maxRows() {
			return fmt.Errorf(
				"this subject has more than %d events, which is more than one bundle holds; "+
					"raise EXPORT_MAX_ROWS or narrow the request", e.maxRows())
		}
		if err := b.write(line{Section: "event", Event: &ev}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// writeDevices adds the lifetime record of each device (§29.6).
func (e *Exporter) writeDevices(ctx context.Context, db *sql.DB, req *ent.DataExportRequest, who subject.Subject, b *bundle) error {
	if len(who.Devices) == 0 {
		return nil
	}
	// Through the view, the only reader that merges an AggregatingMergeTree's partial rows
	// into the device's actual totals (§29.6).
	rows, err := db.QueryContext(ctx, `
		SELECT
			device_id, device_key, first_seen, last_seen, cohort_date,
			tupleElement(first_attrs, 1), tupleElement(first_attrs, 2),
			tupleElement(first_attrs, 3),
			playtime_ms, revenue_micros, purchases, active_days
		FROM player_lifetime_v
		WHERE project_seq = ? AND device_id IN (`+subject.Placeholders(len(who.Devices))+`)`,
		subject.DeviceArgs(req.ProjectSeq, who.Devices)...)
	if err != nil {
		return fmt.Errorf("read the subject's devices: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d DeviceRow
		var cohortDate time.Time
		if err := rows.Scan(
			&d.DeviceID, &d.InstallID, &d.FirstSeen, &d.LastSeen, &cohortDate,
			&d.InstallPlatform, &d.InstallCountry, &d.FirstAppVersion,
			&d.PlaytimeMS, &d.RevenueMicros, &d.Purchases, &d.ActiveDays,
		); err != nil {
			return fmt.Errorf("read the subject's devices: %w", err)
		}
		d.CohortDate = cohortDate.Format(time.DateOnly)

		b.counts.Devices++
		if err := b.write(line{Section: "device", Device: &d}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// writeDays adds the per-device daily record (§29.7).
func (e *Exporter) writeDays(ctx context.Context, db *sql.DB, req *ent.DataExportRequest, who subject.Subject, b *bundle) error {
	if len(who.Devices) == 0 {
		return nil
	}
	// The GROUP BY is not cosmetic: this is a SummingMergeTree, so a device-day exists as
	// several partial rows until its parts merge, and reading it without summing would
	// export a day's playtime as two smaller numbers (§29.7).
	rows, err := db.QueryContext(ctx, `
		SELECT
			device_id, activity_date,
			sum(sessions), sum(playtime_ms), sum(events), sum(revenue_micros), sum(purchases)
		FROM player_activity_daily
		WHERE project_seq = ? AND device_id IN (`+subject.Placeholders(len(who.Devices))+`)
		GROUP BY device_id, activity_date
		ORDER BY activity_date`,
		subject.DeviceArgs(req.ProjectSeq, who.Devices)...)
	if err != nil {
		return fmt.Errorf("read the subject's activity: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d DayRow
		var date time.Time
		if err := rows.Scan(
			&d.DeviceID, &date,
			&d.Sessions, &d.PlaytimeMS, &d.Events, &d.RevenueMicros, &d.Purchases,
		); err != nil {
			return fmt.Errorf("read the subject's activity: %w", err)
		}
		d.ActivityDate = date.Format(time.DateOnly)

		b.counts.ActivityDays++
		if err := b.write(line{Section: "activity_day", Day: &d}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// writePurchases adds the verified transactions (§47).
func (e *Exporter) writePurchases(ctx context.Context, req *ent.DataExportRequest, b *bundle) error {
	purchases, err := e.DB.Client.VerifiedPurchase.Query().
		Where(
			verifiedpurchase.ProjectUID(req.ProjectUID),
			verifiedpurchase.PlayerID(req.ActorKey),
		).
		Order(ent.Asc(verifiedpurchase.FieldPurchasedAt)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("read the subject's purchases: %w", err)
	}
	for _, p := range purchases {
		b.counts.Purchases++
		if err := b.write(line{Section: "purchase", Purchase: &PurchaseRow{
			Platform:           string(p.Platform),
			StoreTransactionID: p.StoreTransactionID,
			ProductID:          p.ProductID,
			AmountMicros:       p.AmountMicros,
			Currency:           p.Currency,
			PurchasedAt:        p.PurchasedAt.UTC(),
		}}); err != nil {
			return err
		}
	}
	return nil
}
