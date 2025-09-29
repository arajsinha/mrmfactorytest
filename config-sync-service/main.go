package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GiteaWebhookPayload defines the structure of the incoming webhook from Gitea.
type GiteaWebhookPayload struct {
	Repository struct {
		Name     string `json:"name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// handleWebhook is the main function that processes notifications from Gitea.
func handleWebhook(w http.ResponseWriter, r *http.Request) {
	log.Println("Received a webhook from Gitea...")

	// 1. Parse the incoming webhook payload to get the repository details.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}

	var payload GiteaWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("Error unmarshaling webhook payload: %v", err)
		http.Error(w, "Error unmarshaling webhook payload", http.StatusBadRequest)
		return
	}

	repoName := payload.Repository.Name
	cloneURL := payload.Repository.CloneURL
	if repoName == "" || cloneURL == "" {
		log.Println("Webhook payload is missing repository name or clone URL.")
		http.Error(w, "Invalid webhook payload", http.StatusBadRequest)
		return
	}

	// --- THIS IS THE DEFINITIVE FIX ---
	// 2. Read the Gitea bot's credentials from environment variables.
	//    These are securely injected from the 'gitea-bot-credentials' Kubernetes secret.
	botUser := os.Getenv("GITEA_BOT_USER")
	botToken := os.Getenv("GITEA_BOT_TOKEN")
	if botUser == "" || botToken == "" {
		log.Println("FATAL: Gitea bot credentials (GITEA_BOT_USER, GITEA_BOT_TOKEN) are not set in the environment.")
		http.Error(w, "Internal server configuration error", http.StatusInternalServerError)
		return
	}

	// 3. Construct the authenticated, internal clone URL.
	//    a. Replace the public hostname with the internal Kubernetes service DNS name for Gitea.
	internalBaseURL := strings.Replace(cloneURL, "http://gitea.c9ff5e0.kyma.ondemand.com", "", 1)
	//    b. Assemble the final URL with credentials for non-interactive cloning.
	authenticatedCloneURL := fmt.Sprintf("http://%s:%s@gitea-http.gitea.svc.cluster.local:3000%s", botUser, botToken, internalBaseURL)
	// --- END OF FIX ---

	// 4. Create a temporary directory and clone the user's repository.
	tmpDir, err := os.MkdirTemp("", "config-sync-")
	if err != nil {
		log.Printf("Error creating temp directory: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	log.Printf("Cloning repository '%s' from authenticated internal URL into '%s'", repoName, tmpDir)
	cmdClone := exec.Command("git", "clone", authenticatedCloneURL, tmpDir)
	if output, err := cmdClone.CombinedOutput(); err != nil {
		log.Printf("Error cloning repository: %v\nOutput: %s", err, string(output))
		http.Error(w, "Error cloning repository", http.StatusInternalServerError) // Respond with 500
		return
	}

	// 5. Create or update the Kubernetes ConfigMap from the cloned file.
	configFilePath := filepath.Join(tmpDir, "fsm-config.yaml")
	configMapName := fmt.Sprintf("%s-config", repoName)

	log.Printf("Creating/updating ConfigMap '%s' from file '%s'", configMapName, configFilePath)

	// This robust command creates the ConfigMap if it doesn't exist, or updates it if it does,
	// and ensures it's created in the correct 'gitea' namespace.
	cmdKubectl := exec.Command("sh", "-c",
		fmt.Sprintf("kubectl create configmap %s --from-file=%s --dry-run=client -o yaml | kubectl apply -f - -n gitea", configMapName, configFilePath),
	)
	if output, err := cmdKubectl.CombinedOutput(); err != nil {
		log.Printf("Error applying ConfigMap: %v\nOutput: %s", err, string(output))
		http.Error(w, "Error applying ConfigMap", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully synced configuration and created/updated ConfigMap '%s'", configMapName)
	fmt.Fprintf(w, "Webhook processed successfully for repository %s", repoName)
}

func main() {
	http.HandleFunc("/webhook", handleWebhook)
	log.Println("Starting Config Sync Service on port 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
