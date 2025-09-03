package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mrm_cell/internal/config"
	"mrm_cell/internal/fsm"
	"mrm_cell/internal/store"
	"mrm_cell/internal/taskrunner"
	"os"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const scenarioID = "S001"

// Server holds the long-lived application components.
type Server struct {
	EtcdClient *clientv3.Client
	Runner     *taskrunner.Runner
	Logger     *slog.Logger
}

// NewServer creates a new server instance.
func NewServer(etcdClient *clientv3.Client, runner *taskrunner.Runner, logger *slog.Logger) *Server {
	return &Server{
		EtcdClient: etcdClient,
		Runner:     runner,
		Logger:     logger,
	}
}

// ExecuteFSM is the method that handles a new FSM execution.
func (s *Server) ExecuteFSM(command string) {
	ctx := context.Background()
	s.Runner.SetCommand(command)

	baseStore := store.NewExecutionStore(s.EtcdClient, s.Logger, scenarioID, "")

	cfg, err := bootstrapFSMConfig(ctx, s.Logger, baseStore, "fsm-config.yaml")
	if err != nil {
		s.Logger.Error("Could not bootstrap FSM configuration.", "error", err)
		return
	}

	fsmCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	fsmMachine := fsm.NewMachine(fsmCtx, cfg, s.Runner, s.Logger, baseStore)
	fsmMachine.Run(fsmCtx)
}

// RestartFSM is the method that handles restarting a failed execution.
func (s *Server) RestartFSM(sid string, eid string) {
	logger := s.Logger.With("sid", sid, "eid", eid)
	logger.Info("Starting restart process for execution.")

	ctx := context.Background()
	execStore := store.NewExecutionStore(s.EtcdClient, logger, sid, eid)

	fsmConfig, err := bootstrapFSMConfig(ctx, logger, execStore, "fsm-config.yaml")
	if err != nil {
		logger.Error("Restart failed: could not get FSM config.", "error", err)
		return
	}

	header, err := execStore.GetHeader(ctx)
	if err != nil {
		logger.Error("Restart failed: could not get execution header.", "error", err)
		return
	}
	if header.Status == store.StatusCompleted {
		logger.Warn("Execution is already completed. Nothing to restart.")
		return
	}
	
	allTasks, err := execStore.GetAllTasks(ctx)
	if err != nil {
		logger.Error("Restart failed: could not get task details.", "error", err)
		return
	}

	logger.Info("Resetting non-completed tasks to pending status.")
	for i, task := range allTasks {
		if task.Status != store.StatusCompleted {
			allTasks[i].Status = store.StatusPending
			allTasks[i].StartTime = nil; allTasks[i].EndTime = nil; allTasks[i].Result = nil; allTasks[i].Error = ""
		}
	}
	
	header.Status = store.StatusInProgress; header.Error = ""; now := time.Now(); header.StartTime = now; header.EndTime = nil

	if err := execStore.UpdateHeader(ctx, *header); err != nil {
		logger.Error("Restart failed: could not update header.", "error", err)
		return
	}
	for _, task := range allTasks {
		if err := execStore.UpdateTask(ctx, task); err != nil {
			logger.Error("Restart failed: could not update task.", "taskID", task.TaskID, "error", err)
			return
		}
		go execStore.UpdateTaskInDetails(ctx, task)
	}

	logger.Info("State has been reset in etcd. Triggering task runner to resume execution.")
	var tasksToRun []config.Task
	foundTasks := false
	for _, action := range fsmConfig.FSM.Behavior.Actions {
		if action.State == header.TargetState {
			tasksToRun = action.Tasks
			foundTasks = true
			break
		}
	}
	if !foundTasks {
		logger.Error("Restart failed: could not find tasks for target state in the current config.", "state", header.TargetState)
		return
	}
	
	s.Runner.ExecuteActions(ctx, tasksToRun, execStore)
	logger.Info("Restart execution has been handed off to the runner.")
}


func bootstrapFSMConfig(ctx context.Context, logger *slog.Logger, s *store.ExecutionStore, configPath string) (*config.Config, error) {
	cfg, err := s.GetFSMConfig(ctx)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, store.ErrConfigNotFound) {
		return nil, fmt.Errorf("failed to check for FSM config in etcd: %w", err)
	}
	logger.Info("No FSM config in etcd. Bootstrapping from local file.", "path", configPath)
	yamlData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("could not read local config file %s: %w", configPath, err)
	}
	if err := s.WriteFSMConfig(ctx, yamlData); err != nil {
		return nil, fmt.Errorf("failed to write bootstrap config to etcd: %w", err)
	}
	return s.GetFSMConfig(ctx)
}