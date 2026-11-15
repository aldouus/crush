package cluster

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
	attrs   []slog.Attr
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	r = r.Clone()
	r.AddAttrs(h.attrs...)
	h.records = append(h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &captureView{h: h, attrs: as}
}

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

type captureView struct {
	h     *captureHandler
	attrs []slog.Attr
}

func (v *captureView) Enabled(context.Context, slog.Level) bool { return true }

func (v *captureView) Handle(ctx context.Context, r slog.Record) error {
	r = r.Clone()
	r.AddAttrs(v.attrs...)
	return v.h.Handle(ctx, r)
}

func (v *captureView) WithAttrs(as []slog.Attr) slog.Handler {
	return &captureView{h: v.h, attrs: append(append([]slog.Attr{}, v.attrs...), as...)}
}

func (v *captureView) WithGroup(string) slog.Handler { return v }

func recordAttrs(r slog.Record) map[string]string {
	out := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
}

func TestNATSLogger(t *testing.T) {
	t.Parallel()
	h := &captureHandler{}
	l := newNATSLogger(slog.New(h), "node-a", false)
	l.Noticef("hello %d", 1)
	l.Warnf("warn")
	l.Errorf("err")
	l.Fatalf("fatal")
	l.Debugf("debug")
	l.Tracef("trace")

	require.Len(t, h.records, 4)
	levels := []slog.Level{slog.LevelInfo, slog.LevelWarn, slog.LevelError, slog.LevelError}
	for i, r := range h.records {
		require.Equal(t, levels[i], r.Level)
		attrs := recordAttrs(r)
		require.Equal(t, "nats", attrs["component"])
		require.Equal(t, "node-a", attrs["node"])
	}
	require.Equal(t, "NATS: hello 1", h.records[0].Message)

	dh := &captureHandler{}
	dl := newNATSLogger(slog.New(dh), "node-a", true)
	dl.Debugf("debug")
	dl.Tracef("trace")
	require.Len(t, dh.records, 2)
	require.Equal(t, slog.LevelDebug, dh.records[0].Level)
}
