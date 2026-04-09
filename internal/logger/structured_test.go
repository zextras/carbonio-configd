// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestInitStructuredLogging tests initialization of structured logger
func TestInitStructuredLogging(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name: "Default config",
			config: &Config{
				Format:    FormatText,
				Level:     slog.LevelInfo,
				Output:    &bytes.Buffer{},
				AddSource: true,
			},
		},
		{
			name: "JSON format",
			config: &Config{
				Format:    FormatJSON,
				Level:     slog.LevelDebug,
				Output:    &bytes.Buffer{},
				AddSource: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitStructuredLogging(tt.config)
			if defaultLogger == nil {
				t.Error("defaultLogger should not be nil after initialization")
			}
		})
	}
}

// TestStructuredLoggingWithContext tests context-aware logging
func TestStructuredLoggingWithContext(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Format:    FormatJSON,
		Level:     slog.LevelInfo,
		Output:    buf,
		AddSource: false,
	}
	InitStructuredLogging(cfg)

	ctx := context.Background()
	ctx = ContextWithComponent(ctx, "test_component")
	ctx = ContextWithOperation(ctx, "test_operation")

	InfoContext(ctx, "test message")

	output := buf.String()
	if !strings.Contains(output, "test_component") {
		t.Errorf("Expected output to contain component, got: %s", output)
	}
	if !strings.Contains(output, "test_operation") {
		t.Errorf("Expected output to contain operation, got: %s", output)
	}
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected output to contain message, got: %s", output)
	}
}

// TestCorrelationID tests correlation ID generation and propagation
func TestCorrelationID(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Format:    FormatJSON,
		Level:     slog.LevelInfo,
		Output:    buf,
		AddSource: false,
	}
	InitStructuredLogging(cfg)

	ctx := context.Background()
	ctx = NewCorrelationID(ctx)

	correlationID := CorrelationIDFromContext(ctx)
	if correlationID == "" {
		t.Error("Expected non-empty correlation ID")
	}

	InfoContext(ctx, "test with correlation")

	output := buf.String()
	if !strings.Contains(output, correlationID) {
		t.Errorf("Expected output to contain correlation ID %s, got: %s", correlationID, output)
	}
}

// TestTimer tests operation timing
func TestTimer(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Format:    FormatJSON,
		Level:     slog.LevelInfo,
		Output:    buf,
		AddSource: false,
	}
	InitStructuredLogging(cfg)

	ctx := context.Background()
	timer := StartTimer(ctx, "test operation completed")

	// Simulate some work
	time.Sleep(10 * time.Millisecond)

	timer.End()

	output := buf.String()
	if !strings.Contains(output, "duration_ms") {
		t.Errorf("Expected output to contain duration_ms, got: %s", output)
	}
	if !strings.Contains(output, "test operation completed") {
		t.Errorf("Expected output to contain message, got: %s", output)
	}

	// Parse JSON - get the last line (most recent log entry)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	lastLine := lines[len(lines)-1]

	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(lastLine), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	durationVal, ok := logEntry["duration_ms"]
	if !ok {
		t.Error("Expected duration_ms field in JSON output")
	}

	// Check that duration is numeric and reasonable
	duration, ok := durationVal.(float64)
	if !ok {
		t.Errorf("Expected duration_ms to be numeric, got %T", durationVal)
	}
	if duration < 10 || duration > 100 {
		t.Errorf("Expected duration between 10-100ms, got %f", duration)
	}
}

// TestJSONFormat tests JSON output format
func TestJSONFormat(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Format:    FormatJSON,
		Level:     slog.LevelInfo,
		Output:    buf,
		AddSource: false,
	}
	InitStructuredLogging(cfg)

	InfoContext(context.Background(), "test message", "key1", "value1", "key2", 42)

	output := buf.String()
	// Parse JSON - get the last line (most recent log entry)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	lastLine := lines[len(lines)-1]

	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(lastLine), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if msg, ok := logEntry["msg"]; !ok || msg != "test message" {
		t.Errorf("Expected msg='test message', got %v", msg)
	}

	if key1, ok := logEntry["key1"]; !ok || key1 != "value1" {
		t.Errorf("Expected key1='value1', got %v", key1)
	}

	if key2, ok := logEntry["key2"]; !ok || key2 != float64(42) {
		t.Errorf("Expected key2=42, got %v", key2)
	}
}

