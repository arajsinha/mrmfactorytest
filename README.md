# Go FSM Task Execution Engine

This project implements a highly concurrent, production-ready Finite State Machine (FSM) engine in Go. It drives state transitions based on a declarative YAML configuration and executes complex, dependent task graphs for each state using a high-performance worker pool.

It is designed for reliability, maintainability, and operational excellence, making it suitable for orchestrating complex workflows such as infrastructure failovers, CI/CD pipelines, or any process-driven automation.

## Key Features

* **Declarative FSM:** Define states, events, and transitions entirely in a `fsm-config.yaml` file.
* **Concurrent Execution Engine:** Utilizes a manager-worker pattern with a configurable pool of goroutines to execute tasks with maximum parallelism, minimizing total runtime.
* **Dependency Management:** Supports complex task graphs with `dependsOn` relationships.
* **Conditional Execution:** Tasks can be conditionally run or skipped based on the output of previous tasks using `condition` expressions.
* **Plugin Architecture:** Easily extend the engine's capabilities by creating simple, compiled Go plugins (`.so` files).
* **Structured & Human-Readable Logging:** Supports JSON-formatted logs for production and a "developer-friendly" text format for interactive use, controlled by a flag.
* **Graceful Shutdown:** Listens for OS interrupt signals (e.g., `Ctrl+C`) to shut down cleanly.
* **Configuration via Flags:** Application parameters like config file path and worker count are configurable via command-line flags.

## Project Structure

The project is organized using a standard Go application layout to ensure a clean separation of concerns.

```
mrm_prod_non_docker/
├── adaptors/
├── cmd/
│   └── fsm-app/
│       └── main.go         # Main application entry point. Handles setup and wiring.
├── internal/
│   ├── config/
│   │   └── config.go       # Structs and loader for the fsm-config.yaml.
│   ├── fsm/
│   │   └── fsm.go          # Core state machine logic and the user interaction loop.
│   └── taskrunner/
│       ├── runner.go       # The high-performance task execution engine.
│       ├── plugins.go      # Logic for loading and executing Go plugins.
│       └── conditions.go   # Logic for conditional expressions and context templating.
├── plugins/
│   ├── sleeper.go          # Example plugin source code.
│   └── sleeper.so          # A compiled plugin.
└── fsm-config.yaml         # The heart of the application's configuration.
```

* **`cmd/fsm-app/`**: Contains the `main` package, the entry point of the application. Its sole responsibility is to parse command-line flags, set up dependencies like the logger and configuration, wire the components together, and handle graceful shutdown signals.
* **`internal/config/`**: Defines all the Go structs that map directly to the `fsm-config.yaml` file. It provides a clean, reusable way to load and validate the configuration.
* **`internal/fsm/`**: Encapsulates the logic of the Finite State Machine itself. It manages the current state, processes events, determines transitions, and calls the task runner when a state change occurs.
* **`internal/taskrunner/`**: The core execution engine. It takes a list of tasks for a given state and orchestrates their execution with high efficiency.
* **`plugins/`**: A directory to store both the source code (`.go`) and the compiled shared object files (`.so`) for your custom plugins.

## Core Architecture & Algorithm

The engine's logic is split into two primary components: the **FSM** that directs the flow, and the **Task Runner** that performs the work.

### 1. The Finite State Machine

The FSM operates based on the definitions in `fsm-config.yaml`. Its algorithm is as follows:
1.  Start in the initial state defined in the configuration.
2.  Display the current state and available events to the user.
3.  Wait for the user to input an event.
4.  Upon receiving an event, look up the `transitions` table in the config.
5.  If a transition exists for the current state and the given event, move to the `nextState`.
6.  If the state has changed, delegate the list of tasks associated with the new state to the **Task Runner**.
7.  If the state was a transient one (e.g., `SwitchingTo...`), wait for the Task Runner's result (success/failure) and trigger an internal event (`SwitchComplete` or `SwitchFailed`) to move to a final state.
8.  Repeat from step 2.

### 2. The Concurrent Task Execution Engine

This is a high-performance system designed to execute tasks as quickly as possible by eliminating idle time. It uses a **Manager-Worker** pattern (also known as a Dispatcher or Producer-Consumer).

**The Algorithm:**

1.  **Initialization**: When called, the Runner creates a communication system using Go channels and a fixed pool of `worker` goroutines.
2.  **The Hub (`ExecuteActions` function)**: The main function acts as the central hub. It starts the workers and the dispatcher.
3.  **The Dispatcher (a background goroutine)**:
    * It performs an initial scan of all tasks and sends any with no dependencies to the `tasksToRunChan`.
    * It then waits for signals on a `dispatcherSignalChan`. Each signal tells it that a task has finished, so it should re-scan the task list to see if any new tasks have become runnable.
4.  **The Worker Pool (a set of goroutines)**:
    * Workers block and wait for a task to arrive on the `tasksToRunChan`.
    * When a task is received, the worker executes it (evaluating conditions, resolving context, and calling the plugin).
    * Upon completion (success, failure, or skip), the worker sends a `TaskExecutionRecord` back to the central hub via the `resultsChan`.
