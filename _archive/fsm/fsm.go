package fsm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"mrm_cell/internal/config"
	"mrm_cell/internal/store"
	"mrm_cell/internal/taskrunner"
)

type State string
type Event string

type Machine struct {
	cfg       *config.Config
	runner    *taskrunner.Runner
	logger    *slog.Logger
	store     *store.ExecutionStore
	state     State
	mu        sync.Mutex
	eventChan chan Event
}

func NewMachine(ctx context.Context, cfg *config.Config, runner *taskrunner.Runner, logger *slog.Logger, storeMgr *store.ExecutionStore) *Machine {
	m := &Machine{
		cfg:       cfg,
		runner:    runner,
		logger:    logger,
		store:     storeMgr,
		eventChan: make(chan Event, 10),
	}

	persistedState, err := storeMgr.GetCurrentFSMState(ctx)
	if err == nil {
		m.state = State(persistedState)
		logger.Info("Initialized FSM with state retrieved from etcd", "state", m.state)
	} else if errors.Is(err, store.ErrStateNotFound) {
		initialState := State(cfg.FSM.Definition.States[0])
		m.state = initialState
		logger.Info("No previous state found in etcd. Bootstrapping initial FSM state.", "state", m.state)
		if writeErr := m.store.SetCurrentFSMState(ctx, string(m.state)); writeErr != nil {
			logger.Error("Failed to write initial state to etcd", "error", writeErr)
		}
	} else {
		panic(fmt.Sprintf("FSM initialization failed: %v", err))
	}
	return m
}

func generateEID() string {
	return time.Now().Format("20060102-150405-999999999")
}

func (m *Machine) Run(ctx context.Context) {
	m.logger.Info("FSM Started", "initial_state", m.state)
	m.eventChan <- Event(m.runner.GetCommand())

	for {
		select {
		case event := <-m.eventChan:
			go m.handleEvent(ctx, event)
		case <-ctx.Done():
			m.logger.Info("FSM Shutting down due to context cancellation.")
			return
		}
	}
}

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
	
	fromState := m.state
	m.state = potentialNextState
	m.logger.Info("➡️ Event triggered transition", "event", event, "from_state", fromState, "to_state", m.state)
	if err := m.store.SetCurrentFSMState(ctx, string(m.state)); err != nil {
		m.logger.Error("FATAL: Failed to persist FSM state change to etcd!", "error", err)
		return
	}
	
	isInternalEvent := false
	for _, e := range m.cfg.FSM.Definition.Triggers.Internal {
		if e == string(event) {
			isInternalEvent = true
			break
		}
	}

	actionsToRun := m.getActionsForState(fromState)
	if len(actionsToRun) > 0 && !isInternalEvent {
		eid := generateEID()
		execStore := store.NewExecutionStore(m.store.Client(), m.logger, m.store.SID(), eid)
		
		if err := execStore.InitializeExecution(ctx, string(fromState), string(m.state), actionsToRun); err != nil {
			m.logger.Error("FATAL: Failed to initialize execution record in etcd!", "eid", eid, "error", err)
			return
		}
		
		m.logger.Info("Successfully created execution record. Handing off to task runner.", "sid", m.store.SID(), "eid", eid)
		
		success, err := m.runner.ExecuteActions(ctx, actionsToRun, execStore)
		if err != nil {
			m.logger.Error("Task runner failed catastrophically", "error", err)
			success = false
		}
		
		var nextEvent Event
		if success {
			nextEvent = Event(m.cfg.FSM.Definition.Triggers.Internal[1]) // Assumes SwitchComplete is second
		} else {
			nextEvent = Event(m.cfg.FSM.Definition.Triggers.Internal[0]) // Assumes SwitchFailed is first
		}
		m.logger.Info("Transient state actions finished", "in_state", fromState, "outcome_success", success, "triggering_event", nextEvent)
		m.eventChan <- nextEvent
	} else if m.state == "ErrorState" {
		m.logger.Error("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
		m.logger.Error("!!! FSM ENTERED ERROR STATE. !!!")
		m.logger.Error("!!! Check etcd for full execution details under EID: " + m.store.EID() + " !!!")
		m.logger.Error("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
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
	for _, e := range m.cfg.FSM.Definition.Triggers.External { if e == eventStr { return true } }
	for _, e := range m.cfg.FSM.Definition.Triggers.Internal { if e == eventStr { return true } }
	return false
}