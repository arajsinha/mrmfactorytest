package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// AgentTask represents the state of a single agent's task in etcd.
type AgentTask struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // e.g., "pending", "in-progress", "completed"
	Input     string    `json:"input"`
	Output    string    `json:"output,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MCPState holds the shared components for our MCP server.
type MCPState struct {
	etcdClient *clientv3.Client
	logger     *slog.Logger
}

// tasksHandler routes requests for the /tasks/ endpoint.
func (s *MCPState) tasksHandler(w http.ResponseWriter, r *http.Request) {
	// This simple router uses the HTTP method to decide what to do.
	// e.g., POST /tasks/ -> createTaskHandler
	// e.g., GET /tasks/task-123 -> getTaskHandler
	// e.g., POST /tasks/task-123 -> updateTaskHandler
	
	path := strings.TrimPrefix(r.URL.Path, "/tasks/")
	
	if r.Method == http.MethodPost && path == "" {
		s.createTaskHandler(w, r)
		return
	}
	
	if path != "" {
		switch r.Method {
		case http.MethodGet:
			s.getTaskHandler(w, r, path)
		case http.MethodPost:
			s.updateTaskHandler(w, r, path)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	http.NotFound(w, r)
}

// createTaskHandler creates a new, pending task in etcd.
func (s *MCPState) createTaskHandler(w http.ResponseWriter, r *http.Request) {
	var task AgentTask
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	task.ID = fmt.Sprintf("task-%d", time.Now().UnixNano())
	task.Status = "pending"
	task.UpdatedAt = time.Now()
	
	taskJSON, _ := json.Marshal(task)
	etcdKey := fmt.Sprintf("mrm/agent_tasks/%s", task.ID)

	_, err := s.etcdClient.Put(context.Background(), etcdKey, string(taskJSON))
	if err != nil {
		s.logger.Error("Failed to create task in etcd", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	
	s.logger.Info("Successfully created new task in etcd", "task_id", task.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(task)
}


// updateTaskHandler allows an agent to save its work to etcd.
func (s *MCPState) updateTaskHandler(w http.ResponseWriter, r *http.Request, taskID string) {
	var task AgentTask
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	task.ID = taskID // Ensure the ID from the URL is used
	task.UpdatedAt = time.Now()
	taskJSON, _ := json.Marshal(task)
	
	etcdKey := fmt.Sprintf("mrm/agent_tasks/%s", taskID)
	_, err := s.etcdClient.Put(context.Background(), etcdKey, string(taskJSON))
	if err != nil {
		s.logger.Error("Failed to save task state to etcd", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	
	s.logger.Info("Successfully updated task state in etcd", "task_id", taskID, "status", task.Status)
	w.WriteHeader(http.StatusOK)
}

// getTaskHandler allows an agent to retrieve the work of another agent.
func (s *MCPState) getTaskHandler(w http.ResponseWriter, r *http.Request, taskID string) {
	etcdKey := fmt.Sprintf("mrm/agent_tasks/%s", taskID)

	resp, err := s.etcdClient.Get(context.Background(), etcdKey)
	if err != nil || len(resp.Kvs) == 0 {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	var task AgentTask
	json.Unmarshal(resp.Kvs[0].Value, &task)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Connect to the etcd cluster using its internal Kubernetes DNS name.
	// This assumes your main mrm-cell app is running.
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"http://mrm-cell-internal.default.svc.cluster.local:2379"},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		logger.Error("Failed to connect to etcd", "error", err)
		os.Exit(1)
	}
	defer etcdClient.Close()
	logger.Info("Successfully connected to etcd cluster.")

	state := &MCPState{etcdClient: etcdClient, logger: logger}

	// Register the new, stateful MCP endpoints.
	http.HandleFunc("/tasks/", state.tasksHandler)

	port := "8080"
	logger.Info("Starting MCP Server...", "port", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		logger.Error("Failed to start MCP server", "error", err)
		os.Exit(1)
	}
}