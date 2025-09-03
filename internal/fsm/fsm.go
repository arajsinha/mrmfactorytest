package fsm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mrm_cell/internal/config"
	"mrm_cell/internal/store"
	"mrm_cell/internal/taskrunner"
	"strings"
	"sync"
	"time"
)

type State string
type Event string

// Machine encapsulates the state machine's state and dependencies.
type Machine struct {
	cfg    *config.Config
	runner *taskrunner.Runner
	logger *slog.Logger
	store  *store.ExecutionStore
	state  State
	mu     sync.Mutex
}

// NewMachine creates a new FSM machine, initializing its state from the store.
func NewMachine(ctx context.Context, cfg *config.Config, runner *taskrunner.Runner, logger *slog.Logger, storeMgr *store.ExecutionStore) *Machine {
	m := &Machine{
		cfg:    cfg,
		runner: runner,
		logger: logger,
		store:  storeMgr,
	}

	persistedState, err := storeMgr.GetCurrentFSMState(ctx)
	if err == nil {
		m.state = State(persistedState)
		logger.Info("Initialized FSM with state retrieved from etcd", "state", m.state)
	} else if errors.Is(err, store.ErrStateNotFound) {
		initialState := State("ErrorState")
		if len(cfg.FSM.Definition.States) > 0 {
			initialState = State(cfg.FSM.Definition.States[0])
		}
		m.state = initialState
		logger.Info("No previous state found in etcd. Bootstrapping initial FSM state.", "state", m.state)
		if writeErr := m.store.SetCurrentFSMState(ctx, string(m.state)); writeErr != nil {
			logger.Error("Failed to write initial state to etcd, continuing with in-memory state", "error", writeErr)
		}
	} else {
		logger.Error("FATAL: Could not retrieve initial FSM state from etcd.", "error", err)
		panic(fmt.Sprintf("FSM initialization failed: %v", err))
	}

	return m
}

// generateEID creates a new unique execution ID based on the current time.
func generateEID() string {
	return time.Now().Format("20060102-150405-999999999")
}

// Run starts the FSM logic.
func (m *Machine) Run(ctx context.Context) {
	m.logger.Info("FSM Started", "initial_state", m.state)
	go func() {
		m.handleEvent(ctx, Event(m.runner.GetCommand()))
	}()
	<-ctx.Done()
	m.logger.Info("FSM Shutting down due to context cancellation.")
}

// calculateIterations analyzes the dependency graph to determine the iteration number for each task.
func calculateIterations(tasks []config.Task) map[string]int {
	iterations := make(map[string]int)
	taskMap := make(map[string]config.Task)
	for _, task := range tasks {
		taskMap[task.ID] = task
	}

	var calculate func(taskID string) int
	calculate = func(taskID string) int {
		if iter, found := iterations[taskID]; found {
			return iter
		}

		task := taskMap[taskID]
		if task.Dependencies == nil || len(task.Dependencies.Tasks) == 0 {
			iterations[taskID] = 1
			return 1
		}

		maxDepIter := 0
		for _, depID := range task.Dependencies.Tasks {
			depIter := calculate(depID)
			if depIter > maxDepIter {
				maxDepIter = depIter
			}
		}

		iteration := maxDepIter + 1
		iterations[taskID] = iteration
		return iteration
	}

	for _, task := range tasks {
		if _, found := iterations[task.ID]; !found {
			calculate(task.ID)
		}
	}
	return iterations
}

