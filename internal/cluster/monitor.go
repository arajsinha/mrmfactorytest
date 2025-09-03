package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	fsm_interface "mrm_cell/cmd/fsm-app"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

const (
	leaderElectionPrefix = "mrm/leader/"
	checkInterval        = 15 * time.Second
	// --- NEW: A grace period for the leader after an election ---
	initialGracePeriod = 30 * time.Second
	activeEIDKey         = "mrm/scenario/S001/active_eid"
)

// ... (Monitor struct and NewMonitor are the same) ...
type Monitor struct {
	client     *clientv3.Client
	logger     *slog.Logger
	nodeID     string
	knownPeers map[uint64]bool
}
func NewMonitor(client *clientv3.Client, logger *slog.Logger, nodeID string) *Monitor {
	return &Monitor{
		client:     client,
		logger:     logger.With("component", "cluster-monitor"),
		nodeID:     nodeID,
		knownPeers: make(map[uint64]bool),
	}
}

// ... (Start and campaignForLeadership are the same) ...
func (m *Monitor) Start(ctx context.Context) { go m.campaignForLeadership(ctx) }
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


// --- THIS FUNCTION IS NOW CORRECTED ---
// It now includes a grace period to allow the cluster to stabilize.
func (m *Monitor) runAsLeader(appCtx context.Context, sessionDone <-chan struct{}) {
	m.logger.Info("Leader is in initial grace period to allow cluster to stabilize...", "duration", initialGracePeriod)

	// Wait for the grace period to pass before starting regular checks.
	select {
	case <-time.After(initialGracePeriod):
		// Grace period finished, proceed.
	case <-sessionDone:
		m.logger.Info("Leader session expired during grace period.")
		return
	case <-appCtx.Done():
		m.logger.Info("Leader stepping down during grace period due to context cancellation.")
		return
	}

	m.logger.Info("Grace period ended. Starting regular health checks.")
	
	// Run the first check immediately after the grace period.
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

// ... (The rest of the file is unchanged) ...
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
func (m *Monitor) triggerFailover(ctx context.Context) {
	m.logger.Info("Leader is triggering a failover restart.")
	resp, err := m.client.Get(ctx, activeEIDKey)
	if err != nil {
		m.logger.Error("Could not get active EID for restart", "error", err)
		return
	}
	if len(resp.Kvs) == 0 {
		m.logger.Info("No active EID found to restart. Nothing to do.")
		return
	}
	eidToRestart := string(resp.Kvs[0].Value)
	if strings.TrimSpace(eidToRestart) == "" {
		m.logger.Warn("Active EID key was empty. Nothing to restart.")
		return
	}
	m.logger.Info("Found EID to restart. Initiating restart process.", "eid", eidToRestart)
	go fsm_interface.RestartExecution("S001", eidToRestart, m.client)
}