package cluster

import (
	"context"
	"fmt"
	"log/slog"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

// natsLogger routes the embedded NATS server's logs into slog. Debug and
// trace output is dropped unless debug is set, since it is per-message.
type natsLogger struct {
	log   *slog.Logger
	debug bool
}

var _ natsserver.Logger = (*natsLogger)(nil)

func newNATSLogger(base *slog.Logger, node string, debug bool) *natsLogger {
	if base == nil {
		base = slog.Default()
	}
	return &natsLogger{log: base.With("component", "nats", "node", node), debug: debug}
}

func (l *natsLogger) emit(level slog.Level, format string, v []any) {
	ctx := context.Background()
	if !l.log.Enabled(ctx, level) {
		return
	}
	l.log.Log(ctx, level, "NATS: "+fmt.Sprintf(format, v...))
}

func (l *natsLogger) Noticef(format string, v ...any) { l.emit(slog.LevelInfo, format, v) }
func (l *natsLogger) Warnf(format string, v ...any)   { l.emit(slog.LevelWarn, format, v) }
func (l *natsLogger) Errorf(format string, v ...any)  { l.emit(slog.LevelError, format, v) }

// Fatalf logs at error level; the embedded server must not exit the
// process.
func (l *natsLogger) Fatalf(format string, v ...any) { l.emit(slog.LevelError, format, v) }

func (l *natsLogger) Debugf(format string, v ...any) {
	if l.debug {
		l.emit(slog.LevelDebug, format, v)
	}
}

func (l *natsLogger) Tracef(format string, v ...any) {
	if l.debug {
		l.emit(slog.LevelDebug, format, v)
	}
}
