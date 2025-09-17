package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	// "mrm_cell/cmd/fsm-app" // <-- THIS IS THE FIX: This unused import has been removed.

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

const (
	leaderElectionPrefix = "mrm/leader/"
	checkInterval        = 15 * time.Second
	initialGracePeriod   = 30 * time.Second
	activeEIDKey         = "mrm/scenario/S001/active_eid"
)

// Monitor handles leader election and cluster health checks.
type Monitor struct {
	client     *clientv3.Client
	logger     *slog.Logger
	nodeID     string
	knownPeers map[uint64]bool
}

// NewMonitor creates a new cluster monitor.
func NewMonitor(client *clientv3.Client, logger *slog.Logger, nodeID string) *Monitor {
	return &Monitor{
		client:     client,
		logger:     logger.With("component", "cluster-monitor"),
		nodeID:     nodeID,
		knownPeers: make(map[uint64]bool),
	}
}

// Start begins the leader election and monitoring process.
func (m *Monitor) Start(ctx context.Context) {
	go m.campaignForLeadership(ctx)
}

// campaignForLeadership continuously tries to become the leader.
func (m *Monitor) campaignForLeadership(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			s, err := concurrency.NewSession(m.client, concurrency.WithTTL(10))
			if err != nil {
				m.logger.Error("Failed to create etcd session", "error", err)
				time.Sleep(5 * time.Second)
				continue
			}
			e := concurrency.NewElection(s, leaderElectionPrefix)
			m.logger.Info("Campaigning for leadership", "node", m.nodeID)
			if err := e.Campaign(ctx, m.nodeID); err != nil {
				m.logger.Error("Error during leadership campaign", "error", err)
				s.Close()
				continue
			}
			m.logger.Info("👑 Acquired leadership. Starting cluster monitoring.", "node", m.nodeID)
			m.runAsLeader(ctx, s.Done())
			s.Close()
			m.logger.Warn("Lost leadership. Re-campaigning...", "node", m.nodeID)
		}
	}
}

// runAsLeader is the main loop for the leader node.
func (m *Monitor) runAsLeader(appCtx context.Context, sessionDone <-chan struct{}) {
	m.logger.Info("Leader is in initial grace period to allow cluster to stabilize...", "duration", initialGracePeriod)
	select {
	case <-time.After(initialGracePeriod):
	case <-sessionDone:
		m.logger.Info("Leader session expired during grace period.")
		return
	case <-appCtx.Done():
		m.logger.Info("Leader stepping down during grace period due to context cancellation.")
		return
	}
	m.logger.Info("Grace period ended. Starting regular health checks.")
	m.checkClusterHealth(appCtx)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.checkClusterHealth(appCtx)
		case <-sessionDone:
			m.logger.Info("Leader session expired.")
			return
		case <-appCtx.Done():
			m.logger.Info("Leader stepping down due to context cancellation.")
			return
		}
	}
}

// checkClusterHealth lists members and actively probes their status.
func (m *Monitor) checkClusterHealth(ctx context.Context) {
	m.logger.Debug("Leader is checking cluster health...")
	listCtx, cancelList := context.WithTimeout(ctx, 5*time.Second)
	resp, err := m.client.MemberList(listCtx)
	cancelList()
	if err != nil {
		m.logger.Error("Leader failed to list cluster members", "error", err)
		return
	}
	healthyPeers := make(map[uint64]bool)
	for _, member := range resp.Members {
		if len(member.ClientURLs) == 0 {
			m.logger.Warn("Member found but has no client URLs yet, skipping health check for now", "name", member.Name, "id", fmt.Sprintf("%x", member.ID))
			healthyPeers[member.ID] = true
			continue
		}
		statusCtx, cancelStatus := context.WithTimeout(ctx, 2*time.Second)
		_, err := m.client.Status(statusCtx, member.ClientURLs[0])
		cancelStatus()
		if err != nil {
			m.logger.Warn("Member failed health check", "name", member.Name, "id", fmt.Sprintf("%x", member.ID), "error", err)
			continue
		}
		healthyPeers[member.ID] = true
	}
	if len(m.knownPeers) > 0 {
		for id := range m.knownPeers {
			if !healthyPeers[id] {
				m.logger.Warn("🔴 Node lost! A peer has become unhealthy.", "peer_id", fmt.Sprintf("%x", id))
				m.triggerFailover(ctx)
				break
			}
		}
	}
	m.knownPeers = healthyPeers
	m.logger.Debug("Cluster health check complete", "healthy_peer_count", len(m.knownPeers))
}

// triggerFailover logs a message. The actual restart is handled by the API.
func (m *Monitor) triggerFailover(ctx context.Context) {
	m.logger.Info("Leader has detected a node failure.")
	m.logger.Warn("Automatic restart is now handled by the API. Please use the /scenario/restart endpoint to retry the last failed EID.")
	// In a fully automated system, this function could call another service
	// or create a Kubernetes Job to trigger the restart.
}