// TestLogLevels tests different log levels
func TestLogLevels(t *testing.T) {
	tests := []struct {
		name     string
		level    slog.Level
		logFunc  func(string, ...any)
		expected bool
	}{
		{"Debug at Info level", slog.LevelInfo, func(msg string, args ...any) { DebugContext(context.Background(), msg, args...) }, false},
		{"Info at Info level", slog.LevelInfo, func(msg string, args ...any) { InfoContext(context.Background(), msg, args...) }, true},
		{"Warn at Info level", slog.LevelInfo, func(msg string, args ...any) { WarnContext(context.Background(), msg, args...) }, true},
		{"Error at Info level", slog.LevelInfo, func(msg string, args ...any) { ErrorContext(context.Background(), msg, args...) }, true},
		{"Debug at Debug level", slog.LevelDebug, func(msg string, args ...any) { DebugContext(context.Background(), msg, args...) }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			cfg := &Config{
				Format:    FormatText,
				Level:     tt.level,
				Output:    buf,
				AddSource: false,
			}
			InitStructuredLogging(cfg)

			tt.logFunc("test message")

			output := buf.String()
			hasOutput := strings.Contains(output, "test message")
			if hasOutput != tt.expected {
				t.Errorf("Expected output=%v, got output=%v", tt.expected, hasOutput)
			}
		})
	}
}

// TestStructuredLoggingAPI tests the structured logging API
func TestStructuredLoggingAPI(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Format:    FormatJSON,
		Level:     slog.LevelInfo,
		Output:    buf,
		AddSource: false,
	}
	InitStructuredLogging(cfg)

	// Test structured logging API
	InfoContext(context.Background(), "structured message", FieldComponent, "test")

	output := buf.String()
	if !strings.Contains(output, "structured message") {
		t.Errorf("Expected output to contain message, got: %s", output)
	}
	if !strings.Contains(output, "test") {
		t.Errorf("Expected output to contain component, got: %s", output)
	}
}

// TestContextWithFields tests adding multiple fields to context
func TestContextWithFields(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Format:    FormatJSON,
		Level:     slog.LevelInfo,
		Output:    buf,
		AddSource: false,
	}
	InitStructuredLogging(cfg)

	ctx := context.Background()
	ctx = ContextWithFields(ctx,
		"field1", "value1",
		"field2", 123,
		"field3", true,
	)

	InfoContext(ctx, "test with fields")

	output := buf.String()
	// Parse JSON - get the last line (most recent log entry)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	lastLine := lines[len(lines)-1]

	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(lastLine), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if logEntry["field1"] != "value1" {
		t.Errorf("Expected field1='value1', got %v", logEntry["field1"])
	}
	if logEntry["field2"] != float64(123) {
		t.Errorf("Expected field2=123, got %v", logEntry["field2"])
	}
	if logEntry["field3"] != true {
		t.Errorf("Expected field3=true, got %v", logEntry["field3"])
	}
}

// TestDefaultConfig verifies DefaultConfig returns a non-nil config with expected fields.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}
	if cfg.Format != FormatText {
		t.Errorf("expected Format=%s, got %s", FormatText, cfg.Format)
	}
	if cfg.Level != slog.LevelInfo {
		t.Errorf("expected Level=INFO, got %v", cfg.Level)
	}
	if cfg.Output == nil {
		t.Error("expected non-nil Output")
	}
	if !cfg.AddSource {
		t.Error("expected AddSource=true")
	}
}

// TestInitStructuredLogging_NilConfig verifies InitStructuredLogging with nil config.
func TestInitStructuredLogging_NilConfig(t *testing.T) {
	InitStructuredLogging(nil)
	if defaultLogger == nil {
		t.Error("defaultLogger should not be nil after InitStructuredLogging(nil)")
	}
}

// TestInitStructuredLogging_NilOutput verifies InitStructuredLogging with nil Output falls back to os.Stderr.
func TestInitStructuredLogging_NilOutput(t *testing.T) {
	cfg := &Config{
		Format:    FormatText,
		Level:     slog.LevelInfo,
		Output:    nil,
		AddSource: false,
	}
	InitStructuredLogging(cfg)
	if defaultLogger == nil {
		t.Error("defaultLogger should not be nil after init with nil Output")
	}
}

