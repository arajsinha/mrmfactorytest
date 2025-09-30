package config

import (
	"os"
	"gopkg.in/yaml.v3"
)

type Config struct {
	FSM FSM `yaml:"fsm"`
}

type FSM struct {
	Definition FSMDefinition `yaml:"definition"`
	Behavior   FSMBehavior   `yaml:"behavior"`
}

type FSMDefinition struct {
	States   []string `yaml:"states"`
	Triggers Triggers `yaml:"triggers"`
}

type Triggers struct {
	External []string `yaml:"external"`
	Internal []string `yaml:"internal"`
}

type FSMBehavior struct {
	Transitions []Transition `yaml:"transitions"`
	Actions     []ActionSet  `yaml:"actions"`
}

type Transition struct {
	Trigger string   `yaml:"trigger"`
	Changes []Change `yaml:"changes"`
}

type Change struct {
	CurrentState string `yaml:"currentState"`
	NextState    string `yaml:"nextState"`
}

type ActionSet struct {
	State string `yaml:"state"`
	Tasks []Task `yaml:"tasks"`
}

type Task struct {
	ID             string                 `yaml:"id"`
	Package        string                 `yaml:"package"`
	Method         string                 `yaml:"method"`
	OutputVariable string                 `yaml:"outputVariable"`
	Context        map[string]interface{} `yaml:"context"`
	Dependencies   *Dependencies          `yaml:"dependencies,omitempty"`
}

type Dependencies struct {
	Tasks []string `yaml:"tasks"`
}

func Load(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}