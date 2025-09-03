package main // Must be main for a plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// --- NEW: Custom error for handling expired/invalid tokens ---
var ErrTokenInvalid = errors.New("token is invalid or unauthorized")

// --- NEW: A thread-safe, global cache for authentication tokens ---
var globalTokenCache = struct {
	sync.Mutex
	tokens map[string]string // Key: cacheKey, Value: Bearer Token
}{
	tokens: make(map[string]string),
}

const (
	// maxLoginRetries is for retrying a *single* login attempt if it fails.
	maxLoginRetries = 2
	retryWaitDuration = 3 * time.Second
	// maxOperationRetries allows retrying the entire operation if our cached token is invalid.
	maxOperationRetries = 1 // Total attempts will be 1 (initial) + 1 (retry) = 2
)

// CFPluginContext defines the expected structure from the FSM YAML for this plugin.
type CFPluginContext struct {
	Action   string `json:"action"`   // "start" or "stop"
	CFAPI    string `json:"cfAPI"`    // e.g., "https://api.cf.example.com"
	AppGUID  string `json:"appGUID"`
	Username string `json:"username"`
	Password string `json:"password"`
	Origin   string `json:"origin,omitempty"` // Optional, defaults to "sap.ids"
}

// --- NEW: Helper to create a unique key for caching tokens ---
func getCacheKey(ctx *CFPluginContext) string {
	return fmt.Sprintf("%s|%s|%s", ctx.CFAPI, ctx.Username, ctx.Origin)
}

// Helper function to parse the raw context into CFPluginContext
func parseAndValidateCFContext(rawContext map[string]interface{}) (*CFPluginContext, error) {
	var ctx CFPluginContext
	ctx.Origin = "sap.ids" // Default value
	data, err := json.Marshal(rawContext)
	if err != nil {
		return nil, fmt.Errorf("cf-plugin: failed to marshal raw context: %w", err)
	}
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("cf-plugin: failed to unmarshal context into CFPluginContext: %w", err)
	}
	// Re-apply origin if it was present but empty in the unmarshal
	if val, ok := rawContext["origin"].(string); ok && val != "" {
		ctx.Origin = val
	}
	if ctx.Action == "" || ctx.CFAPI == "" || ctx.AppGUID == "" || ctx.Username == "" || ctx.Password == "" {
		return nil, fmt.Errorf("cf-plugin: 'action', 'cfAPI', 'appGUID', 'username', and 'password' are required")
	}
	if ctx.Action != "start" && ctx.Action != "stop" {
		return nil, fmt.Errorf("cf-plugin: 'action' must be 'start' or 'stop', got '%s'", ctx.Action)
	}
	return &ctx, nil
}

// Execute is the exported function that will be called by the FSM.
func Execute(rawContext map[string]interface{}) (interface{}, error) {
	log.Println("CF Plugin: Execute called.")

	ctx, err := parseAndValidateCFContext(rawContext)
	if err != nil {
		errMessage := fmt.Sprintf("cf-plugin: context validation/parsing error: %v", err)
		log.Printf("❌ %s", errMessage)
		return map[string]interface{}{"status": "failure", "error": errMessage, "appGUID": rawContext["appGUID"]}, fmt.Errorf(errMessage)
	}

	cacheKey := getCacheKey(ctx)
	var token string
	var operationErr error
	var successMessage string

	// --- MODIFIED: Main operation loop to handle token expiration ---
	for attempt := 0; attempt <= maxOperationRetries; attempt++ {
		// Try to get a token (from cache or by logging in)
		token, err = getCachedTokenOrLogin(ctx, cacheKey)
		if err != nil {
			errMessage := fmt.Sprintf("cf-plugin: failed to get token: %v", err)
			log.Printf("❌ %s", errMessage)
			return map[string]interface{}{"status": "failure", "error": errMessage, "appGUID": ctx.AppGUID}, err
		}

		// Perform the requested action
		switch ctx.Action {
		case "start":
			log.Printf("CF Plugin: Attempting to START app %s", ctx.AppGUID)
			operationErr = startApp(ctx.CFAPI, token, ctx.AppGUID)
			if operationErr == nil {
				successMessage = fmt.Sprintf("🟢 App with GUID [%s] has been STARTED successfully.", ctx.AppGUID)
			}
		case "stop":
			log.Printf("CF Plugin: Attempting to STOP app %s", ctx.AppGUID)
			operationErr = stopApp(ctx.CFAPI, token, ctx.AppGUID)
			if operationErr == nil {
				successMessage = fmt.Sprintf("🔴 App with GUID [%s] has been STOPPED successfully.", ctx.AppGUID)
			}
		}

		// If the operation was successful, break the loop.
		if operationErr == nil {
			log.Printf("CF Plugin: %s", successMessage)
			break
		}

		// If the error is because our token is invalid, clear it from the cache and retry.
		if errors.Is(operationErr, ErrTokenInvalid) {
			log.Printf("⚠️ CF Plugin: Cached token for key '%s' is invalid. Clearing and retrying operation.", cacheKey)
			globalTokenCache.Lock()
			delete(globalTokenCache.tokens, cacheKey)
			globalTokenCache.Unlock()

			if attempt < maxOperationRetries {
				log.Println("CF Plugin: Re-attempting operation with a fresh login...")
				continue // Go to the next iteration of the loop
			} else {
				// We've already retried, so this is the final failure.
				operationErr = fmt.Errorf("operation failed after retry with new token: %w", operationErr)
			}
		}
		
		// For any other error, or if retries are exhausted, break and fail.
		break
	}

	if operationErr != nil {
		errMessage := fmt.Sprintf("cf-plugin: action '%s' failed for app '%s': %v", ctx.Action, ctx.AppGUID, operationErr)
		log.Printf("❌ %s", errMessage)
		return map[string]interface{}{
			"status": "failure", "error": errMessage, "action": ctx.Action,
			"appGUID": ctx.AppGUID, "cfAPI": ctx.CFAPI,
		}, fmt.Errorf(errMessage)
	}

	// Success
	return map[string]interface{}{
		"status": "success", "message": successMessage, "action": ctx.Action,
		"appGUID": ctx.AppGUID, "cfAPI": ctx.CFAPI,
	}, nil
}

