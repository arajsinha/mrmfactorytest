package etcd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/url" // <-- We use the standard library for URLs
	"os"
	"strconv"
	"strings"
	"time"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	// The problematic "types" import has been completely removed.
)

// ... (constants, TLSConfig, Config, and NewDefaultConfig are the same) ...
const (
	defaultTickMs              = 1000
	defaultElectionMs          = 5000
	defaultStartupTimeout      = 600 * time.Second
	defaultDialTimeout         = 5 * time.Second
	defaultOpTimeout           = 30 * time.Second
	defaultClusterToken        = "mrm-etcd-cluster-token"
	defaultInitialClusterState = "new"
)

type TLSConfig struct{ CAFile, ServerCertFile, ServerKeyFile, PeerCertFile, PeerKeyFile, ClientCertFile, ClientKeyFile string }

func (t *TLSConfig) IsEnabled() bool { return t.CAFile != "" }
func (t *TLSConfig) GetScheme() string {
	if t.IsEnabled() {
		return "https"
	}
	return "http"
}

type Config struct {
	NodeID, HostID, DataDir, InitialCluster, ClusterState string
	ClientPort, PeerPort                                  int
	TickMs, ElectionMs                                    uint
	InitialClusterToken                                   string
	StartupTimeout, DialTimeout, OpTimeout                time.Duration
	TLS                                                   TLSConfig
}

func NewDefaultConfig() *Config {
	return &Config{ClusterState: defaultInitialClusterState, TickMs: defaultTickMs, ElectionMs: defaultElectionMs, InitialClusterToken: defaultClusterToken, StartupTimeout: defaultStartupTimeout, DialTimeout: defaultDialTimeout, OpTimeout: defaultOpTimeout}
}
func CreateClientTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
	if !cfg.IsEnabled() {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client key pair: %w", err)
	}
	caCert, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA file: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA certificate to pool")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caCertPool, MinVersion: tls.VersionTLS12}, nil
}

// --- NEW HELPER FUNCTION ---
// parseURLs is our own helper to replace the problematic etcd types package.
// It takes a slice of strings and returns a slice of url.URL.
func parseURLs(urls []string) ([]url.URL, error) {
	parsed := make([]url.URL, len(urls))
	for i, u := range urls {
		p, err := url.Parse(u)
		if err != nil {
			return nil, err
		}
		parsed[i] = *p
	}
	return parsed, nil
}

// newEmbedConfig now uses our own parseURLs helper function.
func newEmbedConfig(cfg *Config) (*embed.Config, error) {
	embedCfg := embed.NewConfig()
	embedCfg.Name = cfg.NodeID
	embedCfg.Dir = cfg.DataDir
	embedCfg.LogLevel = "error"
	embedCfg.InitialCluster = cfg.InitialCluster
	embedCfg.ClusterState = cfg.ClusterState
	embedCfg.InitialClusterToken = cfg.InitialClusterToken
	embedCfg.TickMs = cfg.TickMs
	embedCfg.ElectionMs = cfg.ElectionMs
	scheme := cfg.TLS.GetScheme()

	if cfg.TLS.IsEnabled() {
		embedCfg.ClientTLSInfo = transport.TLSInfo{
			CertFile:       cfg.TLS.ServerCertFile,
			KeyFile:        cfg.TLS.ServerKeyFile,
			TrustedCAFile:  cfg.TLS.CAFile,
			ClientCertAuth: true,
		}
		embedCfg.PeerTLSInfo = transport.TLSInfo{
			CertFile:       cfg.TLS.PeerCertFile,
			KeyFile:        cfg.TLS.PeerKeyFile,
			TrustedCAFile:  cfg.TLS.CAFile,
			ClientCertAuth: true,
		}
	}

	var err error
	embedCfg.ListenPeerUrls, err = parseURLs([]string{fmt.Sprintf("%s://0.0.0.0:%d", scheme, cfg.PeerPort)})
	if err != nil {
		return nil, fmt.Errorf("failed to parse listen peer URLs: %w", err)
	}
	embedCfg.AdvertisePeerUrls, err = parseURLs([]string{fmt.Sprintf("%s://%s:%d", scheme, cfg.HostID, cfg.PeerPort)})
	if err != nil {
		return nil, fmt.Errorf("failed to parse advertise peer URLs: %w", err)
	}
	embedCfg.ListenClientUrls, err = parseURLs([]string{fmt.Sprintf("%s://0.0.0.0:%d", scheme, cfg.ClientPort)})
	if err != nil {
		return nil, fmt.Errorf("failed to parse listen client URLs: %w", err)
	}
	embedCfg.AdvertiseClientUrls, err = parseURLs([]string{fmt.Sprintf("%s://%s:%d", scheme, cfg.HostID, cfg.ClientPort)})
	if err != nil {
		return nil, fmt.Errorf("failed to parse advertise client URLs: %w", err)
	}

	return embedCfg, nil
}

