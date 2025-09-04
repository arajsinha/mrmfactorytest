package handlers

import (
	"fmt"
	"net/http"
)

// AppServer defines the interface our HTTP handlers need.
// This decouples the handlers from the main package.
type AppServer interface {
	ExecuteFSM(command string)
	RestartFSM(sid string, eid string)
}

// SetupHandlers registers handlers that call methods on our long-lived server object.
func SetupHandlers(server AppServer) {
	// --- THIS IS THE FIX ---
	// Add a simple health check endpoint.
	// Kubernetes will call this to determine if the container is ready.
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})
	// --- END OF FIX ---
	http.HandleFunc("/executeFSM", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		command := r.URL.Query().Get("command")
		if command == "" {
			http.Error(w, "Missing 'command' query parameter", http.StatusBadRequest)
			return
		}

		// Run the execution in the background so the API call returns immediately.
		go server.ExecuteFSM(command)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted execution request for command: %s\n", command)
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

		// Run the restart in the background.
		go server.RestartFSM(sid, eid)

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "Accepted restart request for EID: %s\n", eid)
	})
}