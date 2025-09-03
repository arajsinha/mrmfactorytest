package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mrm_cell/internal/config"
	"path"
	"time"
	"strconv"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"gopkg.in/yaml.v3"
)

const (
	// defaultOpTimeout is a sensible timeout for etcd operations.
	defaultOpTimeout = 5 * time.Second
	// scenarioPrefix is the root for all data related to this application.
	scenarioPrefix = "mrm/scenario"
)

// ExecutionStore provides a structured API for interacting with etcd for a specific execution.
type ExecutionStore struct {
	client *clientv3.Client
	logger *slog.Logger
	sid    string // Scenario ID
	eid    string // Execution ID
}

// NewExecutionStore creates a store for a given scenario and execution.
// The EID can be empty if only accessing scenario-level data (like config).
func NewExecutionStore(client *clientv3.Client, logger *slog.Logger, sid, eid string) *ExecutionStore {
	return &ExecutionStore{
		client: client,
		logger: logger,
		sid:    sid,
		eid:    eid,
	}
}

func (s *ExecutionStore) Client() *clientv3.Client {
	return s.client
}
func (s *ExecutionStore) Logger() *slog.Logger {
	return s.logger
}
func (s *ExecutionStore) SID() string {
	return s.sid
}

func (s *ExecutionStore) fsmConfigKey() string {
	return path.Join(scenarioPrefix, s.sid, "config")
}

func (s *ExecutionStore) fsmStateKey() string {
	return path.Join(scenarioPrefix, s.sid, "current_state")
}

func (s *ExecutionStore) executionPrefix() string {
	return path.Join(scenarioPrefix, s.sid, "execution", s.eid)
}

func (s *ExecutionStore) headerKey() string {
	return path.Join(s.executionPrefix(), "header")
}

func (s *ExecutionStore) tasksPrefix() string {
	return path.Join(s.executionPrefix(), "tasks")
}

func (s *ExecutionStore) taskKey(taskID string) string {
	return path.Join(s.tasksPrefix(), taskID)
}

func (s *ExecutionStore) detailsKey() string {
	return path.Join(s.executionPrefix(), "details")
}

func (s *ExecutionStore) batchCounterKey() string {
	return path.Join(s.executionPrefix(), "batch_counter")
}

// --- Public Methods ---