// --- NEW: Function to manage token retrieval from cache or by logging in ---
func getCachedTokenOrLogin(ctx *CFPluginContext, cacheKey string) (string, error) {
	globalTokenCache.Lock()
	// Check if a valid token already exists in our cache.
	if token, found := globalTokenCache.tokens[cacheKey]; found {
		log.Printf("CF Plugin: Found token in cache for key '%s'. Reusing.", cacheKey)
		globalTokenCache.Unlock()
		return token, nil
	}
	// If not found, we must unlock before logging in to not block others.
	// But to prevent a thundering herd, we will relock after login to write to the cache.
	log.Printf("CF Plugin: No token in cache for key '%s'. Performing login.", cacheKey)
	globalTokenCache.Unlock()

	var token string
	var err error

	for attempt := 0; attempt <= maxLoginRetries; attempt++ {
		if attempt > 0 {
			log.Printf("CF Plugin: Retrying login... (Attempt %d of %d)", attempt, maxLoginRetries)
			time.Sleep(retryWaitDuration)
		}
		token, err = loginToCF(ctx.CFAPI, ctx.Username, ctx.Password, ctx.Origin)
		if err == nil {
			break // Success!
		}
		log.Printf("⚠️ CF Plugin: Login attempt %d/%d failed: %v", attempt+1, maxLoginRetries+1, err)
	}
	
	if err != nil {
		return "", fmt.Errorf("login failed for API '%s' after %d attempts: %w", ctx.CFAPI, maxLoginRetries+1, err)
	}
	
	log.Printf("CF Plugin: Successfully logged in to %s. Caching token.", ctx.CFAPI)

	// Lock again to safely write the new token to the cache.
	globalTokenCache.Lock()
	globalTokenCache.tokens[cacheKey] = token
	globalTokenCache.Unlock()
	
	return token, nil
}