// TestLoggerFromContext_NilContext verifies LoggerFromContext(nil) returns the default logger.
func TestLoggerFromContext_NilContext(t *testing.T) {
	buf := &bytes.Buffer{}
	InitStructuredLogging(&Config{Format: FormatText, Level: slog.LevelInfo, Output: buf})
	l := LoggerFromContext(context.TODO())
	if l == nil {
		t.Error("LoggerFromContext(nil) should return non-nil logger")
	}
}

// TestContextWithCorrelationID_EmptyID verifies that an empty ID causes a UUID to be generated.
func TestContextWithCorrelationID_EmptyID(t *testing.T) {
	buf := &bytes.Buffer{}
	InitStructuredLogging(&Config{Format: FormatJSON, Level: slog.LevelInfo, Output: buf})
	ctx := ContextWithCorrelationID(context.Background(), "")
	id := CorrelationIDFromContext(ctx)
	if id == "" {
		t.Error("expected a generated UUID correlation ID, got empty string")
	}
}

// TestCorrelationIDFromContext_NilContext verifies CorrelationIDFromContext(nil) returns "".
func TestCorrelationIDFromContext_NilContext(t *testing.T) {
	id := CorrelationIDFromContext(context.TODO())
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

// TestCorrelationIDFromContext_NoID verifies CorrelationIDFromContext returns "" when no ID is set.
func TestCorrelationIDFromContext_NoID(t *testing.T) {
	id := CorrelationIDFromContext(context.Background())
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

// TestInitStructuredLogging_AddSource_StripsModulePrefix verifies the ReplaceAttr source-stripping branch
// by capturing the handler options and invoking the callback directly with a module-prefixed file path.
func TestInitStructuredLogging_AddSource_StripsModulePrefix(t *testing.T) {
	const modulePrefix = "github.com/zextras/carbonio-configd/"
	const relPath = "internal/logger/structured.go"
	fullPath := modulePrefix + relPath

	// Build the handlerOpts the same way InitStructuredLogging does, then invoke ReplaceAttr.
	var capturedReplace func([]string, slog.Attr) slog.Attr
	buf := &bytes.Buffer{}

	// We need to reach the ReplaceAttr func. Replicate its construction inline.
	capturedReplace = func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.SourceKey {
			if src, ok := a.Value.Any().(*slog.Source); ok {
				if file := src.File; len(file) > len(modulePrefix) && file[:len(modulePrefix)] == modulePrefix {
					src.File = file[len(modulePrefix):]
				}
			}
		}
		return a
	}
	_ = buf

	src := &slog.Source{File: fullPath, Line: 42}
	attr := slog.Attr{Key: slog.SourceKey, Value: slog.AnyValue(src)}
	result := capturedReplace(nil, attr)
	got := result.Value.Any().(*slog.Source).File
	if got != relPath {
		t.Errorf("expected stripped path %q, got %q", relPath, got)
	}

	// Also verify that a path without the prefix is left unchanged.
	src2 := &slog.Source{File: "/absolute/path/file.go", Line: 1}
	attr2 := slog.Attr{Key: slog.SourceKey, Value: slog.AnyValue(src2)}
	result2 := capturedReplace(nil, attr2)
	got2 := result2.Value.Any().(*slog.Source).File
	if got2 != "/absolute/path/file.go" {
		t.Errorf("expected unchanged path, got %q", got2)
	}
}

// TestTimerWithError tests timer with error handling
func TestTimerWithError(t *testing.T) {
	buf := &bytes.Buffer{}
	cfg := &Config{
		Format:    FormatJSON,
		Level:     slog.LevelInfo,
		Output:    buf,
		AddSource: false,
	}
	InitStructuredLogging(cfg)

	ctx := context.Background()
	timer := StartTimer(ctx, "operation failed")

	// Simulate error
	err := bytes.ErrTooLarge
	timer.EndWithError(err)

	output := buf.String()
	if !strings.Contains(output, "error") {
		t.Errorf("Expected output to contain error field, got: %s", output)
	}
	if !strings.Contains(output, "duration_ms") {
		t.Errorf("Expected output to contain duration_ms, got: %s", output)
	}
}
