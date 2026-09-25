package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Column types, as the builder draws them.
const (
	TypeString   = "string"
	TypeNumber   = "number"
	TypeMoney    = "money"
	TypePercent  = "percent"
	TypeTime     = "time"
	TypeCountry  = "country"
	TypePlatform = "platform"
)

// Column is one column of a result.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

// Row is one row: text as strings, numbers as float64, times as milliseconds, nil when empty.
type Row = map[string]any

// Result is what a node produced.
type Result struct {
	Columns []Column
	Rows    []Row
	Err     string
}

// Sources are the columns of each source, as the builder names them.
var Sources = map[string][]Column{
	"events": {{"event", "Event", TypeString}, {"player_id", "Player", TypeString}, {"country", "Country", TypeCountry},
		{"platform", "Platform", TypePlatform}, {"at", "Time", TypeTime}, {"level", "Level", TypeNumber}},
	"players": {{"player_id", "Player", TypeString}, {"country", "Country", TypeCountry}, {"platform", "Platform", TypePlatform},
		{"first_seen", "First seen", TypeTime}, {"sessions", "Sessions", TypeNumber}, {"spend", "Spend", TypeMoney},
		{"payer", "Payer", TypeString}},
	"purchases": {{"purchase_id", "Purchase", TypeString}, {"player_id", "Player", TypeString}, {"product", "Product", TypeString},
		{"price", "Price", TypeMoney}, {"country", "Country", TypeCountry}, {"store", "Store", TypeString},
		{"status", "Status", TypeString}, {"at", "Time", TypeTime}},
	"sessions": {{"session_id", "Session", TypeString}, {"player_id", "Player", TypeString}, {"country", "Country", TypeCountry},
		{"platform", "Platform", TypePlatform}, {"at", "Started", TypeTime}, {"minutes", "Minutes", TypeNumber}},
}

// SourceReader reads a source's rows: query.Engine's ReportSource, scoped.
type SourceReader interface {
	Read(ctx context.Context, source string, days int, event string, limit int, now time.Time) ([]Row, error)
}

// Limits on what a run holds in memory.
const (
	// SourceRows is the most rows a source hands on.
	SourceRows = 50_000
	// JoinRows is the most a join produces.
	JoinRows = 100_000
)

// evaluate runs every node in order over results already computed, the builder's rules exactly.
type evaluator struct {
	graph   Graph
	edges   []Edge
	sources SourceReader
	now     time.Time
	results map[string]*Result
}

func newEvaluator(g Graph, sources SourceReader, now time.Time) *evaluator {
	return &evaluator{graph: g, edges: g.lines(), sources: sources, now: now, results: map[string]*Result{}}
}

func fail(format string, args ...any) *Result { return &Result{Err: fmt.Sprintf(format, args...)} }

func (ev *evaluator) input(id, handle string) (*Result, bool) {
	for _, e := range ev.edges {
		if e.Target == id && e.TargetHandle != WhenHandle && (handle == "" || e.TargetHandle == handle) {
			if handle == "" && (e.TargetHandle == "left" || e.TargetHandle == "right") {
				continue
			}
			r, ok := ev.results[e.Source]
			return r, ok
		}
	}
	return nil, false
}

