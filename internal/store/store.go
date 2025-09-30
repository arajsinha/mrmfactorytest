package store

import (
	"context"
	"encoding/json"
	"strconv"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"mrm_cell/internal/config"
	clientv3 "go.etcd.io/etcd/client/v3"
	"gopkg.in/yaml.v3"
)
// ExecutionStore manages all interactions with the etcd data store.
type ExecutionStore struct {
	client *clientv3.Client
	logger *slog.Logger
	sid    string
	eid    string
	mu     sync.Mutex
}

func NewExecutionStore(client *clientv3.Client, logger *slog.Logger, sid, eid string) *ExecutionStore {
	return &ExecutionStore{client: client, logger: logger, sid: sid, eid: eid}
}

func (s *ExecutionStore) SID() string { return s.sid }
func (s *ExecutionStore) EID() string { return s.eid }
func (s *ExecutionStore) Client() *clientv3.Client { return s.client }

// Private helper methods to construct etcd keys
func (s *ExecutionStore) fsmStateKey() string { return fmt.Sprintf("mrm/scenario/%s/current_state", s.sid) }
func (s *ExecutionStore) fsmConfigKey() string { return fmt.Sprintf("mrm/scenario/%s/fsm_config", s.sid) }
func (s *ExecutionStore) executionHeaderKey() string { return fmt.Sprintf("mrm/scenario/%s/execution/%s/header", s.sid, s.eid) }
func (s *ExecutionStore) executionDetailsKey() string { return fmt.Sprintf("mrm/scenario/%s/execution/%s/details", s.sid, s.eid) }
func (s *ExecutionStore) batchCounterKey() string { return fmt.Sprintf("mrm/scenario/%s/execution/%s/batch_counter", s.sid, s.eid) }
func (s *ExecutionStore) activeEIDKey() string { return fmt.Sprintf("mrm/scenario/%s/active_eid", s.sid) }

// GetCurrentFSMState retrieves the FSM's last known state from etcd.
func (s *ExecutionStore) GetCurrentFSMState(ctx context.Context) (string, error) {
	resp, err := s.client.Get(ctx, s.fsmStateKey())
	if err != nil { return "", err }
	if len(resp.Kvs) == 0 { return "", ErrStateNotFound }
	return string(resp.Kvs[0].Value), nil
}

// SetCurrentFSMState persists the FSM's new state to etcd.
func (s *ExecutionStore) SetCurrentFSMState(ctx context.Context, state string) error {
	_, err := s.client.Put(ctx, s.fsmStateKey(), state)
	return err
}

// SetActiveEID sets the currently active Execution ID for the scenario.
func (s *ExecutionStore) SetActiveEID(ctx context.Context, eid string) error {
	_, err := s.client.Put(ctx, s.activeEIDKey(), eid)
	return err
}

// InitializeExecution sets up the initial records for a new workflow execution.
func (s *ExecutionStore) InitializeExecution(ctx context.Context, fromState, toState string, tasks []config.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	header := ExecutionHeader{
		SID:          s.sid,
		EID:          s.eid,
		Status:       StatusInProgress,
		InitialState: fromState,
		TargetState:  toState,
		StartTime:    time.Now(),
	}
	headerJSON, _ := json.Marshal(header)
	
	details := make(map[string]TaskDetail)
	for _, task := range tasks {
		details[task.ID] = TaskDetail{TaskID: task.ID, Status: StatusPending, Iteration: 1} // Simplified iteration
	}
	detailsJSON, _ := json.Marshal(details)

	_, err := s.client.Txn(ctx).
		Then(
			clientv3.OpPut(s.executionHeaderKey(), string(headerJSON)),
			clientv3.OpPut(s.executionDetailsKey(), string(detailsJSON)),
			clientv3.OpPut(s.batchCounterKey(), "0"),
		).
		Commit()
	return err
}