// GetFSMConfig retrieves the raw YAML config string from etcd and parses it.
func (s *ExecutionStore) GetFSMConfig(ctx context.Context) (*config.Config, error) {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	resp, err := s.client.Get(opCtx, s.fsmConfigKey())
	if err != nil {
		return nil, fmt.Errorf("etcd get failed for config key: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return nil, ErrConfigNotFound
	}

	var cfg config.Config
	if err := yaml.Unmarshal(resp.Kvs[0].Value, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fsm config from etcd: %w", err)
	}
	return &cfg, nil
}

// WriteFSMConfig writes the raw YAML config bytes to etcd.
func (s *ExecutionStore) WriteFSMConfig(ctx context.Context, configYaml []byte) error {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	_, err := s.client.Put(opCtx, s.fsmConfigKey(), string(configYaml))
	return err
}

// GetCurrentFSMState reads the high-level state of the FSM.
func (s *ExecutionStore) GetCurrentFSMState(ctx context.Context) (string, error) {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	resp, err := s.client.Get(opCtx, s.fsmStateKey())
	if err != nil {
		return "", fmt.Errorf("etcd get failed for state key: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return "", ErrStateNotFound
	}
	return string(resp.Kvs[0].Value), nil
}

// SetCurrentFSMState writes the high-level state of the FSM.
func (s *ExecutionStore) SetCurrentFSMState(ctx context.Context, state string) error {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	_, err := s.client.Put(opCtx, s.fsmStateKey(), state)
	return err
}

// InitializeExecution now also creates the batch_counter key.
func (s *ExecutionStore) InitializeExecution(ctx context.Context, tasks []config.Task, iterations map[string]int, targetState string) error {
	if s.eid == "" {
		return fmt.Errorf("cannot initialize execution with an empty EID")
	}
	s.logger.Info("Initializing new execution record in etcd", "sid", s.sid, "eid", s.eid)
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	var ops []clientv3.Op

	header := ExecutionHeader{TargetState: targetState, Status: StatusPending, StartTime: time.Now()}
	headerVal, _ := json.Marshal(header)
	ops = append(ops, clientv3.OpPut(s.headerKey(), string(headerVal)))

	var detailsList []TaskDetail
	for _, task := range tasks {
		// Use the pre-calculated iteration number passed from the FSM.
		iteration := iterations[task.ID]
		taskDetail := TaskDetail{TaskID: task.ID, Status: StatusPending, Iteration: iteration}
		taskVal, _ := json.Marshal(taskDetail)
		ops = append(ops, clientv3.OpPut(s.taskKey(task.ID), string(taskVal)))
		detailsList = append(detailsList, taskDetail)
	}

	detailsVal, _ := json.Marshal(detailsList)
	ops = append(ops, clientv3.OpPut(s.detailsKey(), string(detailsVal)))
	ops = append(ops, clientv3.OpPut(s.batchCounterKey(), "0"))

	resp, err := s.client.Txn(opCtx).Then(ops...).Commit()
	if err != nil {
		return fmt.Errorf("etcd transaction failed for execution init: %w", err)
	}
	if !resp.Succeeded {
		return fmt.Errorf("etcd transaction was not successful for execution init")
	}
	s.logger.Info("Successfully initialized execution record", "task_count", len(tasks))
	return nil
}


// IncrementBatchCounter atomically increments the batch counter and returns the new value.
func (s *ExecutionStore) IncrementBatchCounter(ctx context.Context) (int, error) {
	var newBatchNum int
	_, err := concurrency.NewSTM(s.client, func(stm concurrency.STM) error {
		key := s.batchCounterKey()
		val := stm.Get(key)
		currentNum, err := strconv.Atoi(val)
		if err != nil {
			// If the value is not a number, default to 0 before incrementing.
			currentNum = 0
		}
		newBatchNum = currentNum + 1
		stm.Put(key, strconv.Itoa(newBatchNum))
		return nil
	}, concurrency.WithAbortContext(ctx))

	if err != nil {
		return -1, fmt.Errorf("failed to increment batch counter: %w", err)
	}
	return newBatchNum, nil
}

// GetTask retrieves the details for a single task from its individual key.
func (s *ExecutionStore) GetTask(ctx context.Context, taskID string) (*TaskDetail, error) {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	resp, err := s.client.Get(opCtx, s.taskKey(taskID))
	if err != nil {
		return nil, fmt.Errorf("etcd get failed for task key %s: %w", taskID, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("task %s not found in store", taskID)
	}

	var detail TaskDetail
	if err := json.Unmarshal(resp.Kvs[0].Value, &detail); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task detail for %s: %w", taskID, err)
	}
	return &detail, nil
}

// --- END OF MISSING FUNCTION ---

// UpdateTaskInDetails performs a safe Read-Modify-Write on the /details key.
// It now correctly handles the two return values from NewSTM.
func (s *ExecutionStore) UpdateTaskInDetails(ctx context.Context, updatedTaskDetail TaskDetail) error {
	s.logger.Debug("Updating task in consolidated /details record", "taskID", updatedTaskDetail.TaskID)
	_, err := concurrency.NewSTM(s.client, func(stm concurrency.STM) error { // Corrected assignment
		val := stm.Get(s.detailsKey())
		if val == "" {
			return fmt.Errorf("/details key not found for eid %s", s.eid)
		}
		var currentDetails []TaskDetail
		if err := json.Unmarshal([]byte(val), &currentDetails); err != nil {
			return fmt.Errorf("failed to unmarshal /details json: %w", err)
		}
		found := false
		for i, detail := range currentDetails {
			if detail.TaskID == updatedTaskDetail.TaskID {
				currentDetails[i] = updatedTaskDetail
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("task %s not found in /details record", updatedTaskDetail.TaskID)
		}
		newVal, err := json.Marshal(currentDetails)
		if err != nil {
			return fmt.Errorf("failed to marshal updated /details json: %w", err)
		}
		stm.Put(s.detailsKey(), string(newVal))
		return nil
	})

	if err != nil {
		s.logger.Error("Failed to update /details record", "taskID", updatedTaskDetail.TaskID, "error", err)
	}
	return err
}

// UpdateTask updates the record for a single task.
func (s *ExecutionStore) UpdateTask(ctx context.Context, detail TaskDetail) error {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	val, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("failed to marshal task detail for update: %w", err)
	}

	_, err = s.client.Put(opCtx, s.taskKey(detail.TaskID), string(val))
	return err
}

// GetAllTasks retrieves all task details for the current execution.
func (s *ExecutionStore) GetAllTasks(ctx context.Context) ([]TaskDetail, error) {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	resp, err := s.client.Get(opCtx, s.tasksPrefix(), clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("etcd get failed for tasks prefix: %w", err)
	}

	tasks := make([]TaskDetail, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var detail TaskDetail
		if err := json.Unmarshal(kv.Value, &detail); err != nil {
			s.logger.Warn("Failed to unmarshal task detail from etcd, skipping", "key", string(kv.Key), "error", err)
			continue
		}
		tasks = append(tasks, detail)
	}
	return tasks, nil
}

// GetHeader retrieves the execution header from its key.
func (s *ExecutionStore) GetHeader(ctx context.Context) (*ExecutionHeader, error) {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	resp, err := s.client.Get(opCtx, s.headerKey())
	if err != nil {
		return nil, fmt.Errorf("etcd get failed for header key: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("header not found for eid %s", s.eid)
	}

	var header ExecutionHeader
	if err := json.Unmarshal(resp.Kvs[0].Value, &header); err != nil {
		return nil, fmt.Errorf("failed to unmarshal execution header: %w", err)
	}
	return &header, nil
}

// UpdateHeader updates the header record for the current execution.
func (s *ExecutionStore) UpdateHeader(ctx context.Context, header ExecutionHeader) error {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	val, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("failed to marshal header for update: %w", err)
	}

	_, err = s.client.Put(opCtx, s.headerKey(), string(val))
	return err
}

func (s *ExecutionStore) activeEIDKey() string {
	return path.Join(scenarioPrefix, s.sid, "active_eid")
}

func (s *ExecutionStore) SetActiveEID(ctx context.Context, eid string) error {
	opCtx, cancel := context.WithTimeout(ctx, defaultOpTimeout)
	defer cancel()

	_, err := s.client.Put(opCtx, s.activeEIDKey(), eid)
	return err
}
