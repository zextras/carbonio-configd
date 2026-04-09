// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package logger provides structured logging functionality for configd using log/slog.
// This module implements context-aware logging with standard fields, JSON and text formatters,
// and operation correlation for improved observability.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

// contextKey is a private type for context keys to avoid collisions
type contextKey string

const (
	loggerContextKey    contextKey = "logger"
	correlationIDKey    contextKey = "correlation_id"
	componentContextKey contextKey = "component"
	operationContextKey contextKey = "operation"
)

// Standard field names for structured logging
const (
	unknownComponent   = "unknown"
	FieldComponent     = "component"
	FieldOperation     = "operation"
	FieldService       = "service"
	FieldDurationMS    = "duration_ms"
	FieldCorrelationID = "correlation_id"
	FieldError         = "error"
	FieldErrorType     = "error_type"
	FieldAttempt       = "attempt"
)

var (
	// defaultLogger is the global logger instance
	defaultLogger *slog.Logger
	// logMutexSlog protects logger initialization
	logMutexSlog sync.Mutex
)

// LogFormat represents the output format for logs
type LogFormat string

const (
	// FormatJSON outputs logs in JSON format (machine-readable, for production)
	FormatJSON LogFormat = "json"
	// FormatText outputs logs in human-readable text format (for development)
	FormatText LogFormat = "text"
)

// Log level constants that match slog levels
const (
	LogLevelDebug = slog.LevelDebug
	LogLevelInfo  = slog.LevelInfo
	LogLevelWarn  = slog.LevelWarn
	LogLevelError = slog.LevelError
)

// Config holds configuration for the structured logger
type Config struct {
	// Format specifies the output format (json or text)
	Format LogFormat
	// Level specifies the minimum log level
	Level slog.Level
	// Output specifies the output writer (defaults to os.Stderr)
	Output io.Writer
	// AddSource adds source code position to log entries
	AddSource bool
	// TimeFormat specifies the time format (only used for text format)
	TimeFormat string
}

// DefaultConfig returns the default logger configuration
func DefaultConfig() *Config {
	return &Config{
		Format:     FormatText,
		Level:      slog.LevelInfo,
		Output:     os.Stderr,
		AddSource:  true,
		TimeFormat: time.RFC3339,
	}
}

// InitStructuredLogging initializes the structured logger with the given configuration.
// This should be called once at application startup.
func InitStructuredLogging(cfg *Config) {
	logMutexSlog.Lock()
	defer logMutexSlog.Unlock()

	if cfg == nil {
		cfg = DefaultConfig()
	}

	if cfg.Output == nil {
		cfg.Output = os.Stderr
	}

	var handler slog.Handler

	handlerOpts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.AddSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Shorten source paths: strip the module prefix to show only
			// the relative path from the project root (e.g. "internal/proxy/config.go:60")
			if a.Key == slog.SourceKey {
				if src, ok := a.Value.Any().(*slog.Source); ok {
					const modulePrefix = "github.com/zextras/carbonio-configd/"
					if file := src.File; len(file) > len(modulePrefix) && file[:len(modulePrefix)] == modulePrefix {
						src.File = file[len(modulePrefix):]
					}
				}
			}

			return a
		},
	}

	switch cfg.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(cfg.Output, handlerOpts)
	case FormatText:
		handler = slog.NewTextHandler(cfg.Output, handlerOpts)
	default:
		handler = slog.NewTextHandler(cfg.Output, handlerOpts)
	}

	defaultLogger = slog.New(handler)
	slog.SetDefault(defaultLogger)

	defaultLogger.Info("Structured logger initialized",
		FieldComponent, "logger",
		"format", cfg.Format,
		"level", cfg.Level.String(),
	)
}

// Default returns the default logger instance
func Default() *slog.Logger {
	if defaultLogger == nil {
		InitStructuredLogging(DefaultConfig())
	}

	return defaultLogger
}

// ContextWithLogger returns a new context with the given logger
func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, logger)
}

// LoggerFromContext extracts a logger from the context, or returns the default logger
//
//nolint:revive // LoggerFromContext is idiomatic for context extraction functions
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return Default()
	}

	if logger, ok := ctx.Value(loggerContextKey).(*slog.Logger); ok && logger != nil {
		return logger
	}

	return Default()
}