5.  **The Hub's Collection Loop**:
    * The hub listens on the `resultsChan`, collecting a result for every task.
    * Crucially, every time it receives a result, it sends a signal to the **Dispatcher**, waking it up to check for new work.
6.  **Post-Processing**: After all runnable tasks are complete, the engine performs a final check to resolve any tasks that never ran, marking them as `BLOCKED` (if a dependency failed) or `SKIPPED` (if a dependency was skipped).
7.  **Result**: The engine returns a final `true`/`false` success status and a detailed log of every task's outcome.

**Visual Flow:**

```
                        +----------------------------+
                        | Main `ExecuteActions` Hub  |
                        +-------------+--------------+
                                      |
                                      | 1. Collects final results
                                      |
                              +-------v-------+
                              | `resultsChan` |
                              +-------+-------+
                                      ^
                                      | 2. Worker sends result when done
                                      |
      +-------------------+<----[Go Channel]----+-------------------+
      |                   |                    |                   |
+-----v-----+       +-----v-----+        +-----v-----+       +-----v-----+
| Worker 1  |       | Worker 2  | ...... | Worker N  |       | Dispatcher|
+-----------+       +-----------+        +-----------+       +-----+-----+
      ^                   ^                    ^                    | 3b. Wakes up,
      |                   |                    |                    |     scans, & dispatches
      | 3a. Dispatcher sends runnable tasks    |                    |
      |                   |                    |             +------v------+
      +-------------------+--------------------+-------------|tasksToRunChan|
                                                            +-------------+
```

## Configuration (`fsm-config.yaml`)

The entire behavior of the FSM and its actions are controlled by this file.

* **`fsm.definition`**: Defines the raw components of the state machine.
    * `states`: A list of all possible state names.
    * `triggers`: Events that can cause a state change. `external` are user-provided, `internal` are triggered by the system itself.
* **`fsm.behavior`**: Defines the logic of the FSM.
    * `transitions`: A table mapping a `(currentState, trigger)` pair to a `nextState`.
    * `actions`: A list where each entry binds a `state` to a list of `tasks`.
* **`tasks`**: An individual unit of work.
    * `id`: A unique identifier for the task within its action.
    * `package`: The path to the compiled plugin file (`.so`).
    * `method`: The function name to execute within the plugin (typically "Execute").
    * `context`: A map of key-value pairs passed as the argument to the plugin function.
    * `dependsOn`: A list of task `id`s that must be `completed` before this task can run.
    * `condition`: An optional expression string that must evaluate to `true` for the task to run. It can reference outputs of other tasks using `get(outputs, "taskID", "fieldName")`.
    * `outputVariable`: An optional name to store the entire output of this task under, for easier reference in downstream tasks.

## Creating Plugins

To extend the engine, create a standard Go file that defines a function with the following signature. The function name must match the `method` field in your YAML (e.g., "Execute").

```go
package main

// import "..."

func Execute(context map[string]interface{}) (interface{}, error) {
    // Your plugin logic here...
    // Read values from the context map.
    // Return a result (can be a map, string, etc.) and an error (or nil on success).
    
    result := map[string]interface{}{"status": "OK"}
    return result, nil
}
```

Compile it as a plugin from your project's root directory:
```bash
go build -buildmode=plugin -o plugins/myplugin.so path/to/your/plugin/source.go
```

## How to Run

All commands should be run from the root `fsm-executor/` directory.

### Prerequisites

1.  **Go Compiler**: Ensure you have a recent version of Go installed.
2.  **Compile Plugins**: The main application loads pre-compiled plugins. You must compile them first.
    ```bash
    # Example for the sleeper plugin
    go build -buildmode=plugin -o plugins/sleeper.so ./plugins/sleeper.go

    # Do this for all your other custom plugins as well.
    ```

### Development Mode

This mode is best for interactive use, providing clean, human-readable logs.

```bash
go run ./cmd/fsm-app/ -dev
```

* `go run` compiles and runs the application in one step.
* `./cmd/fsm-app/` is the path to the main application package.
* `-dev` is the flag to enable developer-friendly logging.

### Production Mode (Packaging)

This is the standard way to build a standalone binary for deployment.

1.  **Build the Executable:**
    ```bash
    go build -o fsm-app ./cmd/fsm-app/
    ```
    This creates a single executable file named `fsm-app` in your current directory.

2.  **Run the Executable:**
    ```bash
    # Run with default settings (fsm-config.yaml and 10 workers)
    ./fsm-app

    # Run with custom settings
    ./fsm-app -config /path/to/production.yaml -workers 50
    ```
    This mode will output structured JSON logs, which is ideal for log aggregation and monitoring systems.

### Graceful Shutdown

While the application is running, you can press **`Ctrl+C`**. You will see a "Received shutdown signal" message as the application attempts to finish its current work and exit cleanly.