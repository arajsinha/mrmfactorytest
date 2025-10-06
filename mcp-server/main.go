package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// MCPTool represents a tool that can be exposed to an AI agent.
type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// We would define input/output schemas here in a real implementation
}

// MCPState holds the shared components for our MCP server.
type MCPState struct {
	etcdClient *clientv3.Client
	logger     *slog.Logger
	tools      []MCPTool
}

// listToolsHandler tells the AI agent what tools are available.
func (s *MCPState) listToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("MCP request received for list_tools")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.tools)
}

// callToolHandler is where the AI agent asks the server to execute a tool.
func (s *MCPState) callToolHandler(w http.ResponseWriter, r *http.Request) {
	// In a real implementation, we would parse the request, find the
	// correct tool (e.g., "break_down_topic"), execute it by talking
	// to etcd, and return the result in the MCP format.
	s.logger.Info("MCP request received for call_tool")
	fmt.Fprintf(w, "Tool call received and is being processed.")
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Connect to the etcd cluster using its internal Kubernetes DNS name.
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"http://mrm-cell-0.mrm-cell-internal.default.svc.cluster.local:2379"},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		logger.Error("Failed to connect to etcd", "error", err)
		os.Exit(1)
	}
	defer etcdClient.Close()
	logger.Info("Successfully connected to etcd cluster.")

	// Define the tools that our MCP server will offer to the agents.
	state := &MCPState{
		etcdClient: etcdClient,
		logger:     logger,
		tools: []MCPTool{
			{Name: "break_down_topic", Description: "Analyzes a high-level research topic and breaks it down into smaller, specific sub-topics."},
			{Name: "save_research_notes", Description: "Saves a block of text containing research findings to a specific sub-topic task in etcd."},
			{Name: "get_all_research_for_topic", Description: "Retrieves all saved research notes for a completed topic."},
			{Name: "write_final_report", Description: "Synthesizes all research notes into a final report and saves it to etcd."},
		},
	}

	// Register the MCP endpoints.
	http.HandleFunc("/tools/list", state.listToolsHandler)
	http.HandleFunc("/tools/call", state.callToolHandler)

	port := "8080"
	logger.Info("Starting MCP Server...", "port", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		logger.Error("Failed to start MCP server", "error", err)
		os.Exit(1)
	}
}