// ContextWithCorrelationID returns a new context with a correlation ID
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	if correlationID == "" {
		correlationID = uuid.New().String()
	}

	logger := LoggerFromContext(ctx).With(FieldCorrelationID, correlationID)
	ctx = context.WithValue(ctx, correlationIDKey, correlationID)

	return ContextWithLogger(ctx, logger)
}

// NewCorrelationID generates a new correlation ID and returns a context with it
func NewCorrelationID(ctx context.Context) context.Context {
	return ContextWithCorrelationID(ctx, uuid.New().String())
}

// CorrelationIDFromContext extracts the correlation ID from the context
func CorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}

	return ""
}

// ContextWithComponent returns a new context with a component name
func ContextWithComponent(ctx context.Context, component string) context.Context {
	logger := LoggerFromContext(ctx).With(FieldComponent, component)
	ctx = context.WithValue(ctx, componentContextKey, component)

	return ContextWithLogger(ctx, logger)
}

// ContextWithOperation returns a new context with an operation name
func ContextWithOperation(ctx context.Context, operation string) context.Context {
	logger := LoggerFromContext(ctx).With(FieldOperation, operation)
	ctx = context.WithValue(ctx, operationContextKey, operation)

	return ContextWithLogger(ctx, logger)
}

// ContextWithFields returns a new context with additional structured fields
func ContextWithFields(ctx context.Context, args ...any) context.Context {
	logger := LoggerFromContext(ctx).With(args...)
	return ContextWithLogger(ctx, logger)
}

// Timer is a helper for timing operations and logging duration
type Timer struct {
	start  time.Time
	ctx    context.Context
	msg    string
	fields []any
}

// StartTimer starts a new timer for an operation
func StartTimer(ctx context.Context, msg string, fields ...any) *Timer {
	return &Timer{
		start:  time.Now(),
		ctx:    ctx,
		msg:    msg,
		fields: fields,
	}
}

// End logs the operation completion with duration
func (t *Timer) End() {
	duration := time.Since(t.start)
	fields := append([]any{FieldDurationMS, duration.Milliseconds()}, t.fields...)
	LoggerFromContext(t.ctx).Info(t.msg, fields...)
}

// EndWithError logs the operation completion with duration and error
func (t *Timer) EndWithError(err error) {
	duration := time.Since(t.start)
	fields := append([]any{
		FieldDurationMS, duration.Milliseconds(),
		FieldError, err.Error(),
	}, t.fields...)
	LoggerFromContext(t.ctx).Error(t.msg, fields...)
}

// logWithCaller logs a message at the given level, capturing the caller
// at the correct depth (skipping 2 frames: this function + the public wrapper).
func logWithCaller(ctx context.Context, level slog.Level, msg string, args ...any) {
	l := LoggerFromContext(ctx)
	if !l.Enabled(ctx, level) {
		return
	}

	var pcs [1]uintptr
	runtime.Callers(3, pcs[:]) // skip: Callers, logWithCaller, public wrapper

	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(args...)
	_ = l.Handler().Handle(ctx, r)
}

// InfoContext logs an info-level message with context
func InfoContext(ctx context.Context, msg string, args ...any) {
	logWithCaller(ctx, slog.LevelInfo, msg, args...)
}

// DebugContext logs a debug-level message with context
func DebugContext(ctx context.Context, msg string, args ...any) {
	logWithCaller(ctx, slog.LevelDebug, msg, args...)
}

// WarnContext logs a warning-level message with context
func WarnContext(ctx context.Context, msg string, args ...any) {
	logWithCaller(ctx, slog.LevelWarn, msg, args...)
}

// ErrorContext logs an error-level message with context
func ErrorContext(ctx context.Context, msg string, args ...any) {
	logWithCaller(ctx, slog.LevelError, msg, args...)
}

// FatalContext logs a fatal error with context and exits
func FatalContext(ctx context.Context, msg string, args ...any) {
	logWithCaller(ctx, slog.LevelError, msg, args...)
	os.Exit(1)
}
