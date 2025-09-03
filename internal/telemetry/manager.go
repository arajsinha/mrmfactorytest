package telemetry

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// Package-level guards for singleton behavior
var (
	initOnce   sync.Once
	initErr    error
	shutdownFn func(context.Context) error
)

// Initialize sets up logging once. Concurrent safe.
func Initialize(ctx context.Context, cfg *Config) (func(context.Context) error, error) {
	initOnce.Do(func() {
		shutdownFn, initErr = doInitialize(ctx, cfg)
	})
	return shutdownFn, initErr
}

// doInitialize contains the original initialization logic
func doInitialize(ctx context.Context, cfg *Config) (shutdown func(context.Context) error, err error) {
	var handlers []slog.Handler
	var shutdownFuncs []func(context.Context) error

	// Map log level
	level := parseLogLevel(cfg.LogLevel)

	// Create exporter factory
	factory := &ExporterFactory{}

	// Build requested handlers using exporters
	for _, output := range cfg.LogOutputs {
		exporterType := ExporterType(output)
		exporter := factory.CreateExporter(exporterType)

		handler, shutdownFunc, err := exporter.CreateHandler(ctx, cfg, level)
		if err != nil {
			// Fail open - add console fallback and continue
			slog.Warn("Failed to create exporter, falling back to console",
				"exporter_type", exporterType, "error", err)

			fallbackExporter := factory.CreateExporter(ConsoleExporter)
			handler, shutdownFunc, _ = fallbackExporter.CreateHandler(ctx, cfg, level)
		}

		handlers = append(handlers, handler)
		if shutdownFunc != nil {
			shutdownFuncs = append(shutdownFuncs, shutdownFunc)
		}
	}

	// Fallback to console if no handlers
	if len(handlers) == 0 {
		fallbackExporter := factory.CreateExporter(ConsoleExporter)
		handler, shutdownFunc, _ := fallbackExporter.CreateHandler(ctx, cfg, level)
		handlers = append(handlers, handler)
		if shutdownFunc != nil {
			shutdownFuncs = append(shutdownFuncs, shutdownFunc)
		}
	}

	// Create combined handler
	var finalHandler slog.Handler = newMultiHandler(handlers...)

	// Set default logger
	logger := slog.New(finalHandler)
	slog.SetDefault(logger)

	// Return combined shutdown function
	shutdownFunc := func(ctx context.Context) error {
		var lastErr error
		for _, shutdown := range shutdownFuncs {
			if err := shutdown(ctx); err != nil {
				lastErr = err
			}
		}
		return lastErr
	}
	slog.Info("Telemetry initialized", "service", cfg.ServiceName, "version", "1.0.0", "log_format", cfg.LogFormat, "log_outputs", cfg.LogOutputs, "log_file_path", cfg.LogFilePath)
	return shutdownFunc, nil
}

// Shutdown provides a simple shutdown call for main
func Shutdown(ctx context.Context) error {
	if shutdownFn != nil {
		return shutdownFn(ctx)
	}
	return nil
}

// parseLogLevel converts string to slog.Level
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// multiHandler combines multiple handlers
type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) *multiHandler {
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, record.Level) {
			if err := handler.Handle(ctx, record); err != nil {
				// Continue with other handlers even if one fails
				continue
			}
		}
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithAttrs(attrs)
	}
	return newMultiHandler(newHandlers...)
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithGroup(name)
	}
	return newMultiHandler(newHandlers...)
}
