package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// AppServer defines the interface that our HTTP handlers need to interact with.
// This decouples the handlers from the concrete main.Server struct, which is a
// best practice that prevents circular import errors.
type AppServer interface {
	// OrchestrateExecution is the new primary method for user-driven workflows.
	OrchestrateExecution(configRepoURL string, command string) error
	
	// RestartFSM is for retrying a specific, previously failed execution.
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

	// This is the new, primary endpoint for end-users to trigger their workflows.
	// It expects a JSON body specifying the command and the user's config repository.
	http.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Define a struct to parse the incoming JSON request body.
		var reqBody struct {
			Command    string `json:"command"`
			ConfigRepo string `json:"configRepo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if reqBody.Command == "" || reqBody.ConfigRepo == "" {
			http.Error(w, "Missing 'command' or 'configRepo' in request body", http.StatusBadRequest)
			return
		}
		
		// Run the orchestration in the background so the API call returns immediately.
		// This is a non-blocking "fire-and-forget" pattern.
		go server.OrchestrateExecution(reqBody.ConfigRepo, reqBody.Command)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted execution request for repo: %s\n", reqBody.ConfigRepo)
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

		// Run the restart in the background.
		go server.RestartFSM(sid, eid)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted restart request for EID: %s\n", eid)
	})
}