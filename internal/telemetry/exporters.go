package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"google.golang.org/grpc/credentials"
)

// ExporterType represents different types of log exporters
type ExporterType string

const (
	ConsoleExporter ExporterType = "console"
	FileExporter    ExporterType = "file"
	OTLPExporter    ExporterType = "otlp"
)

// LogExporter interface for different exporter implementations
type LogExporter interface {
	CreateHandler(ctx context.Context, cfg *Config, level slog.Level) (slog.Handler, func(context.Context) error, error)
}

// ConsoleLogExporter handles console output
type ConsoleLogExporter struct{}

func (c *ConsoleLogExporter) CreateHandler(ctx context.Context, cfg *Config, level slog.Level) (slog.Handler, func(context.Context) error, error) {
	var handler slog.Handler

	if strings.ToLower(cfg.LogFormat) == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	}

	// Console doesn't need shutdown
	return handler, func(context.Context) error { return nil }, nil
}

// FileLogExporter handles file output
type FileLogExporter struct {
	file *os.File
}

func (f *FileLogExporter) CreateHandler(ctx context.Context, cfg *Config, level slog.Level) (slog.Handler, func(context.Context) error, error) {
	// Open or create log file
	file, err := os.OpenFile(cfg.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, nil, err
	}

	f.file = file

	var handler slog.Handler

	if strings.ToLower(cfg.LogFormat) == "json" {
		handler = slog.NewJSONHandler(file, &slog.HandlerOptions{
			Level: level,
		})
	} else {
		handler = slog.NewTextHandler(file, &slog.HandlerOptions{
			Level: level,
		})
	}

	// Return handler and shutdown function
	shutdownFunc := func(context.Context) error {
		if f.file != nil {
			return f.file.Close()
		}
		return nil
	}

	return handler, shutdownFunc, nil
}

// OTLPLogExporter handles OTLP output
type OTLPLogExporter struct {
	loggerProvider *log.LoggerProvider
}

func (o *OTLPLogExporter) CreateHandler(ctx context.Context, cfg *Config, level slog.Level) (slog.Handler, func(context.Context) error, error) {
	// Create OTLP exporter
	var exporterOpts []otlploggrpc.Option
	exporterOpts = append(exporterOpts, otlploggrpc.WithEndpoint(cfg.OtelExporter.Endpoint))

	if cfg.OtelExporter.Insecure {
		exporterOpts = append(exporterOpts, otlploggrpc.WithInsecure())
	} else if cfg.OtelExporter.Cert != "" && cfg.OtelExporter.Key != "" && cfg.OtelExporter.ServerCA != "" {
		cert, err := tls.X509KeyPair([]byte(cfg.OtelExporter.Cert), []byte(cfg.OtelExporter.Key))
		if err != nil {
			return nil, nil, err
		}

		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM([]byte(cfg.OtelExporter.ServerCA)) {
			return nil, nil, errors.New("failed to add server CA certificate")
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caPool,
		}
		creds := credentials.NewTLS(tlsConfig)
		exporterOpts = append(exporterOpts, otlploggrpc.WithTLSCredentials(creds))
	}

	if len(cfg.OtelExporter.Headers) > 0 {
		exporterOpts = append(exporterOpts, otlploggrpc.WithHeaders(cfg.OtelExporter.Headers))
	}

	exporter, err := otlploggrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, nil, err
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		slog.Warn("Failed to create resource, using default", "error", err)
		res = resource.Default()
	}

	// Create logger provider
	o.loggerProvider = log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exporter)),
		log.WithResource(res),
	)

	// Set global logger provider
	global.SetLoggerProvider(o.loggerProvider)

	// Create OTEL slog handler
	otelHandler := otelslog.NewHandler(cfg.ServiceName)

	// Return handler and shutdown function
	shutdownFunc := func(ctx context.Context) error {
		if o.loggerProvider != nil {
			return o.loggerProvider.Shutdown(ctx)
		}
		return nil
	}

	return otelHandler, shutdownFunc, nil
}

// ExporterFactory creates exporters based on type
type ExporterFactory struct{}

func (f *ExporterFactory) CreateExporter(exporterType ExporterType) LogExporter {
	switch exporterType {
	case ConsoleExporter:
		return &ConsoleLogExporter{}
	case FileExporter:
		return &FileLogExporter{}
	case OTLPExporter:
		return &OTLPLogExporter{}
	default:
		return &ConsoleLogExporter{} // fallback
	}
}