// GetHeader retrieves the header for the current execution.
func (s *ExecutionStore) GetHeader(ctx context.Context) (*ExecutionHeader, error) {
	resp, err := s.client.Get(ctx, s.executionHeaderKey())
	if err != nil { return nil, err }
	if len(resp.Kvs) == 0 { return nil, fmt.Errorf("header not found for eid %s", s.eid) }
	var header ExecutionHeader
	err = json.Unmarshal(resp.Kvs[0].Value, &header)
	return &header, err
}

// UpdateHeader saves the updated header back to etcd.
func (s *ExecutionStore) UpdateHeader(ctx context.Context, header ExecutionHeader) error {
	headerJSON, _ := json.Marshal(header)
	_, err := s.client.Put(ctx, s.executionHeaderKey(), string(headerJSON))
	return err
}

// GetTask retrieves a single task's details.
func (s *ExecutionStore) GetTask(ctx context.Context, taskID string) (*TaskDetail, error) {
	tasks, err := s.GetAllTasks(ctx)
	if err != nil { return nil, err }
	for _, t := range tasks {
		if t.TaskID == taskID {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("task %s not found in execution details", taskID)
}

// GetAllTasks retrieves all task details for the current execution.
func (s *ExecutionStore) GetAllTasks(ctx context.Context) ([]TaskDetail, error) {
	resp, err := s.client.Get(ctx, s.executionDetailsKey())
	if err != nil { return nil, err }
	if len(resp.Kvs) == 0 { return []TaskDetail{}, nil }
	
	var details map[string]TaskDetail
	if err := json.Unmarshal(resp.Kvs[0].Value, &details); err != nil { return nil, err }
	
	var tasks []TaskDetail
	for _, task := range details {
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// UpdateTask updates a single task's state.
func (s *ExecutionStore) UpdateTask(ctx context.Context, detail TaskDetail) error {
    return s.UpdateTaskInDetails(ctx, detail)
}

// UpdateTaskInDetails updates a single task's state within the consolidated details record.
func (s *ExecutionStore) UpdateTaskInDetails(ctx context.Context, detail TaskDetail) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	resp, err := s.client.Get(ctx, s.executionDetailsKey())
	if err != nil || len(resp.Kvs) == 0 { return fmt.Errorf("could not get details to update task: %w", err) }
	
	var details map[string]TaskDetail
	if err := json.Unmarshal(resp.Kvs[0].Value, &details); err != nil { return err }
	
	details[detail.TaskID] = detail
	detailsJSON, _ := json.Marshal(details)
	
	_, err = s.client.Put(ctx, s.executionDetailsKey(), string(detailsJSON))
	return err
}

// IncrementBatchCounter is a placeholder for more complex batching logic.
func (s *ExecutionStore) IncrementBatchCounter(ctx context.Context) (int, error) {
	key := s.batchCounterKey()
	resp, err := s.client.Get(ctx, key)
	if err != nil { return 0, err }

	currentVal := 0
	if len(resp.Kvs) > 0 {
		currentVal, _ = strconv.Atoi(string(resp.Kvs[0].Value))
	}
	
	newVal := currentVal + 1
	_, err = s.client.Put(ctx, key, fmt.Sprintf("%d", newVal))
	return newVal, err
}

// GetFSMConfig retrieves the FSM config from etcd.
func (s *ExecutionStore) GetFSMConfig(ctx context.Context) (*config.Config, error) {
	resp, err := s.client.Get(ctx, s.fsmConfigKey())
	if err != nil { return nil, err }
	if len(resp.Kvs) == 0 { return nil, ErrConfigNotFound }
	
	var cfg config.Config
	if err := yaml.Unmarshal(resp.Kvs[0].Value, &cfg); err != nil { return nil, err }
	return &cfg, err
}

// WriteFSMConfig writes the FSM config to etcd.
func (s *ExecutionStore) WriteFSMConfig(ctx context.Context, data []byte) error {
	_, err := s.client.Put(ctx, s.fsmConfigKey(), string(data))
	return err
}