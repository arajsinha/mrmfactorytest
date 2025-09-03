package taskrunner

import (
	"fmt"
	"plugin"
	"reflect"

	"mrm_cell/internal/config"
)

// executePluginTask loads and runs a function from a compiled Go plugin.
func executePluginTask(task config.Task, context map[string]interface{}) (interface{}, error) {
	p, err := plugin.Open(task.Package)
	if err != nil {
		return nil, fmt.Errorf("task '%s': error loading plugin %s: %w", task.ID, task.Package, err)
	}

	sym, err := p.Lookup(task.Method)
	if err != nil {
		return nil, fmt.Errorf("task '%s': error looking up symbol %s: %w", task.ID, task.Method, err)
	}

	funcValue, ok := sym.(func(map[string]interface{}) (interface{}, error))
	if !ok {
		// Fallback for less specific plugin signatures, but the above is safer.
		val := reflect.ValueOf(sym)
		if val.Kind() != reflect.Func {
			return nil, fmt.Errorf("task '%s': symbol %s is not a function", task.ID, task.Method)
		}
		// You could add more detailed reflection checks here if needed.
		// For simplicity, we primarily rely on the type assertion above.
		return nil, fmt.Errorf("task '%s': symbol %s has an incorrect signature", task.ID, task.Method)
	}

	return funcValue(context)
}
