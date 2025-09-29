package main

import (
	"context"
	// "encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mrm_cell/internal/cluster"
	"mrm_cell/internal/config"
	"mrm_cell/internal/crypto"
	"mrm_cell/internal/etcd"
	"mrm_cell/internal/fsm"
	"mrm_cell/internal/handlers"
	"mrm_cell/internal/secrets"
	"mrm_cell/internal/store"
	"mrm_cell/internal/taskrunner"
	"mrm_cell/internal/telemetry"

	clientv3 "go.etcd.io/etcd/client/v3"
	embed "go.etcd.io/etcd/server/v3/embed"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var etcdServer *embed.Etcd
const scenarioID = "S001"

// Server holds the shared components for the orchestrator service.
type Server struct {
	etcdClient *clientv3.Client
	runner     *taskrunner.Runner
	logger     *slog.Logger
	kubeClient *kubernetes.Clientset
	configFile string
	pluginsDir string
}

func NewServer(etcdClient *clientv3.Client, runner *taskrunner.Runner, logger *slog.Logger, kubeClient *kubernetes.Clientset, configFile, pluginsDir string) *Server {
	return &Server{
		etcdClient: etcdClient,
		runner:     runner,
		logger:     logger,
		kubeClient: kubeClient,
		configFile: configFile,
		pluginsDir: pluginsDir,
	}
}

func main() {
	config := parseFlags()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	// The application now has two distinct modes based on the --command flag.
	if config.Command != "" {
		// MODE 1: EXECUTION ENGINE (Running inside a Kubernetes Job)
		logger.Info("Starting in ONE-SHOT EXECUTION ENGINE mode.")
		
		client, err := clientv3.New(clientv3.Config{Endpoints: []string{"http://mrm-cell-0.mrm-cell-internal.default.svc.cluster.local:2379"}, DialTimeout: 15 * time.Second})
		if err != nil {
			logger.Error("FATAL: Job failed to connect to etcd", "error", err); os.Exit(1)
		}
		defer client.Close()
		
		runner := taskrunner.NewRunner(logger, 10, config.Command, config.PluginsDir)
		server := NewServer(client, runner, logger, nil, config.ConfigFile, config.PluginsDir)
		server.ExecuteFSM(config.Command)
		logger.Info("One-shot job execution finished.")

	} else {
		// MODE 2: ORCHESTRATOR SERVICE (Running as a long-lived StatefulSet)
		logger.Info("Starting in long-running ORCHESTRATOR SERVICE mode.")
		
		cfg, _ := telemetry.LoadConfig("telemetry.yaml")
		if cfg != nil {
			shutdown, err := telemetry.Initialize(ctx, cfg)
			if err != nil { logger.Error("Failed to initialize telemetry", "error", err) } else { defer shutdown(ctx) }
		}
		
		appServer := &Server{logger: logger}
		startHTTPServer(config.HTTPPort, appServer)

		etcdConfig, err := initializeEtcd(config); if err != nil { logger.Error("FATAL: Failed to initialize etcd", "error", err); os.Exit(1) }; defer closeEtcdServer()
		
		client, err := clientv3.New(clientv3.Config{ Endpoints: []string{fmt.Sprintf("http://%s:%d", etcdConfig.HostID, etcdConfig.ClientPort)}, DialTimeout: 5 * time.Second });
		if err != nil { logger.Error("FATAL: Failed to create shared etcd client", "error", err); os.Exit(1) }; defer client.Close()
		
		kubeConfig, err := rest.InClusterConfig(); if err != nil { logger.Error("FATAL: Failed to get in-cluster Kubernetes config", "error", err); os.Exit(1) }
		kubeClient, err := kubernetes.NewForConfig(kubeConfig); if err != nil { logger.Error("FATAL: Failed to create Kubernetes client", "error", err); os.Exit(1) }

		appServer.etcdClient = client
		appServer.runner = taskrunner.NewRunner(logger, 10, config.Command, config.PluginsDir)
		appServer.kubeClient = kubeClient
		appServer.configFile = config.ConfigFile
		appServer.pluginsDir = config.PluginsDir
		
		clusterMonitor := cluster.NewMonitor(client, logger, config.NodeID)
		clusterMonitor.Start(context.Background())
		
		logger.Info("Orchestrator service fully initialized and ready.")
		waitForShutdown()
	}
}

// OrchestrateExecutionFromRepo is the original function that clones a Git repo.
func (s *Server) OrchestrateExecutionFromRepo(configRepo string, command string) error {
	s.logger.Info("Received new orchestration request from Git repo", "repo", configRepo, "command", command)
	jobName := fmt.Sprintf("mrm-exec-%d", time.Now().UnixNano())

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{ Name: jobName, Namespace: "default" },
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "mrm-execution-job"},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
					InitContainers: []corev1.Container{
						{
							Name:  "git-cloner",
							Image: "alpine/git",
							Command: []string{"sh", "-c", fmt.Sprintf("git clone https://$(username):$(token)@%s /workspace", strings.TrimPrefix(configRepo, "https://"))},
							EnvFrom: []corev1.EnvFromSource{
								{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "github-credentials"}}},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "workspace", MountPath: "/workspace"},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "mrm-cell-engine",
							Image: "arajsinha/mrm-cell-factory:latest",
							Command: []string{ "./mrm-cell", "--config-file=/workspace/fsm-config.yaml", "--plugins-dir=/app/plugins", fmt.Sprintf("--command=%s", command), },
							VolumeMounts: []corev1.VolumeMount{ {Name: "workspace", MountPath: "/workspace"}, },
							EnvFrom: []corev1.EnvFromSource{ {SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "mrm-cell-secrets"}}}, },
						},
					},
					RestartPolicy: "Never",
				},
			},
			BackoffLimit: &[]int32{0}[0],
		},
	}

	s.logger.Info("Creating new Kubernetes Job for execution from Git repo", "jobName", jobName)
	_, err := s.kubeClient.BatchV1().Jobs("default").Create(context.TODO(), job, metav1.CreateOptions{})
	if err != nil {
		s.logger.Error("Failed to create Kubernetes Job", "error", err)
		return err
	}
	s.logger.Info("Successfully launched new execution job", "jobName", jobName)
	return nil
}


