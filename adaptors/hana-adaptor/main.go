package main // A plugin must be in the main package

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/SAP/go-hdb/driver"
)

// Execute is the exported function that will be called by the FSM orchestrator.
// It matches the required signature: func(map[string]interface{}) (interface{}, error).
func Execute(context map[string]interface{}) (interface{}, error) {
	log.Println("HANA Adaptor Plugin: Execution Started.")

	// --- 1. Extract and Validate Parameters from Context ---
	action, err := getStringFromContext(context, "action")
	if err != nil {
		return nil, fmt.Errorf("hana_plugin: %w", err)
	}
	log.Printf("HANA Adaptor Plugin: Action requested: %s", action)

	credentials, err := getCredentialsFromContext(context, "credentials")
	if err != nil {
		return nil, fmt.Errorf("hana_plugin: credentials error: %w", err)
	}

	remoteSource, err := getStringFromContext(context, "remoteSource")
	if err != nil {
		// For some actions, this might be optional. The action-specific functions will handle it.
		log.Printf("HANA Adaptor Plugin: Warning - 'remoteSource' not found in context. This may be acceptable for some actions.")
	}

	targetSchema, err := getStringFromContext(context, "targetSchema")
	if err != nil {
		return nil, fmt.Errorf("hana_plugin: %w", err)
	}

	log.Printf("HANA Adaptor Plugin: Parameters: action=%s, remoteSource=%s, targetSchema=%s", action, remoteSource, targetSchema)

	// --- 2. Dispatch to the Correct Action Handler ---
	var result interface{}
	var actionErr error

	switch action {
	case "deleteRemoteSubscriptions":
		log.Println("HANA Adaptor Plugin: Dispatching to DeleteRemoteSubscriptions.")
		result, actionErr = deleteRemoteSubscriptions(credentials, remoteSource, targetSchema)
	case "createRemoteSubscriptions":
		log.Println("HANA Adaptor Plugin: Dispatching to CreateRemoteSubscriptions.")
		result, actionErr = createRemoteSubscriptions(credentials, remoteSource, targetSchema)
	case "readRemoteSubscriptions":
		log.Println("HANA Adaptor Plugin: Dispatching to readRemoteSubscriptions.")
		result, actionErr = readRemoteSubscriptions(credentials, targetSchema)
	default:
		actionErr = fmt.Errorf("hana_plugin: unknown action '%s'", action)
	}

	// --- 3. Return Result and Error ---
	if actionErr != nil {
		log.Printf("HANA Adaptor Plugin: Action '%s' failed: %v", action, actionErr)
		return nil, actionErr // Return nil for the result on error
	}

	log.Printf("HANA Adaptor Plugin: Action '%s' completed successfully.", action)
	return result, nil // Return the result from the action function and a nil error
}

// --- Helper Functions to Safely Extract Data from Context ---

// getStringFromContext safely gets a string from the context map.
func getStringFromContext(context map[string]interface{}, key string) (string, error) {
	val, ok := context[key]
	if !ok {
		return "", fmt.Errorf("missing required key '%s' in context", key)
	}
	strVal, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("key '%s' in context is not a string, got %T", key, val)
	}
	return strVal, nil
}

// getCredentialsFromContext safely gets and validates the credentials map from the context.
func getCredentialsFromContext(context map[string]interface{}, key string) (map[string]string, error) {
	val, ok := context[key]
	if !ok {
		return nil, fmt.Errorf("missing key '%s' for credentials in context", key)
	}

	// The value from YAML can be map[string]interface{} or map[interface{}]interface{}.
	// We handle both and convert to the expected map[string]string.
	mapInterface, ok := val.(map[string]interface{})
	if !ok {
		if mapIfaceIface, okAlt := val.(map[interface{}]interface{}); okAlt {
			mapInterface = make(map[string]interface{})
			for k, v := range mapIfaceIface {
				if strKey, okKey := k.(string); okKey {
					mapInterface[strKey] = v
				}
			}
		} else {
			return nil, fmt.Errorf("credentials key '%s' is not a valid map, got %T", key, val)
		}
	}

	credentials := make(map[string]string)
	requiredKeys := []string{"SAPHANACLOUD_HOSTURL", "SAPHANACLOUD_USERNAME", "SAPHANACLOUD_PASSWORD"}

	for _, reqKey := range requiredKeys {
		credVal, ok := mapInterface[reqKey]
		if !ok {
			return nil, fmt.Errorf("missing required credential key '%s' in credentials map", reqKey)
		}
		if strCredVal, ok := credVal.(string); ok {
			credentials[reqKey] = strCredVal
		} else {
			return nil, fmt.Errorf("credential value for '%s' is not a string, got %T", reqKey, credVal)
		}
	}
	return credentials, nil
}

