package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	// We only need the handlers package for the simple health check
	"mrm_cell/internal/handlers"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Server now only needs the Kubernetes client and a logger.
type Server struct {
	kubeClient *kubernetes.Clientset
	logger     *slog.Logger
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Initialize Kubernetes Client
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Error("FATAL: Failed to get in-cluster Kubernetes config", "error", err)
		os.Exit(1)
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		logger.Error("FATAL: Failed to create Kubernetes client", "error", err)
		os.Exit(1)
	}

	// Create the main server instance
	appServer := &Server{
		kubeClient: kubeClient,
		logger:     logger,
	}

	// Setup and start the HTTP server
	handlers.SetupHandlers(appServer)
	httpPort := flag.Int("http-port", 8080, "HTTP port")
	flag.Parse()

	logger.Info("Starting orchestrator server", "port", *httpPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *httpPort), nil); err != nil {
		logger.Error("FATAL: HTTP server failed", "error", err)
		os.Exit(1)
	}
}

// This is now the only core method of the orchestrator.
func (s *Server) OrchestrateExecution(configMapName string) error {
	s.logger.Info("Received new orchestration request", "configMap", configMapName)
	jobName := fmt.Sprintf("agent-exec-%d", time.Now().UnixNano())

	// This job will now run your CrewAI python script
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{ Name: jobName, Namespace: "default" },
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "crew-ai-agent",
							Image: "your-docker-repo/crewai-researcher:latest", // The new CrewAI image
							Args:  []string{"The future of quantum computing"},    // Example topic
							// Inject API keys for the AI agent (e.g., OpenAI, Serper)
							EnvFrom: []corev1.EnvFromSource{
								{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "ai-api-keys"}}},
							},
						},
					},
					RestartPolicy: "Never",
				},
			},
			BackoffLimit: &[]int32{0}[0],
		},
	}

	s.logger.Info("Creating new Kubernetes Job for agent execution", "jobName", jobName)
	_, err := s.kubeClient.BatchV1().Jobs("default").Create(context.TODO(), job, metav1.CreateOptions{})
	if err != nil {
		s.logger.Error("Failed to create Kubernetes Job", "error", err)
		return err
	}
	s.logger.Info("Successfully launched new agent execution job", "jobName", jobName)
	return nil
}