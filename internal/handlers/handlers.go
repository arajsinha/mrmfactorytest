package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// AppServer is the interface for our simple orchestrator.
type AppServer interface {
	OrchestrateAgentExecution(topic string) error
}

// SetupHandlers registers the API endpoints.
func SetupHandlers(server AppServer) {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	// The endpoint is now for submitting agent assignments.
	http.HandleFunc("/assignments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
			return
		}

		var reqBody struct {
			Topic string `json:"topic"`
		}

		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if reqBody.Topic == "" {
			http.Error(w, "Missing 'topic' in request body", http.StatusBadRequest)
			return
		}
		
		go server.OrchestrateAgentExecution(reqBody.Topic)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted new agent assignment for topic: %s\n", reqBody.Topic)
	})
}