// OrchestrateExecutionFromConfigMap is the new function that mounts a ConfigMap.
func (s *Server) OrchestrateExecutionFromConfigMap(configMapName string, command string) error {
	s.logger.Info("Received new orchestration request from ConfigMap", "configMap", configMapName, "command", command)
	jobName := fmt.Sprintf("mrm-exec-%d", time.Now().UnixNano())

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{ Name: jobName, Namespace: "default" },
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "config-volume",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "mrm-cell-engine",
							Image: "arajsinha/mrm-cell-factory:latest",
							Command: []string{
								"./mrm-cell",
								"--config-file=/etc/config/fsm-config.yaml",
								"--plugins-dir=/app/plugins",
								fmt.Sprintf("--command=%s", command),
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config-volume", MountPath: "/etc/config/fsm-config.yaml", SubPath: "fsm-config.yaml"},
							},
							EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "mrm-cell-secrets"}}}},
						},
					},
					RestartPolicy: "Never",
				},
			},
			BackoffLimit: &[]int32{0}[0],
		},
	}

	s.logger.Info("Creating new Kubernetes Job for execution from ConfigMap", "jobName", jobName)
	_, err := s.kubeClient.BatchV1().Jobs("default").Create(context.TODO(), job, metav1.CreateOptions{})
	if err != nil {
		s.logger.Error("Failed to create Kubernetes Job", "error", err)
		return err
	}
	s.logger.Info("Successfully launched new execution job", "jobName", jobName)
	return nil
}

// ExecuteFSM is the method used by the one-shot Job.
func (s *Server) ExecuteFSM(command string) {
	ctx := context.Background()
	s.runner.SetCommand(command)
	
	// --- THIS IS THE FIX (Part 1) ---
	// The NewExecutionStore call now correctly passes an empty EID,
	// as the EID will be generated by the FSM.
	execStore := store.NewExecutionStore(s.etcdClient, s.logger, scenarioID, "")
	
	cfg, err := bootstrapFSMConfig(ctx, s.logger, execStore, s.configFile)
	if err != nil {
		s.logger.Error("Could not bootstrap FSM configuration.", "error", err)
		return
	}

	fsmCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	fsmMachine := fsm.NewMachine(fsmCtx, cfg, s.runner, s.logger, execStore)
	fsmMachine.Run(fsmCtx)
}

