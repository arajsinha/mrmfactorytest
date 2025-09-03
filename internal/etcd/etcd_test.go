package etcd_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mrm_cell/internal/etcd"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
)

// E2ETestSuite defines the suite for end-to-end TLS tests.
type E2ETestSuite struct {
	suite.Suite
	tempDataDir string
	etcdServer  *embed.Etcd
	etcdConfig  *etcd.Config
}

// SetupSuite runs once before the tests in the suite.
// It sets up a temporary data directory and starts a TLS-enabled etcd server.
func (s *E2ETestSuite) SetupSuite() {
	// Create a temporary data directory for the test
	tempDir, err := os.MkdirTemp("", "etcd-test-")
	require.NoError(s.T(), err)
	s.tempDataDir = tempDir

	// Configure etcd for the test
	etcdConfig := etcd.NewDefaultConfig()
	etcdConfig.NodeID = "test-node1"
	etcdConfig.HostID = "localhost"
	etcdConfig.DataDir = s.tempDataDir
	etcdConfig.ClientPort = 23790 // Use a non-standard port to avoid conflicts
	etcdConfig.PeerPort = 23800   // Use a non-standard port to avoid conflicts

	// Set up TLS. Assumes certs are in `mrm_cell/certs`.
	// The test should be run from the `mrm_cell` directory.
	certsDir := "../../certs"
	etcdConfig.TLS = etcd.TLSConfig{
		CAFile:         filepath.Join(certsDir, "ca.crt"),
		ServerCertFile: filepath.Join(certsDir, "server.crt"),
		ServerKeyFile:  filepath.Join(certsDir, "server.key"),
		PeerCertFile:   filepath.Join(certsDir, "peer.crt"),
		PeerKeyFile:    filepath.Join(certsDir, "peer.key"),
		ClientCertFile: filepath.Join(certsDir, "client.crt"),
		ClientKeyFile:  filepath.Join(certsDir, "client.key"),
	}
	s.etcdConfig = etcdConfig

	// Start the etcd server
	server, err := etcd.StartNode(etcdConfig)
	require.NoError(s.T(), err)
	s.etcdServer = server

	// Wait for the server to be ready
	<-server.Server.ReadyNotify()
	fmt.Println("Test etcd server is ready.")
}

// TearDownSuite runs once after all tests in the suite have completed.
// It stops the etcd server and cleans up the temporary data directory.
func (s *E2ETestSuite) TearDownSuite() {
	s.etcdServer.Close()
	os.RemoveAll(s.tempDataDir)
	fmt.Println("Test etcd server shut down and cleaned up.")
}

// TestClientCommunication performs a basic PUT/GET cycle using a TLS-enabled client.
func (s *E2ETestSuite) TestClientCommunication() {
	// Create a TLS-enabled client
	tlsConfig, err := etcd.CreateClientTLSConfig(&s.etcdConfig.TLS)
	require.NoError(s.T(), err)

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("https://localhost:%d", s.etcdConfig.ClientPort)},
		DialTimeout: 5 * time.Second,
		TLS:         tlsConfig,
	})
	require.NoError(s.T(), err)
	defer client.Close()

	// Perform a PUT operation
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key, value := "my-secure-key", "my-secure-value"
	_, err = client.Put(ctx, key, value)
	require.NoError(s.T(), err)

	// Perform a GET operation to verify the stored value
	getCtx, getCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer getCancel()
	resp, err := client.Get(getCtx, key)
	require.NoError(s.T(), err)
	require.Len(s.T(), resp.Kvs, 1, "expected one key-value pair")
	require.Equal(s.T(), key, string(resp.Kvs[0].Key))
	require.Equal(s.T(), value, string(resp.Kvs[0].Value))

	fmt.Println("Successfully performed PUT and GET with TLS client.")
}

// TestE2EWithTLS is the entry point for running the test suite.
func TestE2EWithTLS(t *testing.T) {
	// Check if certificates exist before running the suite.
	// If not, skip the test with a helpful message.
	if _, err := os.Stat("../../certs/ca.crt"); os.IsNotExist(err) {
		t.Skip("TLS certificates not found in ../../certs. Run ./generate-certs.sh from the mrm_cell directory. Skipping e2e test.")
	}
	suite.Run(t, new(E2ETestSuite))
}
