package reports

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/notificationchannel"
	"playersense.dev/backend/ent/reportrun"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/pgdb"
)

// Runner runs reports: the same code for a test run asked for on the dashboard and a scheduled
// run in the worker. Every step is written as an event on the run (report_run_events), numbered,
// so the dashboard can follow a run from any process and pick a dropped stream up where it stopped.
type Runner struct {
	// DB is the pool, read per run: the application opens it after the runner is built.
	DB     *pgdb.DB
	Notify *notify.Sender
	// Store holds Export files for seven days; nil makes an Export fail with the reason.
	Store     blobstore.Store
	Dashboard string // where a notification links to
	Log       *slog.Logger
}

// ExportTTL is how long an Export's file can be downloaded.
const ExportTTL = 7 * 24 * time.Hour

// Target is one run to carry out.
type Target struct {
	Report  *ent.Report
	Run     *ent.ReportRun
	Graph   Graph
	Sources SourceReader
	Project *ent.Project
}

// Execute carries a run out to the end, or until it is stopped. It writes the run's row when it
// ends, whatever happened, so a run is never left running.
func (r *Runner) Execute(ctx context.Context, t Target) {
	x := &execution{Runner: r, t: t, now: time.Now().UTC()}
	status, err := x.run(ctx)
	errText := ""
	if err != nil {
		errText = err.Error()
		if len(errText) > 500 {
			errText = errText[:500]
		}
		status = reportrun.StatusFailed
	}
	finished := time.Now().UTC()
	deliveries := make([]map[string]any, 0, len(x.deliveries))
	for _, d := range x.deliveries {
		deliveries = append(deliveries, d.asMap())
	}
	// Written with a context of its own: a run stopped by shutdown still says it stopped.
	write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	x.emit(write, "run_finished", map[string]any{"at": finished.UnixMilli(), "status": string(status)})
	if err := r.DB.Client.ReportRun.UpdateOneID(t.Run.ID).SetStatus(status).SetFinishedAt(finished).
		SetDeliveries(deliveries).SetError(errText).Exec(write); err != nil {
		r.log().Error("could not record the end of a report run", "run", t.Run.UID, "error", err)
	}
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Delivery is one thing a run sent, or chose not to.
type Delivery struct {
	Node    string
	Title   string
	Kind    string // notify or export
	Channel string // a channel's id
	OK      bool
	Quiet   bool
	Detail  string
}

func (d Delivery) asMap() map[string]any {
	m := map[string]any{"node": d.Node, "title": d.Title, "kind": d.Kind, "ok": d.OK, "detail": d.Detail}
	if d.Channel != "" {
		m["channel"] = d.Channel
	}
	if d.Quiet {
		m["quiet"] = true
	}
	return m
}

type execution struct {
	*Runner
	t          Target
	now        time.Time
	mu         sync.Mutex
	seq        int
	deliveries []Delivery
}

func (x *execution) emit(ctx context.Context, kind string, payload map[string]any) {
	x.mu.Lock()
	x.seq++
	seq := x.seq
	x.mu.Unlock()
	payload["run"] = x.t.Run.UID.String()
	payload["type"] = kind
	if err := x.DB.Client.ReportRunEvent.Create().SetRunID(x.t.Run.ID).SetSeq(seq).SetType(kind).
		SetPayload(payload).Exec(ctx); err != nil {
		x.log().Warn("could not record a report run event", "run", x.t.Run.UID, "type", kind, "error", err)
	}
}

func (x *execution) stopped(ctx context.Context) bool {
	run, err := x.DB.Client.ReportRun.Get(ctx, x.t.Run.ID)
	return err == nil && run.CancelRequested
}

func (x *execution) run(ctx context.Context) (reportrun.Status, error) {
	g := x.t.Graph
	plan := g.Plan(x.t.Run.Focus)
	x.emit(ctx, "run_started", map[string]any{"at": x.now.UnixMilli(), "plan": plan})

	channels := map[string]*ent.NotificationChannel{}
	rows, err := x.DB.Client.NotificationChannel.Query().
		Where(notificationchannel.ProjectID(x.t.Report.ProjectID)).All(ctx)
	if err != nil {
		return "", fmt.Errorf("read the project's channels: %w", err)
	}
	for _, c := range rows {
		channels[c.UID.String()] = c
	}

	ev := newEvaluator(g, x.t.Sources, x.now)
	state := map[string]string{}
	failed := false
	for _, id := range plan {
		if ctx.Err() != nil || x.stopped(ctx) {
			for _, rest := range plan {
				if _, done := state[rest]; !done {
					x.emit(ctx, "node_skipped", map[string]any{"node": rest, "reason": "The run was stopped."})
				}
			}
			return reportrun.StatusStopped, nil
		}
		n, _ := g.Node(id)
		blocked := ""
		for _, e := range inputsOf(id, ev.edges) {
			if s := state[e.Source]; s == "failed" || s == "skipped" {
				blocked = "An input did not finish."
			}
		}
		if blocked != "" {
			state[id] = "skipped"
			x.emit(ctx, "node_skipped", map[string]any{"node": id, "reason": blocked})
			continue
		}
		x.emit(ctx, "node_started", map[string]any{"node": id, "at": time.Now().UnixMilli()})
		began := time.Now()
		result := ev.step(ctx, n)
		ev.results[id] = result
		if result.Err != "" {
			state[id] = "failed"
			failed = true
			x.emit(ctx, "node_failed", map[string]any{"node": id, "error": result.Err})
			continue
		}
		switch n.Data.Kind {
		case KindNotify:
			x.notify(ctx, n, result, channels)
		case KindExport:
			x.export(ctx, n, result, began)
		}
		state[id] = "done"
		x.emit(ctx, "node_finished", map[string]any{"node": id, "rows": len(result.Rows),
			"ms": time.Since(began).Milliseconds()})
	}

	tried, bad := 0, 0
	for _, d := range x.deliveries {
		if !d.Quiet {
			tried++
			if !d.OK {
				bad++
			}
		}
	}
	switch {
	case tried == 0 && failed:
		return reportrun.StatusFailed, nil
	case tried == 0:
		return reportrun.StatusQuiet, nil
	case bad == 0:
		return reportrun.StatusSucceeded, nil
	case bad == tried:
		return reportrun.StatusFailed, nil
	}
	return reportrun.StatusPartial, nil
}

func (x *execution) deliver(ctx context.Context, d Delivery, ms int64) {
	x.mu.Lock()
	x.deliveries = append(x.deliveries, d)
	x.mu.Unlock()
	x.emit(ctx, "delivery", map[string]any{"delivery": d.asMap(), "ms": ms})
}

func (x *execution) notify(ctx context.Context, n Node, result *Result, channels map[string]*ent.NotificationChannel) {
	outcome := Decide(n.Data.Config, x.t.Report.Name, result, x.now)
	link := strings.TrimRight(x.Dashboard, "/") + "/reports?report=" + x.t.Report.UID.String()
	source := "Report: " + x.t.Report.Name
	if x.t.Run.Trigger == reportrun.TriggerTest {
		source += " (test run)"
	}
	for _, id := range n.Data.Config.Channels {
		d := Delivery{Node: n.ID, Title: n.Data.Title, Kind: "notify", Channel: id}
		ch, found := channels[id]
		switch {
		case !found:
			d.Detail = "The channel no longer exists."
		case !outcome.Send:
			d.OK, d.Quiet, d.Detail = true, true, outcome.Reason
		case x.t.Run.Dry:
			d.OK, d.Detail = true, "Would send: "+outcome.Title
		case !ch.Enabled:
			d.Detail = "The channel is off."
		default:
			began := time.Now()
			r := x.Notify.Deliver(ctx, x.DB.Client, ch, notify.Message{
				Title: outcome.Title, Summary: outcome.Body, Lines: outcome.Lines, Severity: outcome.Severity,
				Source: source, Link: link, Test: x.t.Run.Trigger == reportrun.TriggerTest,
				Payload: map[string]any{"report": x.t.Report.UID.String(), "run": x.t.Run.UID.String(), "rows": len(result.Rows)},
			}, source)
			d.OK, d.Detail = r.OK, r.Detail
			x.deliver(ctx, d, time.Since(began).Milliseconds())
			continue
		}
		x.deliver(ctx, d, 0)
	}
}

var slug = regexp.MustCompile(`[^a-z0-9]+`)

func fileSlug(name string) string {
	s := strings.Trim(slug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		return "report"
	}
	return s
}

func (x *execution) export(ctx context.Context, n Node, result *Result, began time.Time) {
	c := n.Data.Config
	d := Delivery{Node: n.ID, Title: n.Data.Title, Kind: "export"}
	file := c.File
	if file == "" {
		file = "json"
	}
	name := fileSlug(x.t.Report.Name) + "." + file
	switch {
	case c.Destination != "" && c.Destination != "download":
		d.Detail = "Only a download is written for now; " + c.Destination + " is not."
	case file != "json" && file != "csv":
		d.Detail = "Only JSON and CSV files are written for now."
	case x.Store == nil:
		d.Detail = "This deployment has nowhere to keep files."
	case x.t.Run.Dry:
		d.OK, d.Detail = true, fmt.Sprintf("Would write %s, %d rows, to a download here", name, len(result.Rows))
	default:
		body, kind, err := encode(file, result)
		if err == nil {
			path := fmt.Sprintf("reports/%s/%s/%s", x.t.Project.UID, x.t.Run.UID, fileSlug(n.ID)+"."+file)
			if _, err = x.Store.Put(ctx, path, body); err == nil {
				err = x.DB.Client.ReportExport.Create().SetRunID(x.t.Run.ID).SetNode(n.ID).SetFileName(name).
					SetObjectPath(path).SetContentType(kind).SetSizeBytes(int64(len(body))).SetRows(len(result.Rows)).
					SetExpiresAt(x.now.Add(ExportTTL)).Exec(ctx)
			}
		}
		if err != nil {
			d.Detail = "The file could not be written: " + err.Error()
		} else {
			d.OK, d.Detail = true, fmt.Sprintf("Wrote %s, %d rows, to a download here", name, len(result.Rows))
		}
	}
	x.deliver(ctx, d, time.Since(began).Milliseconds())
}

func encode(file string, result *Result) ([]byte, string, error) {
	if file == "csv" {
		var b bytes.Buffer
		w := csv.NewWriter(&b)
		header := make([]string, len(result.Columns))
		for i, c := range result.Columns {
			header[i] = c.Label
		}
		if err := w.Write(header); err != nil {
			return nil, "", err
		}
		for _, row := range result.Rows {
			line := make([]string, len(result.Columns))
			for i, c := range result.Columns {
				if c.Type == TypeTime {
					if ms, ok := number(row[c.Key]); ok {
						line[i] = time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339)
						continue
					}
				}
				line[i] = csvCell(row[c.Key])
			}
			if err := w.Write(line); err != nil {
				return nil, "", err
			}
		}
		w.Flush()
		return b.Bytes(), "text/csv", w.Error()
	}
	out := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		one := make(map[string]any, len(result.Columns))
		for _, c := range result.Columns {
			one[c.Key] = row[c.Key]
		}
		out = append(out, one)
	}
	body, err := json.Marshal(map[string]any{"columns": result.Columns, "rows": out})
	return body, "application/json", err
}

// csvCell writes a value, and defuses one a spreadsheet would run as a formula.
func csvCell(v any) string {
	s := text(v)
	if f, ok := v.(float64); ok {
		s = strconv.FormatFloat(f, 'f', -1, 64)
	}
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return "'" + s
		}
	}
	return s
}