// handleEvent now calculates iterations and passes them to the store.
func (m *Machine) handleEvent(ctx context.Context, event Event) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.isValidEvent(event) {
		m.logger.Warn("Invalid event received", "event", event, "current_state", m.state)
		return
	}

	potentialNextState := m.transitionState(event)
	if potentialNextState == m.state {
		m.logger.Info("Event did not cause a state change", "event", event, "current_state", m.state)
		return
	}
	
	initialState := m.state
	m.logger.Info(
		fmt.Sprintf("➡️ Event '%s' triggered transition: %s -> %s", event, initialState, potentialNextState),
		"event", event, "from_state", initialState, "to_state", potentialNextState,
	)

	m.state = potentialNextState
	if err := m.store.SetCurrentFSMState(ctx, string(m.state)); err != nil {
		m.logger.Error("FATAL: Failed to persist FSM state change to etcd! Halting execution.", "new_state", m.state, "error", err)
		return
	}

	tasksToRun := m.getActionsForState(m.state)
	if len(tasksToRun) == 0 {
		m.logger.Info("State transition resulted in a state with no actions to perform.", "state", m.state)
		if strings.HasPrefix(string(m.state), "SwitchingTo") {
			m.logger.Info("Resolving transient state with no tasks.", "triggering_event", "SwitchComplete")
			m.handleInternalEvent(ctx, "SwitchComplete", "")
		}
		return
	}
	
	eid := generateEID()
	
	// Before initializing, update the "active_eid" key so the monitor knows what's running.
	if err := m.store.SetActiveEID(ctx, eid); err != nil {
		m.logger.Error("FATAL: Failed to set active EID in etcd! Halting execution.", "eid", eid, "error", err)
		return
	}

	execStore := store.NewExecutionStore(m.store.Client(), m.logger, m.store.SID(), eid)

	// --- NEW: Calculate iterations before initializing ---
	iterations := calculateIterations(tasksToRun)
	if err := execStore.InitializeExecution(ctx, tasksToRun, iterations, string(potentialNextState)); err != nil {
		m.logger.Error("FATAL: Failed to initialize execution record in etcd! Halting execution.", "eid", eid, "error", err)
		return
	}
	
	m.logger.Info("Successfully created execution record. Handing off to task runner.", "sid", m.store.SID(), "eid", eid)

	m.mu.Unlock()
	actions, err := m.runner.ExecuteActions(ctx, tasksToRun, execStore)
	if err != nil {
		m.logger.Error("Task runner failed catastrophically", "error", err)
		actions = false
	}
	m.mu.Lock()

	if strings.HasPrefix(string(m.state), "SwitchingTo") {
		var internalEvent Event
		if actions { internalEvent = "SwitchComplete" } else { internalEvent = "SwitchFailed" }
		m.logger.Info("Transient state actions finished", "in_state", m.state, "outcome_success", actions, "triggering_event", internalEvent)
		m.handleInternalEvent(ctx, internalEvent, eid)
	}
}

// handleInternalEvent's signature and body are now corrected.
func (m *Machine) handleInternalEvent(ctx context.Context, event Event, eid string) {
	logger := m.logger.With("internal_event", event, "current_state", m.state)
	finalStateAfterSwitch := m.transitionState(event)

	if finalStateAfterSwitch != m.state {
		logger.Info(fmt.Sprintf("➡️  Internal event '%s' triggered transition: %s -> %s", event, m.state, finalStateAfterSwitch))
		m.state = finalStateAfterSwitch

		if err := m.store.SetCurrentFSMState(ctx, string(m.state)); err != nil {
			logger.Error("FATAL: Failed to persist final FSM state change to etcd!", "new_state", m.state, "error", err)
		}

		if m.state == "ErrorState" {
			// --- THIS BLOCK IS NOW CORRECTED ---
			// It no longer tries to print a log from memory. It points to the persistent record in etcd.
			fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
			fmt.Println("!!! FSM ENTERED ERROR STATE. !!!")
			fmt.Printf("!!! Check etcd for full execution details under EID: %s !!!\n", eid)
			fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
		}
	}
}

func (m *Machine) transitionState(event Event) State {
	for _, transition := range m.cfg.FSM.Behavior.Transitions {
		if transition.Trigger == string(event) {
			for _, change := range transition.Changes {
				if change.CurrentState == string(m.state) {
					return State(change.NextState)
				}
			}
		}
	}
	return m.state
}

func (m *Machine) getActionsForState(state State) []config.Task {
	for _, action := range m.cfg.FSM.Behavior.Actions {
		if action.State == string(state) {
			return action.Tasks
		}
	}
	return nil
}

func (m *Machine) isValidEvent(event Event) bool {
	eventStr := string(event)
	for _, e := range m.cfg.FSM.Definition.Triggers.External {
		if e == eventStr {
			return true
		}
	}
	for _, e := range m.cfg.FSM.Definition.Triggers.Internal {
		if e == eventStr {
			return true
		}
	}
	return false
}