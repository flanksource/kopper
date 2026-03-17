package kopper

import (
	"context"
	"log/slog"

	"github.com/flanksource/commons/logger"
	"github.com/go-logr/logr"
)

// All Error logs are emitted
// When verbosity is set to 2 (-vv) along with property(kopper.logs=true): Warn logs are emitted as well
// When verbosity is set to 4 (-vvvv) : All logs are emitted

var klog = logger.GetLogger("kopper")

func init() {
	klog.SetLogLevel(logger.Error)
}

// computeKopperLogLevel determines the log level for the kopper logger.
// Priority: global level >= Trace2 (4) > kopper.logs=true > default (Error).
// The log.level.kopper property is handled separately by the logger infrastructure.
func computeKopperLogLevel(kopperLogsEnabled bool, globalLevel logger.LogLevel) logger.LogLevel {
	if globalLevel >= logger.Trace2 {
		return globalLevel
	}
	if kopperLogsEnabled {
		return logger.Warn
	}
	return logger.Error
}

// shiftLevel maps slog levels from controller-runtime conventions to
// flanksource/commons/logger conventions, shifting each level down by
// one semantic step:
//
//	slog Error (8)          → commons Warn  (slog 4)
//	slog Warn  (4)          → commons Info  (slog 0)
//	slog Info  (0)          → commons Debug (slog -4)
//	slog Debug (-4) & below → commons Trace (slog -5) & below
func shiftLevel(level slog.Level) slog.Level {
	switch {
	case level >= slog.LevelError:
		return slog.LevelWarn
	case level >= slog.LevelWarn:
		return slog.LevelInfo
	case level >= slog.LevelInfo:
		return slog.LevelDebug
	case level >= slog.LevelDebug:
		return logger.SlogTraceLevel
	default:
		return logger.SlogTraceLevel + (level - slog.LevelDebug)
	}
}

// levelShiftHandler wraps an slog.Handler and shifts all log levels down
// so that controller-runtime logs are mapped to appropriate commons/logger levels.
type levelShiftHandler struct {
	handler slog.Handler
}

func (h *levelShiftHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, shiftLevel(level))
}

func (h *levelShiftHandler) Handle(ctx context.Context, record slog.Record) error {
	record.Level = shiftLevel(record.Level)
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
