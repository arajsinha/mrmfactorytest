package taskrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mrm_cell/internal/config"
	"mrm_cell/internal/crypto"
	"mrm_cell/internal/store"
	"os"
	"path/filepath"
	"plugin"
	"strings"
	"sync"
	"time"
)

// DispatchableTask is a struct to send a task along with its assigned batch number to a worker.
type DispatchableTask struct {
	Config    config.Task
	Iteration int
}

// Runner now holds the path to the plugins directory.
type Runner struct {
	logger     *slog.Logger
	numWorkers int
	command    string
	pluginsDir string
}

// NewRunner is updated to accept the plugins directory path.
func NewRunner(logger *slog.Logger, numWorkers int, command string, pluginsDir string) *Runner {
	return &Runner{
		logger:     logger,
		numWorkers: numWorkers,
		command:    command,
		pluginsDir: pluginsDir,
	}
}

func (r *Runner) GetCommand() string {
	return r.command
}

// SetCommand updates the command for a new execution.
func (r *Runner) SetCommand(command string) {
	r.command = command
}

// ExecuteActions runs the main task execution logic driven by the state in etcd.
func (r *Runner) ExecuteActions(ctx context.Context, tasks []config.Task, execStore *store.ExecutionStore) (bool, error) {
	startTime := time.Now()
	r.logger.Info("Executing actions for state, driven by etcd", "task_count", len(tasks))

	if err := detectDependencyCycle(tasks); err != nil {
		r.logger.Error("Invalid task configuration: Dependency cycle detected.", "error", err)
		return false, err
	}
	if len(tasks) == 0 {
		return true, nil
	}

	tasksToRunChan := make(chan DispatchableTask, len(tasks))
	workerDoneSignal := make(chan bool, len(tasks))
	allFinishedChan := make(chan bool, 1)
	var workerGroup sync.WaitGroup

	r.logger.Info("Starting worker pool", "worker_count", r.numWorkers)
	workerGroup.Add(r.numWorkers)
	for i := 0; i < r.numWorkers; i++ {
		go r.worker(ctx, &workerGroup, tasksToRunChan, workerDoneSignal, execStore)
	}
	go r.dispatcher(ctx, tasks, tasksToRunChan, workerDoneSignal, allFinishedChan, execStore)

	select {
	case <-allFinishedChan:
		r.logger.Info("Dispatcher signaled all tasks are complete.")
	case <-ctx.Done():
		r.logger.Warn("Execution cancelled by context during run.")
	}

	close(tasksToRunChan)
	workerGroup.Wait()
	r.logger.Info("All workers have shut down.")

	finalTasks, err := execStore.GetAllTasks(ctx)
	if err != nil {
		r.logger.Error("Failed to get final task statuses from store", "error", err)
		return false, err
	}

	finalSuccess := true
	for _, task := range finalTasks {
		if task.Status == store.StatusFailed || task.Status == store.StatusBlocked {
			finalSuccess = false
			break
		}
	}

	r.logger.Info("Updating final execution header status.", "success", finalSuccess)
	header, err := execStore.GetHeader(ctx)
	if err != nil {
		r.logger.Error("Could not get execution header for final update", "error", err)
	} else {
		if finalSuccess {
			header.Status = store.StatusCompleted
		} else {
			header.Status = store.StatusFailed
			header.Error = "One or more tasks failed or were blocked."
		}
		endTime := time.Now()
		header.EndTime = &endTime
		if err := execStore.UpdateHeader(ctx, *header); err != nil {
			r.logger.Error("Failed to write final execution header update to etcd", "error", err)
		}
	}

	r.logger.Info("Action execution finished", "overall_success", finalSuccess)
	totalTime := time.Since(startTime)
	r.logger.Info("Total action execution time", "total_duration", totalTime.Round(time.Millisecond))

	return finalSuccess, nil
}