// step runs one node, its inputs having run.
func (ev *evaluator) step(ctx context.Context, n Node) *Result {
	c := n.Data.Config
	switch n.Data.Kind {
	case KindStart, KindFinish:
		return &Result{}
	case KindSchedule:
		if _, err := ParseCron(c.Cron); err != nil {
			return fail("%s", err)
		}
		return &Result{}
	case KindSource:
		source := c.Source
		if source == "" {
			source = "purchases"
		}
		columns, ok := Sources[source]
		if !ok {
			return fail("%q is not a source", source)
		}
		days := 30
		if c.Range != "" {
			d, err := strconv.Atoi(c.Range)
			if err != nil || d < 1 || d > 90 {
				return fail("the range is 7, 30 or 90 days")
			}
			days = d
		}
		event := ""
		if source == "events" {
			event = c.Event
		}
		rows, err := ev.sources.Read(ctx, source, days, event, SourceRows, ev.now)
		if err != nil {
			return fail("the rows could not be read: %s", err)
		}
		return &Result{Columns: columns, Rows: rows}
	case KindJoin:
		left, lok := ev.input(n.ID, "left")
		right, rok := ev.input(n.ID, "right")
		if !lok || !rok {
			return fail("Connect two inputs: left and right.")
		}
		if left.Err != "" || right.Err != "" {
			return fail("An input has a problem to fix first.")
		}
		return join(left, right, c.Mode)
	}

	in, ok := ev.input(n.ID, "")
	if !ok {
		switch n.Data.Kind {
		case KindChart, KindTable, KindExport:
			return fail("Connect a result into it.")
		}
		return fail("Connect rows into it.")
	}
	if in.Err != "" {
		return fail("Its input has a problem to fix first.")
	}
	has := func(key string) bool { return columnOf(in.Columns, key) != nil }

	switch n.Data.Kind {
	case KindFilter:
		var conds []Condition
		for _, cond := range c.Conditions {
			if cond.Column != "" {
				if !has(cond.Column) {
					return fail("%s is not a column here any more.", cond.Column)
				}
				conds = append(conds, cond)
			}
		}
		if len(conds) == 0 {
			return in
		}
		out := &Result{Columns: in.Columns}
		for _, row := range in.Rows {
			hit := c.Match != "any"
			for _, cond := range conds {
				m := matches(row[cond.Column], columnOf(in.Columns, cond.Column).Type, cond, ev.now)
				if c.Match == "any" && m {
					hit = true
					break
				}
				if c.Match != "any" && !m {
					hit = false
					break
				}
			}
			if hit {
				out.Rows = append(out.Rows, row)
			}
		}
		return out
	case KindGroup:
		var metrics []Metric
		for _, m := range c.Metrics {
			if strings.TrimSpace(m.As) != "" {
				metrics = append(metrics, m)
			}
		}
		if len(metrics) == 0 {
			return fail("Add a figure to work out: a count or a sum.")
		}
		var by []string
		for _, k := range c.By {
			if k != "" {
				if !has(k) {
					return fail("%s is not a column here any more.", k)
				}
				by = append(by, k)
			}
		}
		for _, m := range metrics {
			if m.Fn != "count" && (m.Column == "" || !has(m.Column)) {
				if m.Column == "" {
					return fail("Choose the column each figure is worked out from.")
				}
				return fail("%s is not a column here any more.", m.Column)
			}
		}
		bucket := c.Bucket
		if bucket == "" {
			bucket = "day"
		}
		return group(in, by, bucket, metrics)
	case KindFormula:
		name := strings.Join(strings.Fields(c.Name), "_")
		if name == "" {
			return fail("Name the new column.")
		}
		if c.Left == "" || !has(c.Left) {
			return fail("Choose the column it starts from.")
		}
		byColumn := c.Right != "" && has(c.Right)
		constant, err := strconv.ParseFloat(strings.TrimSpace(c.Right), 64)
		if !byColumn && err != nil {
			return fail("Choose a column or a number to work it out with.")
		}
		format := c.Format
		if format == "" {
			format = TypeNumber
		}
		out := &Result{}
		for _, col := range in.Columns {
			if col.Key != name {
				out.Columns = append(out.Columns, col)
			}
		}
		out.Columns = append(out.Columns, Column{Key: name, Label: strings.ReplaceAll(name, "_", " "), Type: format})
		for _, row := range in.Rows {
			next := make(Row, len(row)+1)
			for k, v := range row {
				next[k] = v
			}
			a, aok := number(row[c.Left])
			b, bok := constant, true
			if byColumn {
				b, bok = number(row[c.Right])
			}
			next[name] = nil
			if aok && bok {
				var v float64
				switch c.Op {
				case "+":
					v = a + b
				case "-":
					v = a - b
				case "*":
					v = a * b
				default:
					if b == 0 {
						out.Rows = append(out.Rows, next)
						continue
					}
					v = a / b
				}
				next[name] = math.Round(v*10_000) / 10_000
			}
			out.Rows = append(out.Rows, next)
		}
		return out
	case KindSort:
		if c.Column != "" && !has(c.Column) {
			return fail("%s is not a column here any more.", c.Column)
		}
		rows := append([]Row(nil), in.Rows...)
		if c.Column != "" {
			sort.SliceStable(rows, func(i, j int) bool {
				cmp := compare(rows[i][c.Column], rows[j][c.Column])
				if c.Order == "asc" {
					return cmp < 0
				}
				return cmp > 0
			})
		}
		if c.Limit != nil && *c.Limit > 0 && len(rows) > *c.Limit {
			rows = rows[:*c.Limit]
		}
		return &Result{Columns: in.Columns, Rows: rows}
	case KindNotify:
		out := *in
		if len(c.Channels) == 0 {
			out.Err = "Choose a channel to send to."
		} else if c.When == "threshold" {
			if c.Threshold == nil || c.Threshold.Column == "" || !has(c.Threshold.Column) {
				out.Err = "Choose the figure to watch."
			} else if _, err := strconv.ParseFloat(strings.TrimSpace(c.Threshold.Value), 64); err != nil {
				out.Err = "Set the line the figure is watched against."
			}
		}
		return &out
	case KindChart:
		out := *in
		if c.X == "" || !has(c.X) {
			out.Err = "Choose what runs along the bottom."
		} else {
			found := false
			for _, y := range c.Y {
				found = found || has(y)
			}
			if !found {
				out.Err = "Choose a figure to draw."
			}
		}
		return &out
	}
	return in
}