func loginToCF(cfAPI, username, password, origin string) (string, error) {
    // This function remains unchanged.
    var authEndpoint string
    infoV3Resp, errV3 := http.Get(cfAPI + "/v3/info")
    if errV3 == nil {
        defer infoV3Resp.Body.Close()
        if infoV3Resp.StatusCode == http.StatusOK {
            var v3Info struct {
                Links struct {
                    Login struct {
                        HREF string `json:"href"`
                    } `json:"login"`
                    UAA struct {
                        HREF string `json:"href"`
                    } `json:"uaa"`
                } `json:"links"`
            }
            if err := json.NewDecoder(infoV3Resp.Body).Decode(&v3Info); err == nil {
                if v3Info.Links.Login.HREF != "" {
                    authEndpoint = v3Info.Links.Login.HREF
                } else if v3Info.Links.UAA.HREF != "" {
                    authEndpoint = v3Info.Links.UAA.HREF
                }
            }
        }
    }
    if authEndpoint == "" {
        infoV2Resp, errV2 := http.Get(cfAPI + "/v2/info")
        if errV2 != nil {
            return "", fmt.Errorf("failed to get /v2/info from %s: %w", cfAPI, errV2)
        }
        defer infoV2Resp.Body.Close()
        if infoV2Resp.StatusCode != http.StatusOK {
            bodyBytes, _ := io.ReadAll(infoV2Resp.Body)
            return "", fmt.Errorf("failed to get /v2/info from %s, status: %s, body: %s", cfAPI, infoV2Resp.Status, string(bodyBytes))
        }
        var v2Info struct {
            AuthEndpoint string `json:"authorization_endpoint"`
        }
        if err := json.NewDecoder(infoV2Resp.Body).Decode(&v2Info); err != nil {
            return "", fmt.Errorf("failed to decode /v2/info response from %s: %w", cfAPI, err)
        }
        authEndpoint = v2Info.AuthEndpoint
    }
    if authEndpoint == "" {
        return "", fmt.Errorf("could not determine authorization_endpoint from %s/v3/info or %s/v2/info", cfAPI, cfAPI)
    }
    log.Printf("CF Plugin: Determined UAA/Auth endpoint: %s", authEndpoint)
    loginHint := fmt.Sprintf(`{"origin":"%s"}`, origin)
    form := url.Values{}
    form.Add("grant_type", "password")
    form.Add("username", username)
    form.Add("password", password)
    form.Add("response_type", "token")
    form.Add("login_hint", loginHint)
    tokenURL := authEndpoint + "/oauth/token"
    if !strings.HasSuffix(authEndpoint, "/oauth/token") && strings.Contains(authEndpoint, "uaa") {
        parsedAuthURL, _ := url.Parse(authEndpoint)
        if parsedAuthURL != nil && !strings.HasSuffix(parsedAuthURL.Path, "/oauth/token") {
            tokenURL = strings.TrimSuffix(authEndpoint, "/") + "/oauth/token"
        } else if parsedAuthURL == nil {
            tokenURL = strings.TrimSuffix(authEndpoint, "/") + "/oauth/token"
        }
    } else if strings.HasSuffix(authEndpoint, "/oauth/token") {
        tokenURL = authEndpoint
    }
    log.Printf("CF Plugin: Attempting POST to token URL: %s", tokenURL)
    req, err := http.NewRequest("POST", tokenURL, strings.NewReader(form.Encode()))
    if err != nil {
        return "", fmt.Errorf("failed to create login request: %w", err)
    }
    req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
    req.Header.Add("Accept", "application/json")
    req.SetBasicAuth("cf", "")
    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return "", fmt.Errorf("login request failed: %w", err)
    }
    defer resp.Body.Close()
    bodyBytes, _ := io.ReadAll(resp.Body)
    resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
    if resp.StatusCode != http.StatusOK {
        var errorBody map[string]interface{}
        if err := json.Unmarshal(bodyBytes, &errorBody); err == nil {
            return "", fmt.Errorf("login failed (status %s): %v", resp.Status, errorBody)
        }
        return "", fmt.Errorf("login failed (status %s), body: %s", resp.Status, string(bodyBytes))
    }
    var result struct {
        AccessToken string `json:"access_token"`
        TokenType   string `json:"token_type"`
        ExpiresIn   int    `json:"expires_in"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", fmt.Errorf("failed to decode login response: %w, body: %s", err, string(bodyBytes))
    }
    if result.AccessToken == "" {
        return "", fmt.Errorf("access_token not found in login response, body: %s", string(bodyBytes))
    }
    return result.AccessToken, nil
}

// --- MODIFIED: startApp and stopApp now check for 401 Unauthorized ---
func startApp(cfAPI, token, appGUID string) error {
	path := fmt.Sprintf("/v3/apps/%s/actions/start", appGUID)
	resp, err := makeCFAPIRequest("POST", cfAPI, path, token, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrTokenInvalid // Signal that the token was rejected
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		log.Printf("CF Plugin: Start action for app %s returned %s", appGUID, resp.Status)
		return nil
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	var errorResponse map[string]interface{}
	json.Unmarshal(bodyBytes, &errorResponse)
	return fmt.Errorf("start app %s failed (status %s): %v", appGUID, resp.Status, errorResponse)
}

func stopApp(cfAPI, token, appGUID string) error {
	path := fmt.Sprintf("/v3/apps/%s/actions/stop", appGUID)
	resp, err := makeCFAPIRequest("POST", cfAPI, path, token, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrTokenInvalid // Signal that the token was rejected
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		log.Printf("CF Plugin: Stop action for app %s returned %s", appGUID, resp.Status)
		return nil
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	var errorResponse map[string]interface{}
	json.Unmarshal(bodyBytes, &errorResponse)
	return fmt.Errorf("stop app %s failed (status %s): %v", appGUID, resp.Status, errorResponse)
}

func makeCFAPIRequest(method, cfAPI, path, token string, body io.Reader) (*http.Response, error) {
    // This function remains unchanged.
    fullURL := fmt.Sprintf("%s%s", strings.TrimSuffix(cfAPI, "/"), path)
    req, err := http.NewRequest(method, fullURL, body)
    if err != nil {
        return nil, fmt.Errorf("failed to create CF API request for %s: %w", fullURL, err)
    }
    req.Header.Add("Authorization", "Bearer "+token)
    req.Header.Add("Accept", "application/json")
    if body != nil {
        req.Header.Add("Content-Type", "application/json")
    }
    client := &http.Client{Timeout: 60 * time.Second}
    return client.Do(req)
}

func main() {
	fmt.Println("CF Plugin - for testing purposes.")
}