// RestartFSM is the method that handles restarting a failed execution.
func (s *Server) RestartFSM(sid string, eid string) {
	logger := s.logger.With("sid", sid, "eid", eid)
	logger.Info("Starting restart process for execution.")
	ctx := context.Background()
	
	// --- THIS IS THE FIX (Part 2) ---
	// The NewExecutionStore call is corrected here as well.
	execStore := store.NewExecutionStore(s.etcdClient, logger, sid, eid)

	fsmConfig, err := bootstrapFSMConfig(ctx, logger, execStore, s.configFile)
	if err != nil {
		logger.Error("Restart failed: could not get FSM config.", "error", err)
		return
	}

	header, err := execStore.GetHeader(ctx)
	if err != nil {
		logger.Error("Restart failed: could not get execution header.", "error", err)
		return
	}
	if header.Status == store.StatusCompleted {
		logger.Warn("Execution is already completed. Nothing to restart.")
		return
	}
	
	allTasks, err := execStore.GetAllTasks(ctx)
	if err != nil {
		logger.Error("Restart failed: could not get task details.", "error", err)
		return
	}

	logger.Info("Resetting non-completed tasks to pending status.")
	for i, task := range allTasks {
		if task.Status != store.StatusCompleted {
			allTasks[i].Status = store.StatusPending; allTasks[i].StartTime = nil; allTasks[i].EndTime = nil; allTasks[i].Result = nil; allTasks[i].Error = ""
		}
	}
	
	header.Status = store.StatusInProgress; header.Error = ""; now := time.Now(); header.StartTime = now; header.EndTime = nil

	if err := execStore.UpdateHeader(ctx, *header); err != nil {
		logger.Error("Restart failed: could not update header.", "error", err)
		return
	}
	for _, task := range allTasks {
		if err := execStore.UpdateTask(ctx, task); err != nil {
			logger.Error("Restart failed: could not update task.", "taskID", task.TaskID, "error", err)
			return
		}
		go execStore.UpdateTaskInDetails(ctx, task)
	}

	logger.Info("State has been reset in etcd. Triggering task runner to resume execution.")
	var tasksToRun []config.Task
	foundTasks := false
	for _, action := range fsmConfig.FSM.Behavior.Actions {
		if action.State == header.TargetState {
			tasksToRun = action.Tasks
			foundTasks = true
			break
		}
	}
	if !foundTasks {
		logger.Error("Restart failed: could not find tasks for target state in the current config.", "state", header.TargetState)
		return
	}
	
	s.runner.ExecuteActions(ctx, tasksToRun, execStore)
	logger.Info("Restart execution has been handed off to the runner.")
}

// --- Helper Functions ---
func startHTTPServer(httpPort int, server *Server) {
	handlers.SetupHandlers(server)
	go func() {
		slog.Info("Starting HTTP server on port", "port", httpPort)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", httpPort), nil); err != nil {
			slog.Error("HTTP server failed", "error", err)
		}
	}()
}

func bootstrapFSMConfig(ctx context.Context, logger *slog.Logger, s *store.ExecutionStore, configPath string) (*config.Config, error) {
	cfg, err := s.GetFSMConfig(ctx)
	if err == nil { return cfg, nil }
	if !errors.Is(err, store.ErrConfigNotFound) { return nil, fmt.Errorf("failed to check for FSM config in etcd: %w", err) }
	logger.Info("No FSM config in etcd. Bootstrapping from local file.", "path", configPath)
	yamlData, err := os.ReadFile(configPath)
	if err != nil { return nil, fmt.Errorf("could not read local config file %s: %w", configPath, err) }
	if err := s.WriteFSMConfig(ctx, yamlData); err != nil { return nil, fmt.Errorf("failed to write bootstrap config to etcd: %w", err) }
	return config.Load(configPath)
}