func columnOf(cols []Column, key string) *Column {
	for i := range cols {
		if cols[i].Key == key {
			return &cols[i]
		}
	}
	return nil
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return fmt.Sprint(v)
}

func matches(value any, kind string, c Condition, now time.Time) bool {
	target := strings.TrimSpace(c.Value)
	switch c.Op {
	case "is":
		return strings.EqualFold(text(value), target)
	case "is_not":
		return !strings.EqualFold(text(value), target)
	case "gt", "lt":
		a, aok := number(value)
		b, bok := number(target)
		if !aok || !bok {
			return false
		}
		if c.Op == "gt" {
			return a > b
		}
		return a < b
	case "contains":
		return strings.Contains(strings.ToLower(text(value)), strings.ToLower(target))
	case "within":
		at, ok := number(value)
		days, _ := strconv.ParseFloat(target, 64)
		return kind == TypeTime && ok && at >= float64(now.UnixMilli())-days*86_400_000
	}
	return false
}

func compare(a, b any) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return -1
	}
	if b == nil {
		return 1
	}
	x, xok := a.(float64)
	y, yok := b.(float64)
	if !xok || !yok {
		if xi, ok := a.(int64); ok {
			x, xok = float64(xi), true
		}
		if yi, ok := b.(int64); ok {
			y, yok = float64(yi), true
		}
	}
	if xok && yok {
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	return strings.Compare(text(a), text(b))
}

func join(left, right *Result, mode string) *Result {
	if columnOf(left.Columns, "player_id") == nil || columnOf(right.Columns, "player_id") == nil {
		return fail("Both inputs need a Player column to join on.")
	}
	var extra []Column
	for _, col := range right.Columns {
		if columnOf(left.Columns, col.Key) == nil {
			extra = append(extra, col)
		}
	}
	index := make(map[string]Row, len(right.Rows))
	for _, row := range right.Rows {
		index[text(row["player_id"])] = row
	}
	out := &Result{Columns: append(append([]Column(nil), left.Columns...), extra...)}
	for _, row := range left.Rows {
		match, found := index[text(row["player_id"])]
		if !found && mode != "left" {
			continue
		}
		joined := make(Row, len(row)+len(extra))
		for k, v := range row {
			joined[k] = v
		}
		for _, col := range extra {
			joined[col.Key] = nil
			if found {
				joined[col.Key] = match[col.Key]
			}
		}
		out.Rows = append(out.Rows, joined)
		if len(out.Rows) >= JoinRows {
			break
		}
	}
	return out
}

