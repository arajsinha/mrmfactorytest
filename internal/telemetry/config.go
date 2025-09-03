package telemetry

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds telemetry configuration
type Config struct {
	ServiceName  string   `yaml:"service_name"`
	LogLevel     string   `yaml:"log_level"`
	LogFormat    string   `yaml:"log_format"` // New: json, text
	LogOutputs   []string `yaml:"log_outputs"`
	LogFilePath  string   `yaml:"log_file_path"` // New: file path for file exporter
	OtelExporter struct {
		Endpoint string            `yaml:"endpoint"`
		Insecure bool              `yaml:"insecure"`
		Headers  map[string]string `yaml:"headers"`
		Cert     string            `yaml:"cert"`
		Key      string            `yaml:"key"`
		ServerCA string            `yaml:"server_ca"`
	} `yaml:"otel_exporter"`
}

// LoadConfig loads configuration from YAML file and applies ENV overrides
func LoadConfig(path string) (*Config, error) {
	cfg := &Config{}

	// Load from YAML if path provided
	if path != "" {

		data, err := os.ReadFile(path)
		if err != nil {
			return cfg, err // Return defaults on error
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return cfg, err // Return defaults on error
		}
	}

	return cfg, nil
}
