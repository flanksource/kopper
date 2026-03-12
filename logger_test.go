package kopper

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/properties"
)

func TestLoggerConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		logLevel       string
		expectError    bool
		expectDebug    bool
		testMessage    string
		messageLevel   string
	}{
		{
			name:         "Error level - should see errors only",
			logLevel:     "error",
			expectError:  true,
			expectDebug:  false,
			testMessage:  "test error message",
			messageLevel: "error",
		},
		{
			name:         "Error level - should not see debug",
			logLevel:     "error",
			expectError:  false,
			expectDebug:  false,
			testMessage:  "test debug message",
			messageLevel: "debug",
		},
		{
			name:         "Debug level - should see errors",
			logLevel:     "debug",
			expectError:  true,
			expectDebug:  false,
			testMessage:  "test error message",
			messageLevel: "error",
		},
		{
			name:         "Debug level - should see debug messages",
			logLevel:     "debug",
			expectError:  false,
			expectDebug:  true,
			testMessage:  "test debug message",
			messageLevel: "debug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset logger for each test
			var buf bytes.Buffer

			// Create a new logger with JSON output for easier parsing
			properties.Set("log.json", "true")
			properties.Set("log.level.kopper", tt.logLevel)

			kopperLogger := logger.GetLogger("kopper")

			// Create a new handler that writes to our buffer
			handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				Level: kopperLogger.Level,
			})
			kopperLogger.Logger = slog.New(handler)

			// Test the appropriate log level
			switch tt.messageLevel {
			case "error":
				kopperLogger.Errorf("[kopper] %s", tt.testMessage)
			case "debug":
				kopperLogger.Debugf("[kopper] %s", tt.testMessage)
			}

			output := buf.String()

			// Check if message appears in output
			containsMessage := strings.Contains(output, tt.testMessage)

			if tt.expectError && !containsMessage {
				t.Errorf("Expected to see error message in output at %s level, but got: %s", tt.logLevel, output)
			}

			if tt.expectDebug && !containsMessage {
				t.Errorf("Expected to see debug message in output at %s level, but got: %s", tt.logLevel, output)
			}

			if !tt.expectError && !tt.expectDebug && containsMessage {
				t.Errorf("Expected NOT to see %s message in output at %s level, but got: %s", tt.messageLevel, tt.logLevel, output)
			}
		})
	}
}

func TestLoggerV2DebugMessages(t *testing.T) {
	tests := []struct {
		name        string
		logLevel    string
		shouldSee   bool
		testMessage string
	}{
		{
			name:        "Error level - should not see V(2) trace",
			logLevel:    "error",
			shouldSee:   false,
			testMessage: "test V(2) message",
		},
		{
			name:        "Debug level - should not see V(2) trace",
			logLevel:    "debug",
			shouldSee:   false,
			testMessage: "test V(2) message",
		},
		{
			name:        "Trace level - should see V(2) trace",
			logLevel:    "trace",
			shouldSee:   true,
			testMessage: "test V(2) message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset logger for each test
			var buf bytes.Buffer

			// Create a new logger with JSON output for easier parsing
			properties.Set("log.json", "true")
			properties.Set("log.level.kopper", tt.logLevel)

			kopperLogger := logger.GetLogger("kopper")

			// Create a new handler that writes to our buffer
			handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				Level: kopperLogger.Level,
			})
			kopperLogger.Logger = slog.New(handler)

			// Test V(2) debug message (used in reconciler for delete, upsert operations)
			kopperLogger.V(2).Infof("[kopper] %s", tt.testMessage)

			output := buf.String()
			containsMessage := strings.Contains(output, tt.testMessage)

			if tt.shouldSee && !containsMessage {
				t.Errorf("Expected to see V(2) message in output at %s level, but got: %s", tt.logLevel, output)
			}

			if !tt.shouldSee && containsMessage {
				t.Errorf("Expected NOT to see V(2) message in output at %s level, but got: %s", tt.logLevel, output)
			}
		})
	}
}

func TestDefaultLogLevel(t *testing.T) {
	// Reset properties to ensure clean state
	properties.Set("log.level.kopper", "")

	// Initialize logger as the manager would
	logger.UseSlog()
	if properties.String("", "log.level.kopper") == "" {
		properties.Set("log.level.kopper", "error")
	}

	kopperLogger := logger.GetLogger("kopper")

	// Verify the default level is error
	level := kopperLogger.GetLevel()
	if level != logger.Error {
		t.Errorf("Expected default log level to be Error (%d), but got %d", logger.Error, level)
	}

	// Verify that debug messages are not logged at error level
	var buf bytes.Buffer
	properties.Set("log.json", "true")

	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: kopperLogger.Level,
	})
	kopperLogger.Logger = slog.New(handler)

	kopperLogger.Debugf("This debug message should not appear")

	output := buf.String()
	if strings.Contains(output, "This debug message should not appear") {
		t.Errorf("Debug message should not appear at default error level, but got: %s", output)
	}

	// Verify that error messages ARE logged at error level
	buf.Reset()
	kopperLogger.Errorf("This error message should appear")

	output = buf.String()
	if !strings.Contains(output, "This error message should appear") {
		t.Errorf("Error message should appear at error level, but got: %s", output)
	}
}

func TestPropertyConfigurationOverride(t *testing.T) {
	// Test that setting the property before initialization works
	properties.Set("log.level.kopper", "debug")

	logger.UseSlog()
	kopperLogger := logger.GetLogger("kopper")

	level := kopperLogger.GetLevel()
	if level != logger.Debug {
		t.Errorf("Expected log level to be Debug (%d) when set via property, but got %d", logger.Debug, level)
	}
}
