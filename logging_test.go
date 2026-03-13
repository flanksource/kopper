package kopper

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/flanksource/commons/logger"
	"github.com/go-logr/logr"
)

func TestShiftLevel(t *testing.T) {
	tests := []struct {
		name     string
		input    slog.Level
		expected slog.Level
	}{
		{"slog Error maps to commons Warn", slog.LevelError, slog.LevelWarn},
		{"slog Warn maps to commons Info", slog.LevelWarn, slog.LevelInfo},
		{"slog Info maps to commons Debug", slog.LevelInfo, slog.LevelDebug},
		{"slog Debug maps to commons Trace", slog.LevelDebug, logger.SlogTraceLevel},
		{"slog V(1) maps to commons Trace", slog.Level(-1), logger.SlogTraceLevel},
		{"slog V(2) maps to commons Trace", slog.Level(-2), logger.SlogTraceLevel},
		{"slog V(3) maps to commons Trace", slog.Level(-3), logger.SlogTraceLevel},
		{"below slog Debug maps to below Trace", slog.Level(-5), logger.SlogTraceLevel - 1},
		{"far below slog Debug maps to deep Trace", slog.Level(-6), logger.SlogTraceLevel - 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shiftLevel(tt.input)
			if result != tt.expected {
				t.Errorf("shiftLevel(%d) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestLevelShiftHandler(t *testing.T) {
	tests := []struct {
		name         string
		baseLevel    slog.Level
		logFunc      func(logr.Logger)
		expectLogged bool
		expectLevel  string
	}{
		{
			name:         "info shifted to debug, base at debug level",
			baseLevel:    slog.LevelDebug,
			logFunc:      func(l logr.Logger) { l.Info("test info message") },
			expectLogged: true,
			expectLevel:  "DEBUG",
		},
		{
			name:         "info shifted to debug, base at warn - suppressed",
			baseLevel:    slog.LevelWarn,
			logFunc:      func(l logr.Logger) { l.Info("test info message") },
			expectLogged: false,
		},
		{
			name:         "error shifted to warn, base at warn - logged",
			baseLevel:    slog.LevelWarn,
			logFunc:      func(l logr.Logger) { l.Error(nil, "error message") },
			expectLogged: true,
			expectLevel:  "WARN",
		},
		{
			name:         "error always logged via logr (bypasses level check)",
			baseLevel:    slog.LevelError,
			logFunc:      func(l logr.Logger) { l.Error(nil, "error message") },
			expectLogged: true,
			expectLevel:  "WARN",
		},
		{
			name:         "V(1) shifted to trace, base at trace level",
			baseLevel:    logger.SlogTraceLevel,
			logFunc:      func(l logr.Logger) { l.V(1).Info("debug message") },
			expectLogged: true,
		},
		{
			name:         "V(1) shifted to trace, base at warn - suppressed",
			baseLevel:    slog.LevelWarn,
			logFunc:      func(l logr.Logger) { l.V(1).Info("debug message") },
			expectLogged: false,
		},
		{
			name:         "V(1) shifted to trace, base at debug - suppressed",
			baseLevel:    slog.LevelDebug,
			logFunc:      func(l logr.Logger) { l.V(1).Info("debug message") },
			expectLogged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			baseHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
				Level: tt.baseLevel,
			})
			handler := &levelShiftHandler{handler: baseHandler}
			logger := logr.FromSlogHandler(handler)

			tt.logFunc(logger)

			logged := buf.Len() > 0
			if logged != tt.expectLogged {
				t.Errorf("expected logged=%v, got logged=%v, output: %q", tt.expectLogged, logged, buf.String())
			}

			if tt.expectLogged && tt.expectLevel != "" {
				if !strings.Contains(buf.String(), "level="+tt.expectLevel) {
					t.Errorf("expected level=%s in output, got: %q", tt.expectLevel, buf.String())
				}
			}
		})
	}
}

func TestLevelShiftHandlerEnabled(t *testing.T) {
	tests := []struct {
		name      string
		baseLevel slog.Level
		testLevel slog.Level
		expected  bool
	}{
		{
			name:      "info enabled when base is debug",
			baseLevel: slog.LevelDebug,
			testLevel: slog.LevelInfo,
			expected:  true,
		},
		{
			name:      "info disabled when base is warn (info shifts to debug)",
			baseLevel: slog.LevelWarn,
			testLevel: slog.LevelInfo,
			expected:  false,
		},
		{
			name:      "error enabled when base is warn (error shifts to warn)",
			baseLevel: slog.LevelWarn,
			testLevel: slog.LevelError,
			expected:  true,
		},
		{
			name:      "error disabled when base is error+1 (error shifts to warn)",
			baseLevel: slog.LevelError + 1,
			testLevel: slog.LevelError,
			expected:  false,
		},
		{
			name:      "debug enabled when base is trace (debug shifts to trace)",
			baseLevel: logger.SlogTraceLevel,
			testLevel: slog.LevelDebug,
			expected:  true,
		},
		{
			name:      "debug disabled when base is debug (debug shifts to trace)",
			baseLevel: slog.LevelDebug,
			testLevel: slog.LevelDebug,
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseHandler := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{
				Level: tt.baseLevel,
			})
			handler := &levelShiftHandler{handler: baseHandler}

			result := handler.Enabled(context.Background(), tt.testLevel)
			if result != tt.expected {
				t.Errorf("Enabled(%v) = %v, want %v (base level: %v, shifted: %v)",
					tt.testLevel, result, tt.expected, tt.baseLevel, shiftLevel(tt.testLevel))
			}
		})
	}
}

func TestLevelShiftHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	handler := &levelShiftHandler{handler: baseHandler}

	// WithAttrs should preserve the level shift
	attrHandler := handler.WithAttrs([]slog.Attr{slog.String("key", "value")})
	logger := logr.FromSlogHandler(attrHandler)

	logger.Info("test with attrs")

	output := buf.String()
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected attrs in output, got: %q", output)
	}
	if !strings.Contains(output, "level=DEBUG") {
		t.Errorf("expected level=DEBUG (shifted from info), got: %q", output)
	}
}

func TestLevelShiftHandlerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	handler := &levelShiftHandler{handler: baseHandler}

	// WithGroup should preserve the level shift
	groupHandler := handler.WithGroup("testgroup")
	logger := logr.FromSlogHandler(groupHandler)

	logger.Info("test with group", "nested", "val")

	output := buf.String()
	if !strings.Contains(output, "testgroup.nested=val") {
		t.Errorf("expected grouped attrs in output, got: %q", output)
	}
	if !strings.Contains(output, "level=DEBUG") {
		t.Errorf("expected level=DEBUG (shifted from info), got: %q", output)
	}
}

func TestDefaultWarnLevelSuppression(t *testing.T) {
	// Simulate the default warn level behavior:
	// With warn level, only controller-runtime errors (shifted to warn) should pass through.
	var buf bytes.Buffer
	baseHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})
	handler := &levelShiftHandler{handler: baseHandler}
	logger := logr.FromSlogHandler(handler)

	// Info should be suppressed (shifted to debug, below warn)
	logger.Info("info message")
	if buf.Len() > 0 {
		t.Errorf("info message should be suppressed at warn level, got: %q", buf.String())
	}

	// V(1) debug should be suppressed (shifted to trace, below warn)
	logger.V(1).Info("debug message")
	if buf.Len() > 0 {
		t.Errorf("V(1) debug message should be suppressed at warn level, got: %q", buf.String())
	}

	// Error should be logged (shifted to warn, equals warn)
	logger.Error(nil, "error message")
	if buf.Len() == 0 {
		t.Error("error message should be logged at warn level")
	}
	if !strings.Contains(buf.String(), "error message") {
		t.Errorf("expected error message in output, got: %q", buf.String())
	}
}