// ... (The rest of the file is unchanged from our last working version) ...
func StartNode(cfg *Config) (*embed.Etcd, error) {
	if _, err := os.Stat(cfg.DataDir); !os.IsNotExist(err) {
		slog.Info("Existing data directory found. Attempting to restart and rejoin cluster.", "dataDir", cfg.DataDir)
		cfg.ClusterState = "existing"
		return startEtcdServer(cfg)
	}
	slog.Info("No data directory found. Treating as a new node.", "dataDir", cfg.DataDir)
	client, canJoin := probeCluster(cfg)
	if !canJoin {
		slog.Info("Probing failed. Starting as the first node of a new cluster.")
		removeDataDir(cfg.DataDir)
		return startNewClusterNode(cfg)
	}
	defer client.Close()
	slog.Info("Successfully probed existing cluster. Proceeding to join as a new member.")
	return joinCluster(client, cfg)
}
func probeCluster(cfg *Config) (*clientv3.Client, bool) {
	clientEndpoints := extractClientEndpoints(cfg.InitialCluster, cfg.TLS.GetScheme())
	if len(clientEndpoints) == 0 {
		slog.Info("No initial cluster defined for probing.")
		return nil, false
	}
	slog.Info("Probing existing cluster...", "endpoints", clientEndpoints)
	clientCfg := clientv3.Config{Endpoints: clientEndpoints, DialTimeout: cfg.DialTimeout}
	if cfg.TLS.IsEnabled() {
		tlsConfig, err := CreateClientTLSConfig(&cfg.TLS)
		if err != nil {
			slog.Warn("Could not create TLS config for probing", "error", err)
			return nil, false
		}
		clientCfg.TLS = tlsConfig
	}
	client, err := clientv3.New(clientCfg)
	if err != nil {
		slog.Warn("Could not create etcd client to probe cluster.", "error", err)
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
	defer cancel()
	if err := client.Sync(ctx); err != nil {
		slog.Warn("Could not connect to or sync with existing cluster.", "error", err)
		client.Close()
		return nil, false
	}
	return client, true
}
func startNewClusterNode(cfg *Config) (*embed.Etcd, error) {
	nodeCfg := *cfg
	nodeCfg.ClusterState = "new"
	if nodeCfg.InitialCluster == "" {
		nodeCfg.InitialCluster = fmt.Sprintf("%s=%s://%s:%d", nodeCfg.NodeID, nodeCfg.TLS.GetScheme(), nodeCfg.HostID, nodeCfg.PeerPort)
	}
	return startEtcdServer(&nodeCfg)
}
func joinCluster(client *clientv3.Client, cfg *Config) (*embed.Etcd, error) {
	opCtx, cancel := context.WithTimeout(context.Background(), cfg.OpTimeout)
	defer cancel()
	if err := removeExistingMember(opCtx, client, cfg.NodeID); err != nil {
		return nil, fmt.Errorf("failed to ensure member is removed before joining: %w", err)
	}
	removeDataDir(cfg.DataDir)
	peerURL := fmt.Sprintf("%s://%s:%d", cfg.TLS.GetScheme(), cfg.HostID, cfg.PeerPort)
	addResp, err := client.MemberAdd(opCtx, []string{peerURL})
	if err != nil {
		return nil, fmt.Errorf("failed to add member '%s': %w", cfg.NodeID, err)
	}
	slog.Info("Successfully added member to cluster.", "nodeID", cfg.NodeID)
	members := addResp.Members
	peerURLs := buildPeerURLMap(members)
	peerURLs[cfg.NodeID] = peerURL
	initialCluster := buildInitialClusterString(peerURLs)
	nodeCfg := *cfg
	nodeCfg.InitialCluster = initialCluster
	nodeCfg.ClusterState = "existing"
	return startEtcdServer(&nodeCfg)
}
func removeDataDir(dataDir string) {
	slog.Info("Removing data directory for clean join", "dataDir", dataDir)
	if err := os.RemoveAll(dataDir); err != nil {
		slog.Warn("Failed to remove data directory", "dataDir", dataDir, "error", err)
	}
}
func removeExistingMember(ctx context.Context, client *clientv3.Client, nodeID string) error {
	slog.Info("Checking for existing member", "nodeID", nodeID)
	listResp, err := client.MemberList(ctx)
	if err != nil {
		return fmt.Errorf("failed to get member list: %w", err)
	}
	for _, member := range listResp.Members {
		if member.Name == nodeID {
			slog.Info("Existing member found. Removing for a clean join.", "nodeID", nodeID, "id", member.ID)
			if _, err := client.MemberRemove(ctx, member.ID); err != nil {
				return fmt.Errorf("failed to remove existing member %s (%d): %w", nodeID, member.ID, err)
			}
			slog.Info("Successfully removed existing member", "nodeID", nodeID, "id", member.ID)
			return nil
		}
	}
	slog.Info("No existing member found with this name.", "nodeID", nodeID)
	return nil
}
func buildPeerURLMap(members []*etcdserverpb.Member) map[string]string {
	peers := make(map[string]string)
	for _, member := range members {
		if member.Name != "" && len(member.PeerURLs) > 0 {
			peers[member.Name] = member.PeerURLs[0]
		}
	}
	return peers
}
func buildInitialClusterString(peerMap map[string]string) string {
	var pairs []string
	for name, url := range peerMap {
		pairs = append(pairs, fmt.Sprintf("%s=%s", name, url))
	}
	return strings.Join(pairs, ",")
}
func startEtcdServer(cfg *Config) (*embed.Etcd, error) {
	slog.Info("Starting etcd server", "nodeID", cfg.NodeID, "dataDir", cfg.DataDir, "initialCluster", cfg.InitialCluster, "clusterState", cfg.ClusterState)
	embedCfg, err := newEmbedConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create embed config: %w", err)
	}
	e, err := embed.StartEtcd(embedCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to start etcd: %w", err)
	}
	select {
	case <-e.Server.ReadyNotify():
		slog.Info("Etcd server is ready!")
		return e, nil
	case <-time.After(cfg.StartupTimeout):
		e.Server.Stop()
		e.Close()
		return nil, fmt.Errorf("etcd server took too long to start")
	}
}
func extractClientEndpoints(initialCluster, scheme string) []string {
	if initialCluster == "" {
		return nil
	}
	slog.Debug("Extracting client endpoints", "initialCluster", initialCluster, "scheme", scheme)
	var endpoints []string
	parts := strings.Split(initialCluster, ",")
	for _, part := range parts {
		if !strings.Contains(part, "=") {
			continue
		}
		peerURLStr := strings.SplitN(part, "=", 2)[1]
		peerURL, err := url.Parse(peerURLStr)
		if err != nil {
			slog.Warn("Could not parse peer URL from initial cluster string", "part", part, "error", err)
			continue
		}
		peerPortStr := peerURL.Port()
		if peerPortStr == "" {
			slog.Warn("Peer URL missing port, skipping", "peerURL", peerURLStr)
			continue
		}
		peerPort, err := strconv.Atoi(peerPortStr)
		if err != nil {
			slog.Warn("Could not parse peer port", "peerURL", peerURLStr, "error", err)
			continue
		}
		clientPort := peerPort - 1
		endpoint := fmt.Sprintf("%s://%s:%d", scheme, peerURL.Hostname(), clientPort)
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}
