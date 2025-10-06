package taskrunner

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"github.com/Knetic/govaluate"
)

// get is a helper function for govaluate to access nested map values.
func get(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("get function requires at least a map and a key")
	}
	current := args[0]
	for i := 1; i < len(args); i++ {
		key, ok := args[i].(string)
		if !ok {
			return nil, fmt.Errorf("get function keys must be strings, got %T", args[i])
		}
		currentMap, ok := current.(map[string]interface{})
		if !ok {
			// It's possible we are trying to access a field on a non-map value, which is valid to check.
			// Example: get(outputs, "my-task", "error") where the output is just a string. This should not error out.
			return nil, nil
		}
		value, exists := currentMap[key]
		if !exists {
			return nil, nil // Return nil for non-existent keys
		}
		current = value
	}
	return current, nil
}

// resolveContext replaces placeholders in a task's context with actual prior task outputs.
func resolveContext(logger *slog.Logger, taskContext map[string]interface{}, allPriorTaskOutputs map[string]interface{}) (map[string]interface{}, error) {
	resolvedContext := make(map[string]interface{})
	templateData := map[string]interface{}{"outputs": allPriorTaskOutputs}

	for key, val := range taskContext {
		if strVal, ok := val.(string); ok {
			if strings.Contains(strVal, "{{") && strings.Contains(strVal, "}}") {
				tmpl, err := template.New(key).Parse(strVal)
				if err != nil {
					return nil, fmt.Errorf("error parsing template for context key '%s': %v, %v", key, strVal, err)
				}
				var buf bytes.Buffer
				if err := tmpl.Execute(&buf, templateData); err != nil {
					logger.Warn("Error executing template, using original value", "key", key, "error", err)
					resolvedContext[key] = strVal
				} else {
					resolvedContext[key] = buf.String()
				}
			} else {
				resolvedContext[key] = strVal
			}
		} else {
			resolvedContext[key] = val
		}
	}
	return resolvedContext, nil
}

// evaluateCondition evaluates a string condition to a boolean.
// It creates custom functions 'get' and 'status' for use in the expression.
func evaluateCondition(logger *slog.Logger, conditionStr string, taskStatus map[string]string, allPriorTaskOutputs map[string]interface{}) (bool, error) {
	if conditionStr == "" {
		return true, nil
	}

	// Define the custom 'status' function for expressions.
	// It's defined here as a closure so it has access to the taskStatus map.
	statusFunc := func(args ...interface{}) (interface{}, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("status() function requires exactly one argument: a task ID string")
		}
		taskID, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("status() argument must be a string")
		}
		// Return the status, or an empty string if not found (safer for evaluation)
		if status, exists := taskStatus[taskID]; exists {
			return status, nil
		}
		return "", nil
	}

	// The 'get' function is already defined at the package level, so we can just reference it.
	functions := map[string]govaluate.ExpressionFunction{
		"get":    get,
		"status": statusFunc,
	}

	expression, err := govaluate.NewEvaluableExpressionWithFunctions(conditionStr, functions)
	if err != nil {
		return false, fmt.Errorf("invalid condition expression '%s': %w", conditionStr, err)
	}

	// The 'outputs' map is passed directly to the evaluator for the 'get' function.
	parameters := map[string]interface{}{
		"outputs": allPriorTaskOutputs,
	}

	result, err := expression.Evaluate(parameters)
	if err != nil {
		logger.Warn("Error during condition evaluation, assuming false", "condition", conditionStr, "error", err)
		return false, nil
	}
	boolResult, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("condition expression '%s' did not evaluate to a boolean, but got %T", conditionStr, result)
	}
	return boolResult, nil
}