// syncSecrets is no longer used in the primary workflow but kept for potential admin use.
func syncSecrets(ctx context.Context, logger *slog.Logger, sm *secrets.SecretsManager, cs *crypto.CryptoService, etcdClient *clientv3.Client, configPath string) error {
	logger.Info("Secrets synchronization is now handled within the task runner.")
	return nil
}

type Config struct {
	NodeID, HostID, DataDir, CloudProvider, InitialCluster, ClusterState, Command, ConfigFile, PluginsDir string
	ClientPort, PeerPort, HTTPPort int
	CAFile, ServerCertFile, ServerKeyFile, PeerCertFile, PeerKeyFile, ClientCertFile, ClientKeyFile string
}

func parseFlags() *Config {
	config := &Config{}
	flag.StringVar(&config.NodeID, "node-id", "node1", "Node ID")
	flag.StringVar(&config.HostID, "host-id", "localhost", "Host ID")
	flag.StringVar(&config.DataDir, "data-dir", "data.etcd", "etcd data directory")
	flag.IntVar(&config.ClientPort, "client-port", 2379, "etcd client port")
	flag.IntVar(&config.PeerPort, "peer-port", 2380, "etcd peer port")
	flag.StringVar(&config.CloudProvider, "cloud-provider", "", "Cloud provider")
	flag.IntVar(&config.HTTPPort, "http-port", 8080, "HTTP port")
	flag.StringVar(&config.InitialCluster, "initial-cluster", "", "Initial cluster configuration")
	flag.StringVar(&config.ClusterState, "cluster-state", "new", "Initial cluster state")
	flag.StringVar(&config.CAFile, "etcd-ca-file", "", "Path to etcd CA certificate file")
	flag.StringVar(&config.ServerCertFile, "etcd-server-cert-file", "", "Path to etcd server certificate file")
	flag.StringVar(&config.ServerKeyFile, "etcd-server-key-file", "", "Path to etcd server key file")
	flag.StringVar(&config.PeerCertFile, "etcd-peer-cert-file", "", "Path to etcd peer certificate file")
	flag.StringVar(&config.PeerKeyFile, "etcd-peer-key-file", "", "Path to etcd peer key file")
	flag.StringVar(&config.ClientCertFile, "etcd-client-cert-file", "", "Path to etcd client certificate file")
	flag.StringVar(&config.ClientKeyFile, "etcd-client-key-file", "", "Path to etcd client key file")
	flag.StringVar(&config.ConfigFile, "config-file", "fsm-config.yaml", "Path to the FSM configuration file.")
	flag.StringVar(&config.PluginsDir, "plugins-dir", "plugins", "Path to the directory containing .so plugins.")
	flag.StringVar(&config.Command, "command", "", "FSM event/command to execute (for jobs)")
	flag.Parse()
	return config
}

func closeEtcdServer() {
	if etcdServer != nil {
		etcdServer.Close()
	}
}

func waitForShutdown() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	<-sigc
	slog.Info("Shutting down...")
}

func initializeEtcd(config *Config) (*etcd.Config, error) {
	etcdConfig := etcd.NewDefaultConfig()
	etcdConfig.NodeID = config.NodeID
	etcdConfig.HostID = config.HostID
	etcdConfig.DataDir = config.DataDir
	etcdConfig.InitialCluster = config.InitialCluster
	etcdConfig.ClusterState = config.ClusterState
	etcdConfig.ClientPort = config.ClientPort
	etcdConfig.PeerPort = config.PeerPort
	etcdConfig.TLS = etcd.TLSConfig{
		CAFile:         config.CAFile,
		ServerCertFile: config.ServerCertFile,
		ServerKeyFile:  config.ServerKeyFile,
		PeerCertFile:   config.PeerCertFile,
		PeerKeyFile:    config.PeerKeyFile,
		ClientCertFile: config.ClientCertFile,
		ClientKeyFile:  config.ClientKeyFile,
	}
	var err error
	etcdServer, err = etcd.StartNode(etcdConfig)
	if err != nil {
		slog.Error("Failed to start etcd node", "error", err)
		return nil, fmt.Errorf("failed to start etcd node: %w", err)
	}
	return etcdConfig, nil
}