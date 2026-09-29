package staffnotify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// shown is the only attributes an alert repeats. An allowlist rather than everything but the
// secrets: the framework's logger redacts what it writes, but this sits in front of it and sees
// the record before it does, so what is not named here is not sent.
var shown = []string{"error", "err", "panic", "method", "path", "request_id", "job", "reason",
	"account_id", "org", "project", "invoice", "endpoint", "detail"}

// Handler wraps a logger's handler and raises an alert for every record at ERROR or above, which is
// what a panic the framework recovered is logged as, and what this codebase logs for a failure
// nobody is going to see otherwise. Everything still goes to the wrapped handler, unchanged.
type Handler struct {
	inner     slog.Handler
	alerts    *Alerts
	component string
	attrs     []slog.Attr
}

// NewHandler returns inner itself when there is no alerts channel, so the unconfigured case costs
// nothing per record.
func NewHandler(inner slog.Handler, alerts *Alerts, component string) slog.Handler {
	if alerts == nil {
		return inner
	}
	return &Handler{inner: inner, alerts: alerts, component: component}
}

// Enabled defers to the wrapped handler.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle raises the alert, then writes the record.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		h.raise(r)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs keeps the attributes for the alert as well as passing them on.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	kept := append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &Handler{inner: h.inner.WithAttrs(attrs), alerts: h.alerts, component: h.component, attrs: kept}
}

// WithGroup passes the group on. Attributes inside a group are not shown in an alert.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), alerts: h.alerts, component: h.component, attrs: h.attrs}
}

func (h *Handler) raise(r slog.Record) {
	var lines []string
	add := func(a slog.Attr) bool {
		key := strings.ToLower(a.Key)
		for _, k := range shown {
			if key == k {
				lines = append(lines, fmt.Sprintf("%s: %s", a.Key, clip(a.Value.Resolve().String(), 300)))
				break
			}
		}
		return len(lines) < 8
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(add)
	h.alerts.Raise(h.component+"|"+r.Message, h.component,
		fmt.Sprintf("[critical] %s: %s", h.component, clip(r.Message, 200)),
		"", lines...)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
