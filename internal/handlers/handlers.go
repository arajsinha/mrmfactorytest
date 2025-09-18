package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// AppServer defines the interface our HTTP handlers need.
// This decouples the handlers from the main package.
type AppServer interface {
	OrchestrateExecution(configRepoURL string, command string) error
	RestartFSM(sid string, eid string)
}

// SetupHandlers now registers the correct /execute endpoint.
func SetupHandlers(server AppServer) {
	// Health check endpoint for Kubernetes readiness probes.
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	// --- THIS IS THE FIX ---
	// This is the new, primary endpoint for end-users to trigger their workflows.
	http.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

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
		go server.OrchestrateExecution(reqBody.ConfigRepo, reqBody.Command)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted execution request for repo: %s\n", reqBody.ConfigRepo)
	})
	// --- END OF FIX ---

	// This endpoint is for restarting a specific, failed execution.
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