func bucketOf(ms float64, bucket string) string {
	t := time.UnixMilli(int64(ms)).UTC()
	switch bucket {
	case "month":
		return t.Format("2006-01")
	case "week":
		t = t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
	}
	return t.Format(time.DateOnly)
}

func group(in *Result, by []string, bucket string, metrics []Metric) *Result {
	type bag struct {
		key  Row
		rows []Row
	}
	groups := map[string]*bag{}
	var order []string
	for _, row := range in.Rows {
		key := Row{}
		for _, col := range by {
			v := row[col]
			if columnOf(in.Columns, col).Type == TypeTime {
				if ms, ok := number(v); ok {
					v = bucketOf(ms, bucket)
				}
			}
			key[col] = v
		}
		id, _ := json.Marshal(key)
		g, ok := groups[string(id)]
		if !ok {
			g = &bag{key: key}
			groups[string(id)] = g
			order = append(order, string(id))
		}
		g.rows = append(g.rows, row)
	}
	out := &Result{}
	for _, col := range by {
		c := *columnOf(in.Columns, col)
		if c.Type == TypeTime {
			c = Column{Key: col, Label: c.Label + " (" + bucket + ")", Type: TypeString}
		}
		out.Columns = append(out.Columns, c)
	}
	for _, m := range metrics {
		kind := TypeNumber
		if src := columnOf(in.Columns, m.Column); src != nil && src.Type == TypeMoney && m.Fn != "count" && m.Fn != "count_distinct" {
			kind = TypeMoney
		}
		out.Columns = append(out.Columns, Column{Key: strings.Join(strings.Fields(m.As), "_"), Label: strings.TrimSpace(m.As), Type: kind})
	}
	for _, id := range order {
		g := groups[id]
		row := Row{}
		for k, v := range g.key {
			row[k] = v
		}
		for _, m := range metrics {
			var values []float64
			for _, r := range g.rows {
				if v, ok := number(r[m.Column]); ok {
					values = append(values, v)
				}
			}
			var v float64
			switch m.Fn {
			case "count":
				v = float64(len(g.rows))
			case "count_distinct":
				seen := map[string]bool{}
				for _, r := range g.rows {
					seen[text(r[m.Column])] = true
				}
				v = float64(len(seen))
			case "sum", "avg":
				for _, x := range values {
					v += x
				}
				if m.Fn == "avg" && len(values) > 0 {
					v /= float64(len(values))
				}
			case "min", "max":
				for i, x := range values {
					if i == 0 || (m.Fn == "min" && x < v) || (m.Fn == "max" && x > v) {
						v = x
					}
				}
			}
			row[strings.Join(strings.Fields(m.As), "_")] = math.Round(v*100) / 100
		}
		out.Rows = append(out.Rows, row)
	}
	if len(by) > 0 {
		sort.SliceStable(out.Rows, func(i, j int) bool { return compare(out.Rows[i][by[0]], out.Rows[j][by[0]]) < 0 })
	}
	return out
}

// ---- notifying ----------------------------------------------------------------

// Outcome is what a Notify decides for a result: whether it sends, why, and what.
type Outcome struct {
	Send     bool
	Reason   string
	Title    string
	Body     string
	Severity string
	Lines    []string
}

var placeholder = regexp.MustCompile(`\{\{\s*([\w.]+)\s*\}\}`)

