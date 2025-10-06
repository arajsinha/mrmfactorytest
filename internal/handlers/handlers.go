package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// AppServer is the interface for our simple orchestrator.
type AppServer interface {
	OrchestrateExecution(configMapName string) error
}

// SetupHandlers registers the API endpoints.
func SetupHandlers(server AppServer) {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	http.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
			return
		}

		var reqBody struct {
			ConfigMapName string `json:"configMapName"`
		}

		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if reqBody.ConfigMapName == "" {
			http.Error(w, "Missing 'configMapName' in request body", http.StatusBadRequest)
			return
		}
		
		go server.OrchestrateExecution(reqBody.ConfigMapName)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted execution request for config: %s\n", reqBody.ConfigMapName)
	})
}