package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/hashicorp/vault/api" // <-- CORRECTED IMPORT
)

// SecretsManager fetches secrets from an OpenBao/Vault server.
type SecretsManager struct {
	client *api.Client // <-- CORRECTED TYPE
	logger *slog.Logger
}

// NewManager creates and authenticates a new OpenBao/Vault client.
func NewManager(logger *slog.Logger) (*SecretsManager, error) {
	baoAddr := os.Getenv("BAO_ADDR")
	baoToken := os.Getenv("BAO_TOKEN")
	if baoAddr == "" || baoToken == "" {
		return nil, fmt.Errorf("BAO_ADDR and BAO_TOKEN environment variables must be set")
	}

	// Use the official HashiCorp Vault client configuration
	config := &api.Config{
		Address: baoAddr,
	}
	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenBao/Vault client: %w", err)
	}
	client.SetToken(baoToken)

	logger.Info("Successfully connected to OpenBao/Vault server", "address", baoAddr)
	return &SecretsManager{client: client, logger: logger}, nil
}

// FetchSecret retrieves a secret from a given path.
func (m *SecretsManager) FetchSecret(ctx context.Context, path string) (map[string]interface{}, error) {
	// The HashiCorp client uses logical.Read to get KVv2 secrets
	secret, err := m.client.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch secret from %s: %w", path, err)
	}
	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("no secret data found at path %s", path)
	}

	// KVv2 secrets have a nested structure with "data" and "metadata" keys. We want the inner "data".
	data, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid secret format at path %s: missing nested 'data' field", path)
	}

	return data, nil
}