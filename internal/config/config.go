package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// --- NEW: SecretRef represents a reference to a secret in OpenBao ---
type SecretRef struct {
	BaoPath string `yaml:"baoPath"`
	Key     string `yaml:"key,omitempty"`
}

// Config is the top-level configuration structure.
type Config struct {
	FSM FSM `yaml:"fsm"`
}

// FSM holds the full state machine definition and behavior.
type FSM struct {
	Definition FSMDefinition `yaml:"definition"`
	Behavior   FSMBehavior   `yaml:"behavior"`
}

// FSMDefinition defines the static parts of the FSM.
type FSMDefinition struct {
	States   []string      `yaml:"states"`
	Triggers Triggers      `yaml:"triggers"`
}

// Triggers lists the different types of events.
type Triggers struct {
	External []string `yaml:"external"`
	Internal []string `yaml:"internal"`
}

// FSMBehavior defines the dynamic parts of the FSM.
type FSMBehavior struct {
	Transitions []Transition `yaml:"transitions"`
	Actions     []Action     `yaml:"actions"`
}

// Transition defines a state change based on a trigger.
type Transition struct {
	Trigger string   `yaml:"trigger"`
	Changes []Change `yaml:"changes"`
}

// Change represents a single state transition rule.
type Change struct {
	CurrentState string `yaml:"currentState"`
	NextState    string `yaml:"nextState"`
}

// Action defines the tasks to be executed in a given state.
type Action struct {
	State string `yaml:"state"`
	Tasks []Task `yaml:"tasks"`
}

// Task represents a single unit of work.
type Task struct {
	ID             string                 `yaml:"id"`
	Package        string                 `yaml:"package"`
	Method         string                 `yaml:"method"`
	OutputVariable string                 `yaml:"outputVariable,omitempty"`
	Condition      string                 `yaml:"condition,omitempty"`
	Dependencies   *Dependencies          `yaml:"dependencies,omitempty"`
	// The Context can now contain simple values, secret references, or nested maps.
	Context        map[string]interface{} `yaml:"context,omitempty"` 
}

// Dependencies defines the prerequisites for a task.
type Dependencies struct {
	Tasks      []string `yaml:"tasks"`
	Expression string   `yaml:"expression,omitempty"`
}

// Load loads the FSM configuration from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal yaml config: %w", err)
	}

	return &cfg, nil
}