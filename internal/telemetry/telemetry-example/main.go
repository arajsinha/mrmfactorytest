package main

import (
	"context"
	"log/slog"
	"time"

	"mrm_cell/internal/telemetry"
)

func main() {
	// Load configuration from telemetry.yaml in the root directory
	cfg, err := telemetry.LoadConfig("telemetry.yaml")
	if err != nil {
		slog.Error("Failed to load telemetry config, using defaults", "error", err)
		return
	}

	// Initialize telemetry
	ctx := context.Background()
	shutdown, err := telemetry.Initialize(ctx, cfg)
	if err != nil {
		slog.Error("Failed to initialize telemetry", "error", err)
		return
	}
	defer shutdown(ctx)

	// Use the logger
	// logger := telemetry.Logger()

	slog.Info("Telemetry example started",
		"service", cfg.ServiceName,
		"version", "1.0.0",
		"log_format", cfg.LogFormat,
		"log_outputs", cfg.LogOutputs,
		"log_file_path", cfg.LogFilePath)

	slog.Debug("This is a debug message - you might not see this unless log_level is debug")
	slog.Info("Processing request", "user_id", 123, "action", "login")
	slog.Warn("This is a warning", "component", "auth", "reason", "invalid_token")
	slog.Error("This is an error", "error", "connection_failed", "retry_count", 3)

	// You can also use the default slog functions directly
	slog.Info("Using slog directly works too", "timestamp", time.Now())

	// Demonstrate structured logging with context
	slog.With("request_id", "req-123", "user", "john").
		Info("User action completed", "duration_ms", 250)

	// Test different log levels
	slog.Info("Testing different log levels...")
	slog.Debug("Debug level message", "level", "debug")
	slog.Info("Info level message", "level", "info")
	slog.Warn("Warning level message", "level", "warn")
	slog.Error("Error level message", "level", "error")

	// Simulate some work
	slog.Info("Simulating work for 2 seconds...")
	time.Sleep(2 * time.Second)

	slog.Info("Telemetry example shutting down gracefully",
		"total_runtime_seconds", 2,
		"final_message", true)
}
