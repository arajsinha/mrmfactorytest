package store

import (
	"errors"
	"time"
)

// ErrStateNotFound is returned when the FSM state key is not found in etcd.
var ErrStateNotFound = errors.New("fsm state not found in etcd")

// ErrConfigNotFound is returned when the FSM config key is not found in etcd.
var ErrConfigNotFound = errors.New("fsm config not found in etcd")

// ExecutionStatus defines the possible states for an execution or a single task.
type ExecutionStatus string

const (
	// StatusPending is the initial state before execution begins.
	StatusPending ExecutionStatus = "pending"
	// StatusInProgress indicates that the task or execution is actively running.
	StatusInProgress ExecutionStatus = "in-progress"
	// StatusCompleted indicates successful completion.
	StatusCompleted ExecutionStatus = "completed"
	// StatusFailed indicates that an error occurred.
	StatusFailed ExecutionStatus = "failed"
	// StatusSkipped indicates that the task was skipped due to its own conditions.
	StatusSkipped ExecutionStatus = "skipped"
	// StatusBlocked indicates that the task could not run because a dependency failed.
	StatusBlocked ExecutionStatus = "blocked"
)

// ExecutionHeader represents the overall status of a single execution run (EID).
// This will be stored as a JSON object in the '/header' key.
type ExecutionHeader struct {
	Status    ExecutionStatus `json:"status"`
	TargetState string          `json:"targetState"` // <-- ADDED
	StartTime time.Time       `json:"startTime"`
	EndTime   *time.Time      `json:"endTime,omitempty"` // Use a pointer to allow for null end time
	Error     string          `json:"error,omitempty"`
}

// TaskDetail represents the detailed status and result of a single task.
// This will be stored as a JSON object in the '/tasks/<TaskID>' key.
type TaskDetail struct {
	TaskID    string          `json:"taskID"`
	Status    ExecutionStatus `json:"status"`
	Iteration int             `json:"iteration"` // <-- ADDED
	StartTime *time.Time      `json:"startTime,omitempty"` // Pointer to allow for null start time
	EndTime   *time.Time      `json:"endTime,omitempty"`
	Result    interface{}     `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}