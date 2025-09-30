package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// AppServer defines the interface our HTTP handlers need.
// This decouples the handlers from the main package for clean architecture.
type AppServer interface {
	OrchestrateExecutionFromConfigMap(configMapName string, command string) error
	RestartFSM(sid string, eid string)
}

// SetupHandlers now correctly accepts the AppServer interface.
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
			Command       string `json:"command"`
			ConfigMapName string `json:"configMapName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if reqBody.Command == "" || reqBody.ConfigMapName == "" {
			http.Error(w, "Missing 'command' or 'configMapName' in request body", http.StatusBadRequest)
			return
		}
		
		go server.OrchestrateExecutionFromConfigMap(reqBody.ConfigMapName, reqBody.Command)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted execution request for config: %s\n", reqBody.ConfigMapName)
	})
	
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