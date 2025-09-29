package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// AppServer defines the interface our HTTP handlers need.
// It now supports both execution methods for maximum flexibility.
type AppServer interface {
	OrchestrateExecutionFromRepo(configRepo string, command string) error
	OrchestrateExecutionFromConfigMap(configMapName string, command string) error
	RestartFSM(sid string, eid string)
}

// SetupHandlers receives the central Server object and registers all the API endpoints.
func SetupHandlers(server AppServer) {
	// This is a critical endpoint for Kubernetes/Kyma. The readiness probe
	// calls this to verify that the application's HTTP server is running.
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	// This is the main endpoint for end-users to trigger their workflows.
	// It now intelligently handles both ConfigMap and Git repo requests.
	http.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
			return
		}

		// Define a struct to parse the incoming JSON request body.
		// The 'omitempty' tag means the fields are optional.
		var reqBody struct {
			Command       string `json:"command"`
			ConfigRepo    string `json:"configRepo,omitempty"`
			ConfigMapName string `json:"configMapName,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if reqBody.Command == "" {
			http.Error(w, "Missing 'command' in request body", http.StatusBadRequest)
			return
		}

		// --- THIS IS THE "FORK IN THE ROAD" LOGIC ---
		if reqBody.ConfigMapName != "" {
			// If a ConfigMap name is provided, use the new, direct method for testing.
			go server.OrchestrateExecutionFromConfigMap(reqBody.ConfigMapName, reqBody.Command)
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, "Accepted execution request from ConfigMap: %s\n", reqBody.ConfigMapName)
		} else if reqBody.ConfigRepo != "" {
			// Otherwise, use the original Git repository method for the full workflow.
			go server.OrchestrateExecutionFromRepo(reqBody.ConfigRepo, reqBody.Command)
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, "Accepted execution request from repository: %s\n", reqBody.ConfigRepo)
		} else {
			// If neither is provided, return an error.
			http.Error(w, "Request body must contain either 'configRepo' or 'configMapName'", http.StatusBadRequest)
		}
	})
	
	// This endpoint is for restarting a specific, failed execution by its ID.
	http.HandleFunc("/scenario/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sid := r.URL.Query().Get("sid")
		eid := r.URL.Query().Get("eid")
		if sid == "" || eid == "" {
			http.Error(w, "Missing 'sid' or 'eid' query parameter", http.StatusBadRequest)
			return
		}

		go server.RestartFSM(sid, eid)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted restart request for EID: %s\n", eid)
	})
}