// --- Database Interaction Logic ---

// getDBConnection establishes and pings a new database connection.
func getDBConnection(credentials map[string]string) (*sql.DB, error) {
	dsn := fmt.Sprintf("hdb://%s:%s@%s:443?TLSServerName=%s",
		credentials["SAPHANACLOUD_USERNAME"],
		credentials["SAPHANACLOUD_PASSWORD"],
		credentials["SAPHANACLOUD_HOSTURL"],
		credentials["SAPHANACLOUD_HOSTURL"],
	)
	connector, err := driver.NewDSNConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to create DSN connector: %w", err)
	}
	db := sql.OpenDB(connector)

	err = db.Ping()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to HANA: %w", err)
	}
	log.Println("Successfully connected to HANA Cloud.")
	return db, nil
}

// executeQuery is a generic helper to run a SELECT query and return results.
func executeQuery(db *sql.DB, query string, args ...interface{}) ([]map[string]interface{}, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %s, error: %w", query, err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	results := []map[string]interface{}{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		err := rows.Scan(valuePtrs...)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}
	return results, nil
}

// executeInTx executes a list of SQL queries within a single transaction.
func executeInTx(credentials map[string]string, queryList []string) error {
	if len(queryList) == 0 {
		log.Println("executeInTx called with an empty query list. Nothing to do.")
		return nil
	}
	db, err := getDBConnection(credentials)
	if err != nil {
		return fmt.Errorf("failed to get DB connection for transaction: %w", err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	for i, query := range queryList {
		log.Printf("Executing in TX (%d/%d): %s", i+1, len(queryList), query)
		if _, err := tx.Exec(query); err != nil {
			tx.Rollback() // Attempt to roll back on error.
			return fmt.Errorf("error on query '%s': %w", query, err)
		}
	}

	log.Println("All queries in transaction executed. Committing...")
	return tx.Commit()
}

// --- Action-Specific Functions ---

// readRemoteSubscriptions fetches all remote subscriptions for a given schema.
func readRemoteSubscriptions(credentials map[string]string, targetSchema string) ([]map[string]interface{}, error) {
	log.Println("Fetching list of Remote Subscriptions for schema:", targetSchema)
	db, err := getDBConnection(credentials)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return executeQuery(db, `SELECT SUBSCRIPTION_NAME, TARGET_OBJECT_NAME FROM "SYS"."REMOTE_SUBSCRIPTIONS" WHERE "TARGET_OBJECT_SCHEMA_NAME" = ?`, targetSchema)
}

// deleteRemoteSubscriptions finds and drops all remote subscriptions for a schema.
func deleteRemoteSubscriptions(credentials map[string]string, remoteSource string, targetSchema string) (interface{}, error) {
	subscriptions, err := readRemoteSubscriptions(credentials, targetSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to read subscriptions before delete: %w", err)
	}

	if len(subscriptions) == 0 {
		msg := fmt.Sprintf("No remote subscriptions found to delete for schema: %s", targetSchema)
		log.Println(msg)
		return map[string]interface{}{"status": "success", "message": msg}, nil
	}

	log.Printf("Found %d remote subscriptions to delete for schema %s.", len(subscriptions), targetSchema)
	var queries []string
	queries = append(queries, fmt.Sprintf(`ALTER REMOTE SOURCE "%s" SUSPEND CAPTURE`, remoteSource))

	for _, sub := range subscriptions {
		if name, ok := sub["SUBSCRIPTION_NAME"].(string); ok {
			queries = append(queries, fmt.Sprintf(`DROP REMOTE SUBSCRIPTION "%s"`, name))
		}
	}

	queries = append(queries, fmt.Sprintf(`ALTER REMOTE SOURCE "%s" RESUME CAPTURE`, remoteSource))

	if err := executeInTx(credentials, queries); err != nil {
		return nil, fmt.Errorf("failed to execute transaction for deleting subscriptions: %w", err)
	}

	time.Sleep(15 * time.Second) // Wait for changes to propagate.
	msg := fmt.Sprintf("Successfully deleted %d remote subscriptions.", len(subscriptions))
	log.Println(msg)
	return map[string]interface{}{"status": "success", "deleted_count": len(subscriptions), "message": msg}, nil
}

func readTablesInSchema(db *sql.DB, targetSchema string) ([]map[string]interface{}, error) {
	log.Println("Fetching list of Tables in Schema", targetSchema)
	return executeQuery(db, `SELECT TABLE_NAME, RECORD_COUNT FROM "SYS"."M_TABLES" WHERE "SCHEMA_NAME" = ?`, targetSchema)
}

func readVirtualTablesInSchema(db *sql.DB, targetSchema string) ([]map[string]interface{}, error) {
	log.Println("Fetching list of Virtual Tables in Schema", targetSchema)
	return executeQuery(db, `SELECT TABLE_NAME, REMOTE_OWNER_NAME, REMOTE_OBJECT_NAME FROM "SYS"."VIRTUAL_TABLES" WHERE "SCHEMA_NAME" = ? AND "TABLE_NAME" LIKE 'VT_%'`, targetSchema)
}

// createRemoteSubscriptions creates subscriptions for all tables in a schema, using the user's original logic.
func createRemoteSubscriptions(credentials map[string]string, remoteSource string, targetSchema string) (interface{}, error) {
	log.Println("Reading tables in schema to create subscriptions:", targetSchema)
	db, err := getDBConnection(credentials)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Step 1: Check if subscriptions already exist. If so, skip the entire process.
	existingSubs, err := executeQuery(db, `SELECT SUBSCRIPTION_NAME FROM "SYS"."REMOTE_SUBSCRIPTIONS" WHERE "TARGET_OBJECT_SCHEMA_NAME" = ?`, targetSchema)
	if err != nil {
		return nil, fmt.Errorf("could not check for existing remote subscriptions: %w", err)
	}
	if len(existingSubs) > 0 {
		msg := fmt.Sprintf("%d Remote subscriptions already exist for schema %s! Skipping creation.", len(existingSubs), targetSchema)
		log.Println(msg)
		return map[string]interface{}{"status": "skipped", "message": msg}, nil
	}

	// Step 2: Get a list of physical tables.
	listOfTables, err := readTablesInSchema(db, targetSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to read tables for creating subscriptions: %w", err)
	}
	if len(listOfTables) == 0 {
		msg := "No Tables found in schema " + targetSchema + " for remote subscriptions."
		log.Println(msg)
		return map[string]interface{}{"status": "success", "message": msg}, nil
	}

	// Step 3: Get a list of virtual tables and create a lookup map.
	listOfVirtualTables, err := readVirtualTablesInSchema(db, targetSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to read virtual tables for creating subscriptions: %w", err)
	}
	virtualTableMap := make(map[string]bool)
	for _, vt := range listOfVirtualTables {
		if vtName, ok := vt["TABLE_NAME"].(string); ok {
			virtualTableMap[vtName] = true
		}
	}
	if len(virtualTableMap) == 0 {
		log.Println("Warning: No Virtual Tables (starting with VT_) found. No subscriptions will be created.")
	}


	// Step 4: Build the query list.
	var queries []string
	createdCount := 0
	for _, table := range listOfTables {
		tableName, ok := table["TABLE_NAME"].(string)
		if !ok {
			continue
		}

		if tableName == "SAP_TISCE_DEMO_DOCUMENTCHUNK" { // Specific exclusion
			log.Printf("Skipping table %s as per exclusion rule.", tableName)
			continue
		}

		subscriptionName := "SUB_" + tableName
		virtualTableName := "VT_" + tableName

		if _, vtExists := virtualTableMap[virtualTableName]; !vtExists {
			log.Printf("Warning: Virtual table %s not found for table %s. Cannot create subscription. Skipping.", virtualTableName, tableName)
			continue
		}

		queries = append(queries, fmt.Sprintf(`TRUNCATE TABLE "%s"."%s"`, targetSchema, tableName))
		queries = append(queries, fmt.Sprintf(`CREATE REMOTE SUBSCRIPTION "%s" ON "%s"."%s" TARGET TABLE "%s"."%s"`, subscriptionName, targetSchema, virtualTableName, targetSchema, tableName))
		queries = append(queries, fmt.Sprintf(`ALTER REMOTE SUBSCRIPTION "%s" DISTRIBUTE`, subscriptionName))
		createdCount++
	}

	if len(queries) == 0 {
		log.Println("No queries were generated for creating remote subscriptions.")
		return map[string]interface{}{"status": "success", "message": "No subscriptions to create."}, nil
	}

	// Step 5: Execute the transaction.
	if err := executeInTx(credentials, queries); err != nil {
		return nil, fmt.Errorf("failed to execute transaction for creating subscriptions: %w", err)
	}

	time.Sleep(5 * time.Second)
	msg := fmt.Sprintf("Successfully created %d remote subscriptions for schema %s.", createdCount, targetSchema)
	log.Println(msg)
	return map[string]interface{}{"status": "success", "created_count": createdCount, "message": msg}, nil
}


// This main function is not used when compiling as a plugin,
// but it is useful for standalone testing of the package logic.
func main() {
	fmt.Println("This is the HANA adaptor. Compile with '-buildmode=plugin' to use.")
}
