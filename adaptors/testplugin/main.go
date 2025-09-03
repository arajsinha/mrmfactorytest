package main

import (
	"fmt"
	"log"
	"time"
)

// Execute simulates a long-running task by sleeping for a given duration.
// It can now be configured to fail intentionally.
func Execute(context map[string]interface{}) (interface{}, error) {
	durationStr, ok := context["duration"].(string)
	if !ok {
		return nil, fmt.Errorf("context is missing 'duration' string")
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return nil, fmt.Errorf("invalid duration format '%s': %v", durationStr, err)
	}

	log.Printf("⏳ [Sleeper Plugin] Task starting. Will run for %v.", duration)
	time.Sleep(duration)

    // NEW: Check if the task should fail
    if shouldFail, ok := context["should_fail"].(bool); ok && shouldFail {
        log.Printf("🔥 [Sleeper Plugin] Task configured to fail.")
        // Return a result map AND an error
        return map[string]interface{}{"status": "FAILURE"}, fmt.Errorf("task failed intentionally for testing")
    }

	log.Printf("✅ [Sleeper Plugin] Task finished after %v.", duration)

	// NEW: Return structured data for testing the get() function
	result := map[string]interface{}{
		"status":      "SUCCESS",
		"slept_for":   duration.String(),
		"result_code": 100,
	}

	return result, nil
}