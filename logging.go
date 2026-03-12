package kopper

import (
	"context"
	"log/slog"

	"github.com/flanksource/commons/logger"
	"github.com/go-logr/logr"
)

// slogLevelShift is the amount to shift slog levels by when adapting
// controller-runtime logs to flanksource/commons/logger levels.
// A shift of -4 maps:
//   - controller-runtime Error (slog 8) → commons Warn (slog 4)
//   - controller-runtime Info/V(0) (slog 0) → commons Debug (slog -4)
//   - controller-runtime V(1) (slog -1) → commons Trace (slog -5)
const slogLevelShift = slog.Level(-4)

// levelShiftHandler wraps an slog.Handler and shifts all log levels down
// so that controller-runtime logs are mapped to appropriate commons/logger levels.
type levelShiftHandler struct {
	handler slog.Handler
}

func (h *levelShiftHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level+slogLevelShift)
}

func (h *levelShiftHandler) Handle(ctx context.Context, record slog.Record) error {
	record.Level = record.Level + slogLevelShift
	return h.handler.Handle(ctx, record)
}

func (h *levelShiftHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelShiftHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *levelShiftHandler) WithGroup(name string) slog.Handler {
	return &levelShiftHandler{handler: h.handler.WithGroup(name)}
}

// NewControllerRuntimeLogger creates a logr.Logger for controller-runtime
// that routes logs through flanksource/commons/logger with level shifting.
// The default log level is set to warn, suppressing most controller-runtime
// noise while allowing important messages through.
func NewControllerRuntimeLogger() logr.Logger {
	l := logger.GetLogger("controller-runtime")
	l.SetLogLevel(logger.Warn)
	slogLogger := l.GetSlogLogger()
	handler := &levelShiftHandler{
		handler: slogLogger.Handler(),
	}
	return logr.FromSlogHandler(handler)
}