// dispatcher handles scheduling tasks based on etcd state.
func (r *Runner) dispatcher(
	ctx context.Context,
	allConfigTasks []config.Task,
	tasksToRunChan chan<- DispatchableTask,
	workerDoneSignal <-chan bool,
	allFinishedChan chan<- bool,
	execStore *store.ExecutionStore,
) {
	taskConfigMap := make(map[string]config.Task)
	for _, t := range allConfigTasks {
		taskConfigMap[t.ID] = t
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	scanAndDispatch := func() bool {
		tasksFromStore, err := execStore.GetAllTasks(ctx)
		if err != nil {
			r.logger.Error("Dispatcher failed to get tasks from store", "error", err)
			return true
		}

		taskStatusMap := make(map[string]store.ExecutionStatus)
		finishedCount := 0
		for _, t := range tasksFromStore {
			taskStatusMap[t.TaskID] = t.Status
			if t.Status != store.StatusPending && t.Status != store.StatusInProgress {
				finishedCount++
			}
		}

		if finishedCount >= len(allConfigTasks) {
			r.logger.Info("Dispatcher determined all tasks are in a terminal state.")
			allFinishedChan <- true
			return false
		}

		var readyTasks []config.Task
		for _, taskDetail := range tasksFromStore {
			if taskDetail.Status != store.StatusPending {
				continue
			}

			taskConfig := taskConfigMap[taskDetail.TaskID]
			depsMet, depFailed := true, false
			if taskConfig.Dependencies != nil {
				for _, depID := range taskConfig.Dependencies.Tasks {
					depStatus, found := taskStatusMap[depID]
					if !found || (depStatus != store.StatusCompleted && depStatus != store.StatusSkipped) {
						depsMet = false
					}
					if depStatus == store.StatusFailed || depStatus == store.StatusBlocked {
						depFailed = true
						break
					}
				}
			}

			if depFailed {
				logger := r.logger.With("task_id", taskDetail.TaskID)
				logger.Warn("Blocking task due to failed dependency.")
				taskDetail.Status = store.StatusBlocked
				taskDetail.Error = "An upstream dependency failed."
				go execStore.UpdateTask(ctx, taskDetail)
				go execStore.UpdateTaskInDetails(ctx, taskDetail)
				continue
			}

			if depsMet {
				readyTasks = append(readyTasks, taskConfig)
			}
		}

		if len(readyTasks) > 0 {
			newBatchNum, err := execStore.IncrementBatchCounter(ctx)
			if err != nil {
				r.logger.Error("Dispatcher failed to increment batch counter", "error", err)
				return true
			}
			r.logger.Info("Dispatching new batch of tasks", "batch_number", newBatchNum, "task_count", len(readyTasks))
			for _, task := range readyTasks {
				dispatchable := DispatchableTask{Config: task, Iteration: newBatchNum}
				select {
				case tasksToRunChan <- dispatchable:
				default:
					r.logger.Warn("Could not dispatch task immediately, channel is full.", "task_id", task.ID)
				}
			}
		}
		return true
	}

	if !scanAndDispatch() {
		return
	}

	for {
		select {
		case <-workerDoneSignal:
			if !scanAndDispatch() {
				return
			}
		case <-ticker.C:
			if !scanAndDispatch() {
				return
			}
		case <-ctx.Done():
			r.logger.Info("Dispatcher shutting down due to context cancellation.")
			return
		}
	}
}

// worker handles the execution of a single task, including secret resolution and plugin loading.
func (r *Runner) worker(
	ctx context.Context,
	wg *sync.WaitGroup,
	tasksToRunChan <-chan DispatchableTask,
	workerDoneSignal chan<- bool,
	execStore *store.ExecutionStore,
) {
	encryptionKey := os.Getenv("ENCRYPTION_KEY")
	cryptoService, err := crypto.NewService(encryptionKey)
	if err != nil {
		r.logger.Error("Worker failed to initialize crypto service, cannot run tasks", "error", err)
		return
	}

	defer wg.Done()
	for {
		select {
		case dispatchable, ok := <-tasksToRunChan:
			if !ok {
				return
			}
			task := dispatchable.Config
			iteration := dispatchable.Iteration
			logger := r.logger.With("task_id", task.ID, "iteration", iteration)
			taskDetail, err := execStore.GetTask(ctx, task.ID)
			if err != nil {
				logger.Error("Could not get initial task details from store", "error", err)
				continue
			}

			now := time.Now()
			taskDetail.Status = store.StatusInProgress
			taskDetail.StartTime = &now
			taskDetail.Iteration = iteration
			logger.Info("▶️ Worker starting task, updating status to in-progress")
			if err := execStore.UpdateTask(ctx, *taskDetail); err != nil {
				logger.Error("Failed to mark task as in-progress", "error", err)
				continue
			}
			go execStore.UpdateTaskInDetails(ctx, *taskDetail)

			var pluginOutput interface{}
			var errExec error
			preparedContext := make(map[string]interface{})
			if task.Context != nil {
				for key, val := range task.Context {
					pathMap, isSecretRef := val.(map[string]interface{})
					if isSecretRef {
						if baoPath, hasPath := pathMap["baoPath"].(string); hasPath {
							resolvedBaoPath := strings.Replace(baoPath, "{{.ScenarioID}}", execStore.SID(), -1)
							etcdKey := "mrm/secrets/" + resolvedBaoPath
							resp, err := execStore.Client().Get(ctx, etcdKey)
							if err != nil || len(resp.Kvs) == 0 {
								taskDetail.Error = fmt.Sprintf("failed to fetch secret from etcd at key %s: %v", etcdKey, err)
								goto finalize
							}
							var encryptedSecrets map[string]string
							if err := json.Unmarshal(resp.Kvs[0].Value, &encryptedSecrets); err != nil {
								taskDetail.Error = fmt.Sprintf("failed to unmarshal encrypted secret from key %s: %v", etcdKey, err)
								goto finalize
							}
							if secretKey, wantsSpecificKey := pathMap["key"].(string); wantsSpecificKey {
								encryptedVal, found := encryptedSecrets[secretKey]
								if !found {
									taskDetail.Error = fmt.Sprintf("key '%s' not found in secret at path %s", secretKey, baoPath)
									goto finalize
								}
								decryptedVal, err := cryptoService.Decrypt(encryptedVal)
								if err != nil {
									taskDetail.Error = "failed to decrypt secret"
									goto finalize
								}
								preparedContext[key] = decryptedVal
							} else {
								decryptedSecrets := make(map[string]interface{})
								for k, v := range encryptedSecrets {
									decryptedVal, err := cryptoService.Decrypt(v)
									if err != nil {
										taskDetail.Error = fmt.Sprintf("failed to decrypt field '%s' in secret at path %s", k, baoPath)
										goto finalize
									}
									decryptedSecrets[k] = decryptedVal
								}
								preparedContext[key] = decryptedSecrets
							}
							continue
						}
					}
					preparedContext[key] = val
				}
			}
			
			pluginOutput, errExec = r.executePluginTask(task, preparedContext)

		finalize:
			endTime := time.Now()
			taskDetail.EndTime = &endTime
			if errExec != nil || taskDetail.Error != "" {
				if taskDetail.Error == "" {
					taskDetail.Error = errExec.Error()
				}
				taskDetail.Status = store.StatusFailed
				logger.Error("❌ Worker finished task with error", "duration", endTime.Sub(*taskDetail.StartTime), "error", taskDetail.Error)
			} else {
				taskDetail.Status = store.StatusCompleted
				taskDetail.Result = pluginOutput
				logger.Info("🚀 Worker finished task successfully", "duration", endTime.Sub(*taskDetail.StartTime))
			}
			if err := execStore.UpdateTask(ctx, *taskDetail); err != nil {
				logger.Error("CRITICAL: Failed to write final task status to individual key!", "error", err)
			}
			go execStore.UpdateTaskInDetails(ctx, *taskDetail)
			select {
			case workerDoneSignal <- true:
			case <-ctx.Done():
			}
		case <-ctx.Done():
			return
		}
	}
}

// executePluginTask is the real implementation that loads and runs a .so plugin.
func (r *Runner) executePluginTask(task config.Task, preparedContext map[string]interface{}) (interface{}, error) {
	pluginPath := filepath.Join(r.pluginsDir, task.Package)
	p, err := plugin.Open(pluginPath)
	if err != nil {
		return nil, fmt.Errorf("task '%s': error loading plugin %s: %w", task.ID, task.Package, err)
	}

	execSymbol, err := p.Lookup(task.Method)
	if err != nil {
		return nil, fmt.Errorf("task '%s': error looking up method '%s' in plugin %s: %w", task.ID, task.Method, task.Package, err)
	}

	executeFunc, ok := execSymbol.(func(map[string]interface{}) (interface{}, error))
	if !ok {
		return nil, fmt.Errorf("task '%s': method '%s' in plugin %s has incorrect signature", task.ID, task.Method, task.Package)
	}

	return executeFunc(preparedContext)
}

// detectDependencyCycle and helpers check for circular dependencies in the config.
const (
	unvisited = iota
	visiting
	visited
)

func detectDependencyCycle(tasks []config.Task) error {
	taskMap := make(map[string]config.Task)
	for _, task := range tasks {
		taskMap[task.ID] = task
	}
	visitation := make(map[string]int)
	for _, task := range tasks {
		visitation[task.ID] = unvisited
	}
	for _, task := range tasks {
		if visitation[task.ID] == unvisited {
			var recursionStack []string
			if err := findCycleDFS(task.ID, taskMap, visitation, &recursionStack); err != nil {
				return err
			}
		}
	}
	return nil
}

func findCycleDFS(taskID string, taskMap map[string]config.Task, visitation map[string]int, recursionStack *[]string) error {
	visitation[taskID] = visiting
	*recursionStack = append(*recursionStack, taskID)
	task, exists := taskMap[taskID]
	if !exists {
		return fmt.Errorf("task '%s' has a dependency on '%s', which is not defined in this action", (*recursionStack)[0], taskID)
	}
	if task.Dependencies != nil {
		for _, dep := range task.Dependencies.Tasks {
			if visitation[dep] == visiting {
				return fmt.Errorf("cycle detected: %s -> %s", strings.Join(*recursionStack, " -> "), dep)
			}
			if visitation[dep] == unvisited {
				if err := findCycleDFS(dep, taskMap, visitation, recursionStack); err != nil {
					return err
				}
			}
		}
	}
	visitation[taskID] = visited
	*recursionStack = (*recursionStack)[:len(*recursionStack)-1]
	return nil
}