// WriteCell is a value as the builder writes it.
func WriteCell(v any, kind string) string {
	if v == nil {
		return "–"
	}
	n, isNumber := v.(float64)
	if kind == TypeTime && isNumber {
		return time.UnixMilli(int64(n)).UTC().Format("2006-01-02 15:04")
	}
	if !isNumber {
		if i, ok := v.(int64); ok {
			n, isNumber = float64(i), true
		}
	}
	if !isNumber {
		return text(v)
	}
	switch kind {
	case TypeMoney:
		return "$" + commas(strconv.FormatFloat(n, 'f', 2, 64))
	case TypePercent:
		return strconv.FormatFloat(n*100, 'f', 1, 64) + "%"
	}
	return commas(strconv.FormatFloat(math.Round(n*100)/100, 'f', -1, 64))
}

func commas(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

func fill(template, report string, r *Result, at time.Time) string {
	return placeholder.ReplaceAllStringFunc(template, func(whole string) string {
		name := placeholder.FindStringSubmatch(whole)[1]
		switch name {
		case "report":
			return report
		case "rows":
			return commas(strconv.Itoa(len(r.Rows)))
		case "date":
			return at.UTC().Format("Mon, Jan 2")
		}
		scope, key, _ := strings.Cut(name, ".")
		col := columnOf(r.Columns, key)
		if col == nil {
			return whole
		}
		switch scope {
		case "first":
			if len(r.Rows) == 0 {
				return WriteCell(nil, col.Type)
			}
			return WriteCell(r.Rows[0][key], col.Type)
		case "sum":
			var s float64
			for _, row := range r.Rows {
				if v, ok := number(row[key]); ok {
					s += v
				}
			}
			return WriteCell(s, col.Type)
		}
		return whole
	})
}

// Decide is a Notify's rule over its result: every run, when rows came out, or when the first
// row's figure crosses a line.
func Decide(c Config, report string, r *Result, at time.Time) Outcome {
	o := Outcome{Send: true, Reason: "Sends on every run."}
	switch c.When {
	case "rows":
		o.Send = len(r.Rows) > 0
		if o.Send {
			o.Reason = commas(strconv.Itoa(len(r.Rows))) + " rows came out, so it sends."
		} else {
			o.Reason = "No rows came out, so it stays quiet."
		}
	case "threshold":
		if c.Threshold != nil {
			line, _ := strconv.ParseFloat(strings.TrimSpace(c.Threshold.Value), 64)
			var value any
			if len(r.Rows) > 0 {
				value = r.Rows[0][c.Threshold.Column]
			}
			v, ok := number(value)
			crossed := ok && ((c.Threshold.Op == "gt" && v > line) || (c.Threshold.Op != "gt" && v < line))
			o.Send = crossed
			name := c.Threshold.Column
			kind := TypeNumber
			if col := columnOf(r.Columns, c.Threshold.Column); col != nil {
				name, kind = col.Label, col.Type
			}
			side := "under"
			if c.Threshold.Op == "gt" {
				side = "over"
			}
			written := WriteCell(value, kind)
			if !ok {
				written = "–"
			}
			if crossed {
				o.Reason = fmt.Sprintf("%s is %s, %s %s: it sends.", capital(name), written, side, c.Threshold.Value)
			} else {
				o.Reason = fmt.Sprintf("%s is %s, not %s %s: it stays quiet.", capital(name), written, side, c.Threshold.Value)
			}
		}
	}
	subject := c.Subject
	if subject == "" {
		subject = "{{report}}"
	}
	o.Title = fill(subject, report, r, at)
	o.Body = fill(c.Body, report, r, at)
	o.Severity = c.Severity
	if o.Severity == "" {
		o.Severity = "warning"
	}
	if c.Include == "" || c.Include == "rows" {
		shown := r.Columns
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for i, row := range r.Rows {
			if i == 5 {
				break
			}
			var cells []string
			for _, col := range shown {
				cells = append(cells, col.Label+": "+WriteCell(row[col.Key], col.Type))
			}
			o.Lines = append(o.Lines, strings.Join(cells, " · "))
		}
	}
	return o
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
