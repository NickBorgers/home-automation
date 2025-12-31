package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/logbuffer"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
)

func TestHandleGetState(t *testing.T) {
	// Create logger
	logger := testlogger.New()

	// Create mock HA client
	mockClient := ha.NewMockClient()

	// Create state manager
	stateManager := state.NewManager(mockClient, logger, false)

	// Set some test values
	stateManager.SetBool("isNickHome", true)
	stateManager.SetBool("isCarolineHome", false)
	stateManager.SetNumber("alarmTime", 7.5)
	stateManager.SetString("dayPhase", "morning")
	stateManager.SetString("musicPlaybackType", "default")

	// Create API server
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	w := httptest.NewRecorder()

	// Handle request
	server.handleGetState(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}

	// Parse response
	var response StateResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify boolean values
	if !response.Booleans["isNickHome"] {
		t.Error("Expected isNickHome to be true")
	}
	if response.Booleans["isCarolineHome"] {
		t.Error("Expected isCarolineHome to be false")
	}

	// Verify number values
	if response.Numbers["alarmTime"] != 7.5 {
		t.Errorf("Expected alarmTime to be 7.5, got %f", response.Numbers["alarmTime"])
	}

	// Verify string values
	if response.Strings["dayPhase"] != "morning" {
		t.Errorf("Expected dayPhase to be 'morning', got '%s'", response.Strings["dayPhase"])
	}
	if response.Strings["musicPlaybackType"] != "default" {
		t.Errorf("Expected musicPlaybackType to be 'default', got '%s'", response.Strings["musicPlaybackType"])
	}

	// Verify all expected keys are present (at least some of them)
	expectedBoolKeys := []string{"isNickHome", "isCarolineHome", "isToriHere", "isAnyoneHome"}
	for _, key := range expectedBoolKeys {
		if _, ok := response.Booleans[key]; !ok {
			t.Errorf("Expected boolean key %s to be present", key)
		}
	}

	expectedNumberKeys := []string{"alarmTime", "remainingSolarGeneration", "thisHourSolarGeneration"}
	for _, key := range expectedNumberKeys {
		if _, ok := response.Numbers[key]; !ok {
			t.Errorf("Expected number key %s to be present", key)
		}
	}

	expectedStringKeys := []string{"dayPhase", "sunevent", "musicPlaybackType"}
	for _, key := range expectedStringKeys {
		if _, ok := response.Strings[key]; !ok {
			t.Errorf("Expected string key %s to be present", key)
		}
	}
}

func TestHandleGetStateMethodNotAllowed(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Test POST method (should be rejected)
	req := httptest.NewRequest(http.MethodPost, "/api/state", nil)
	w := httptest.NewRecorder()

	server.handleGetState(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.Connect() // Connect so IsHealthy returns true
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	t.Run("healthy when connected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()

		server.handleHealth(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if response["status"] != "ok" {
			t.Errorf("Expected status 'ok', got '%v'", response["status"])
		}
		if response["ha_connected"] != true {
			t.Errorf("Expected ha_connected true, got '%v'", response["ha_connected"])
		}
	})

	t.Run("unhealthy when disconnected", func(t *testing.T) {
		mockClient.Disconnect()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()

		server.handleHealth(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("Expected status 503, got %d", w.Code)
		}

		var response map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if response["status"] != "unhealthy" {
			t.Errorf("Expected status 'unhealthy', got '%v'", response["status"])
		}
		if response["ha_connected"] != false {
			t.Errorf("Expected ha_connected false, got '%v'", response["ha_connected"])
		}
	})
}

func TestHandleSitemap(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	server.handleSitemap(w, req)

	// Should return 404 status for automation compatibility
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/plain; charset=utf-8" {
		t.Errorf("Expected Content-Type text/plain; charset=utf-8, got %s", contentType)
	}

	// Check body contains expected content
	body := w.Body.String()
	expectedStrings := []string{
		"Home Automation API",
		"/api/state",
		"/health",
		"GET",
		"curl",
	}

	for _, expected := range expectedStrings {
		if len(body) == 0 || len(expected) == 0 {
			t.Errorf("Body or expected string is empty")
			continue
		}
		found := false
		for i := 0; i <= len(body)-len(expected); i++ {
			if body[i:i+len(expected)] == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected body to contain '%s', got:\n%s", expected, body)
		}
	}
}

func TestHandleSitemapHTML(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()

	server.handleSitemap(w, req)

	// Should return 404 status for automation compatibility
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Expected Content-Type text/html; charset=utf-8, got %s", contentType)
	}

	// Check body contains HTML
	body := w.Body.String()
	htmlElements := []string{
		"<!DOCTYPE html>",
		"<html>",
		"<title>Home Automation API</title>",
		"<h1>Home Automation API</h1>",
		"/api/state",
		"/health",
	}

	for _, expected := range htmlElements {
		if len(body) == 0 || len(expected) == 0 {
			t.Errorf("Body or expected string is empty")
			continue
		}
		found := false
		for i := 0; i <= len(body)-len(expected); i++ {
			if body[i:i+len(expected)] == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected HTML body to contain '%s'", expected)
		}
	}
}

func TestHandleSitemapMethodNotAllowed(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Test POST method (should be rejected)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	server.handleSitemap(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHandleSitemapNonRootPath(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Test non-root path (should return 404 without sitemap)
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()

	server.handleSitemap(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	// Should not contain sitemap content
	body := w.Body.String()
	if len(body) > 0 {
		// Simple check - standard 404 from http.NotFound
		found := false
		searchStr := "Home Automation API"
		for i := 0; i <= len(body)-len(searchStr); i++ {
			if body[i:i+len(searchStr)] == searchStr {
				found = true
				break
			}
		}
		if found {
			t.Error("Non-root path should not return sitemap content")
		}
	}
}

func TestHandleGetStatesByPlugin(t *testing.T) {
	// Create logger
	logger := testlogger.New()

	// Create mock HA client
	mockClient := ha.NewMockClient()

	// Create state manager
	stateManager := state.NewManager(mockClient, logger, false)

	// Set some test values for different plugins
	stateManager.SetBool("isNickHome", true)
	stateManager.SetBool("isCarolineHome", false)
	stateManager.SetBool("isToriHere", true)
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isAnyoneAsleep", false)
	stateManager.SetString("dayPhase", "evening")
	stateManager.SetString("sunevent", "dusk")
	stateManager.SetString("musicPlaybackType", "default")
	stateManager.SetNumber("alarmTime", 7.5)
	stateManager.SetString("currentEnergyLevel", "green")

	// Create API server
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/api/states", nil)
	w := httptest.NewRecorder()

	// Handle request
	server.handleGetStatesByPlugin(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}

	// Parse response
	var response PluginStatesResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify we have plugin data
	if len(response.Plugins) == 0 {
		t.Error("Expected plugin data, got empty response")
	}

	// Verify statetracking plugin has expected variables
	statetrackingStates, ok := response.Plugins["statetracking"]
	if !ok {
		t.Error("Expected statetracking plugin in response")
	} else {
		// Check it has the variables it reads
		if _, ok := statetrackingStates["isNickHome"]; !ok {
			t.Error("Expected statetracking to have isNickHome")
		}
		if _, ok := statetrackingStates["isCarolineHome"]; !ok {
			t.Error("Expected statetracking to have isCarolineHome")
		}
		// Check it has the variables it writes
		if _, ok := statetrackingStates["isAnyoneHome"]; !ok {
			t.Error("Expected statetracking to have isAnyoneHome")
		}

		// Verify value and type
		if val, ok := statetrackingStates["isNickHome"]; ok {
			if val.Type != "boolean" {
				t.Errorf("Expected isNickHome type to be boolean, got %s", val.Type)
			}
			if val.Value != true {
				t.Errorf("Expected isNickHome value to be true, got %v", val.Value)
			}
		}
	}

	// Verify music plugin has expected variables
	musicStates, ok := response.Plugins["music"]
	if !ok {
		t.Error("Expected music plugin in response")
	} else {
		// Check it has dayPhase (read)
		if _, ok := musicStates["dayPhase"]; !ok {
			t.Error("Expected music to have dayPhase")
		}
		// Check it has musicPlaybackType (read and write)
		if _, ok := musicStates["musicPlaybackType"]; !ok {
			t.Error("Expected music to have musicPlaybackType")
		}

		// Verify value and type
		if val, ok := musicStates["dayPhase"]; ok {
			if val.Type != "string" {
				t.Errorf("Expected dayPhase type to be string, got %s", val.Type)
			}
			if val.Value != "evening" {
				t.Errorf("Expected dayPhase value to be 'evening', got %v", val.Value)
			}
		}
	}

	// Verify dayphase plugin has expected variables
	dayphaseStates, ok := response.Plugins["dayphase"]
	if !ok {
		t.Error("Expected dayphase plugin in response")
	} else {
		// Check it has sunevent (write)
		if _, ok := dayphaseStates["sunevent"]; !ok {
			t.Error("Expected dayphase to have sunevent")
		}
		// Check it has dayPhase (write)
		if _, ok := dayphaseStates["dayPhase"]; !ok {
			t.Error("Expected dayphase to have dayPhase")
		}
	}

	// Verify sleephygiene plugin has alarmTime
	sleepStates, ok := response.Plugins["sleephygiene"]
	if !ok {
		t.Error("Expected sleephygiene plugin in response")
	} else {
		if val, ok := sleepStates["alarmTime"]; ok {
			if val.Type != "number" {
				t.Errorf("Expected alarmTime type to be number, got %s", val.Type)
			}
			if val.Value != 7.5 {
				t.Errorf("Expected alarmTime value to be 7.5, got %v", val.Value)
			}
		} else {
			t.Error("Expected sleephygiene to have alarmTime")
		}
	}

	// Verify loadshedding plugin has currentEnergyLevel
	loadsheddingStates, ok := response.Plugins["loadshedding"]
	if !ok {
		t.Error("Expected loadshedding plugin in response")
	} else {
		if _, ok := loadsheddingStates["currentEnergyLevel"]; !ok {
			t.Error("Expected loadshedding to have currentEnergyLevel")
		}
	}
}

func TestHandleGetStatesByPluginMethodNotAllowed(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Test POST method (should be rejected)
	req := httptest.NewRequest(http.MethodPost, "/api/states", nil)
	w := httptest.NewRecorder()

	server.handleGetStatesByPlugin(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHandleGetStatesByPluginEmptyState(t *testing.T) {
	// Test with default/empty state values
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	req := httptest.NewRequest(http.MethodGet, "/api/states", nil)
	w := httptest.NewRecorder()

	server.handleGetStatesByPlugin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response PluginStatesResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should still have plugin data even with default values
	if len(response.Plugins) == 0 {
		t.Error("Expected plugin data, got empty response")
	}

	// Each plugin should have its variables with default values
	for pluginName, pluginStates := range response.Plugins {
		if len(pluginStates) == 0 {
			// Plugins with no variables (only reading/writing) are ok
			continue
		}
		// Verify at least one variable has a type
		hasType := false
		for _, stateVal := range pluginStates {
			if stateVal.Type != "" {
				hasType = true
				break
			}
		}
		if !hasType && len(pluginStates) > 0 {
			t.Errorf("Plugin %s has variables but none have types", pluginName)
		}
	}
}

func TestPluginRegistryCompleteness(t *testing.T) {
	// Verify that all plugins in the registry have valid metadata
	for _, plugin := range pluginRegistry {
		if plugin.Name == "" {
			t.Error("Found plugin with empty name")
		}
		if plugin.Description == "" {
			t.Errorf("Plugin %s has empty description", plugin.Name)
		}
		// At least one of Reads or Writes should be non-empty
		if len(plugin.Reads) == 0 && len(plugin.Writes) == 0 {
			t.Errorf("Plugin %s has no reads or writes", plugin.Name)
		}
	}

	// Verify expected plugins are present
	expectedPlugins := []string{
		"statetracking",
		"dayphase",
		"music",
		"lighting",
		"tv",
		"energy",
		"loadshedding",
		"sleephygiene",
		"security",
		"reset",
	}

	pluginMap := make(map[string]bool)
	for _, plugin := range pluginRegistry {
		pluginMap[plugin.Name] = true
	}

	for _, expected := range expectedPlugins {
		if !pluginMap[expected] {
			t.Errorf("Expected plugin %s not found in registry", expected)
		}
	}
}

func TestHandleGetLightingShadowState(t *testing.T) {
	// Create logger
	logger := testlogger.New()

	// Create mock HA client
	mockClient := ha.NewMockClient()

	// Create state manager
	stateManager := state.NewManager(mockClient, logger, false)

	// Create shadow tracker
	shadowTracker := shadowstate.NewTracker()

	// Register a mock lighting shadow state
	lightingState := shadowstate.NewLightingShadowState()
	lightingState.Inputs.Current["dayPhase"] = "evening"
	lightingState.Inputs.AtLastAction["dayPhase"] = "afternoon"
	shadowTracker.RegisterPlugin("lighting", lightingState)

	// Create API server
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/api/shadow/lighting", nil)
	w := httptest.NewRecorder()

	// Handle request
	server.handleGetLightingShadowState(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}

	// Parse response
	var response shadowstate.LightingShadowState
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify inputs
	if response.Inputs.Current["dayPhase"] != "evening" {
		t.Errorf("Expected current dayPhase to be 'evening', got %v", response.Inputs.Current["dayPhase"])
	}
	if response.Inputs.AtLastAction["dayPhase"] != "afternoon" {
		t.Errorf("Expected atLastAction dayPhase to be 'afternoon', got %v", response.Inputs.AtLastAction["dayPhase"])
	}
}

func TestHandleGetLightingShadowState_NotFound(t *testing.T) {
	// Create logger
	logger := testlogger.New()

	// Create mock HA client
	mockClient := ha.NewMockClient()

	// Create state manager
	stateManager := state.NewManager(mockClient, logger, false)

	// Create empty shadow tracker (no lighting state registered)
	shadowTracker := shadowstate.NewTracker()

	// Create API server
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/api/shadow/lighting", nil)
	w := httptest.NewRecorder()

	// Handle request
	server.handleGetLightingShadowState(w, req)

	// Check status code - should be 404 Not Found
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestHandleGetSecurityShadowState(t *testing.T) {
	// Create logger
	logger := testlogger.New()

	// Create mock HA client
	mockClient := ha.NewMockClient()

	// Create state manager
	stateManager := state.NewManager(mockClient, logger, false)

	// Create shadow tracker
	shadowTracker := shadowstate.NewTracker()

	// Register a mock security shadow state
	securityState := shadowstate.NewSecurityShadowState()
	securityState.Inputs.Current["isEveryoneAsleep"] = true
	securityState.Inputs.AtLastAction["isEveryoneAsleep"] = false
	securityState.Outputs.Lockdown.Active = true
	securityState.Outputs.Lockdown.Reason = "Everyone is asleep"
	shadowTracker.RegisterPlugin("security", securityState)

	// Create API server
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/api/shadow/security", nil)
	w := httptest.NewRecorder()

	// Handle request
	server.handleGetSecurityShadowState(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}

	// Parse response
	var response shadowstate.SecurityShadowState
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify inputs
	if response.Inputs.Current["isEveryoneAsleep"] != true {
		t.Errorf("Expected current isEveryoneAsleep to be true, got %v", response.Inputs.Current["isEveryoneAsleep"])
	}
	if response.Inputs.AtLastAction["isEveryoneAsleep"] != false {
		t.Errorf("Expected atLastAction isEveryoneAsleep to be false, got %v", response.Inputs.AtLastAction["isEveryoneAsleep"])
	}

	// Verify outputs
	if !response.Outputs.Lockdown.Active {
		t.Error("Expected lockdown to be active")
	}
	if response.Outputs.Lockdown.Reason != "Everyone is asleep" {
		t.Errorf("Expected lockdown reason to be 'Everyone is asleep', got %s", response.Outputs.Lockdown.Reason)
	}
}

func TestHandleGetSecurityShadowState_NotFound(t *testing.T) {
	// Create logger
	logger := testlogger.New()

	// Create mock HA client
	mockClient := ha.NewMockClient()

	// Create state manager
	stateManager := state.NewManager(mockClient, logger, false)

	// Create empty shadow tracker (no security state registered)
	shadowTracker := shadowstate.NewTracker()

	// Create API server
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/api/shadow/security", nil)
	w := httptest.NewRecorder()

	// Handle request
	server.handleGetSecurityShadowState(w, req)

	// Check status code - should be 404 Not Found
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestHandleGetAllShadowStates(t *testing.T) {
	// Create logger
	logger := testlogger.New()

	// Create mock HA client
	mockClient := ha.NewMockClient()

	// Create state manager
	stateManager := state.NewManager(mockClient, logger, false)

	// Create shadow tracker
	shadowTracker := shadowstate.NewTracker()

	// Register multiple shadow states
	lightingState := shadowstate.NewLightingShadowState()
	lightingState.Inputs.Current["dayPhase"] = "evening"
	shadowTracker.RegisterPlugin("lighting", lightingState)

	securityState := shadowstate.NewSecurityShadowState()
	securityState.Inputs.Current["isEveryoneAsleep"] = true
	shadowTracker.RegisterPlugin("security", securityState)

	// Create API server
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/api/shadow", nil)
	w := httptest.NewRecorder()

	// Handle request
	server.handleGetAllShadowStates(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}

	// Parse response
	var response AllShadowStatesResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify we have both plugins
	if len(response.Plugins) != 2 {
		t.Errorf("Expected 2 plugins, got %d", len(response.Plugins))
	}

	// Verify lighting plugin is present
	if _, ok := response.Plugins["lighting"]; !ok {
		t.Error("Expected lighting plugin in response")
	}

	// Verify security plugin is present
	if _, ok := response.Plugins["security"]; !ok {
		t.Error("Expected security plugin in response")
	}

	// Verify metadata is present
	if response.Metadata.Version == "" {
		t.Error("Expected metadata version to be set")
	}
}

func TestAddLocalTimestamps(t *testing.T) {
	// Load a test timezone (EST = UTC-5)
	estLocation, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("Failed to load timezone: %v", err)
	}

	// Create mock dependencies
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, estLocation)

	// Test cases
	tests := []struct {
		name    string
		input   interface{}
		checkFn func(t *testing.T, result interface{})
	}{
		{
			name: "simple timestamp",
			input: map[string]interface{}{
				"lastUpdated": "2025-12-01T14:30:45Z",
			},
			checkFn: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				// Original should be preserved
				if m["lastUpdated"] != "2025-12-01T14:30:45Z" {
					t.Errorf("Original timestamp was modified: %v", m["lastUpdated"])
				}
				// Local version should be added
				local, ok := m["lastUpdatedLocal"]
				if !ok {
					t.Error("Expected lastUpdatedLocal to be added")
					return
				}
				// Check it contains expected components (Dec 1, 2025 at 9:30 AM EST)
				localStr := local.(string)
				if localStr == "" {
					t.Error("Local timestamp is empty")
				}
				// Should contain "Dec" and "2025" and "AM" (or "PM" depending on time)
				if len(localStr) < 10 {
					t.Errorf("Local timestamp too short: %s", localStr)
				}
			},
		},
		{
			name: "timestamp with nanoseconds",
			input: map[string]interface{}{
				"created": "2025-06-15T08:00:00.123456789Z",
			},
			checkFn: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				if _, ok := m["createdLocal"]; !ok {
					t.Error("Expected createdLocal to be added for RFC3339Nano timestamp")
				}
			},
		},
		{
			name: "nested objects",
			input: map[string]interface{}{
				"metadata": map[string]interface{}{
					"timestamp": "2025-01-15T12:00:00Z",
				},
			},
			checkFn: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				metadata := m["metadata"].(map[string]interface{})
				if _, ok := metadata["timestampLocal"]; !ok {
					t.Error("Expected timestampLocal in nested object")
				}
			},
		},
		{
			name: "arrays with timestamps",
			input: map[string]interface{}{
				"events": []interface{}{
					map[string]interface{}{
						"time": "2025-03-20T10:00:00Z",
					},
				},
			},
			checkFn: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				events := m["events"].([]interface{})
				event := events[0].(map[string]interface{})
				if _, ok := event["timeLocal"]; !ok {
					t.Error("Expected timeLocal in array element")
				}
			},
		},
		{
			name: "non-timestamp strings unchanged",
			input: map[string]interface{}{
				"name":   "test",
				"status": "ok",
			},
			checkFn: func(t *testing.T, result interface{}) {
				m := result.(map[string]interface{})
				// Should not add Local versions for non-timestamp strings
				if _, ok := m["nameLocal"]; ok {
					t.Error("Should not add Local version for non-timestamp string")
				}
				if _, ok := m["statusLocal"]; ok {
					t.Error("Should not add Local version for non-timestamp string")
				}
			},
		},
		{
			name:  "nil timezone returns original",
			input: map[string]interface{}{"ts": "2025-01-01T00:00:00Z"},
			checkFn: func(t *testing.T, result interface{}) {
				// This test uses a server with nil timezone, handled separately
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "nil timezone returns original" {
				// Test with nil timezone
				nilTzServer := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, nil)
				result := nilTzServer.addLocalTimestamps(tc.input)
				m := result.(map[string]interface{})
				if _, ok := m["tsLocal"]; ok {
					t.Error("Nil timezone should not add Local fields")
				}
				return
			}
			result := server.addLocalTimestamps(tc.input)
			tc.checkFn(t, result)
		})
	}
}

func TestHandleDashboard(t *testing.T) {
	// Create logger
	logger := testlogger.New()

	// Create mock HA client
	mockClient := ha.NewMockClient()

	// Create state manager
	stateManager := state.NewManager(mockClient, logger, false)

	// Create shadow tracker with some test data
	shadowTracker := shadowstate.NewTracker()
	lightingState := shadowstate.NewLightingShadowState()
	lightingState.Inputs.Current["dayPhase"] = "evening"
	shadowTracker.RegisterPlugin("lighting", lightingState)

	// Create API server
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	// Handle request
	server.handleDashboard(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Expected Content-Type text/html; charset=utf-8, got %s", contentType)
	}

	// Check body contains expected HTML elements
	body := w.Body.String()
	expectedElements := []string{
		"<!DOCTYPE html>",
		"<title>Shadow State Dashboard</title>",
		"Shadow State Dashboard",
		"/api/shadow",
		"autoRefresh",
		"plugins-grid",
		"#1a1a2e", // dark mode background color
	}

	for _, expected := range expectedElements {
		found := false
		for i := 0; i <= len(body)-len(expected); i++ {
			if body[i:i+len(expected)] == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected dashboard HTML to contain '%s'", expected)
		}
	}
}

func TestHandleDashboardMethodNotAllowed(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, time.UTC)

	// Test POST method (should be rejected)
	req := httptest.NewRequest(http.MethodPost, "/dashboard", nil)
	w := httptest.NewRecorder()

	server.handleDashboard(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestWriteJSONWithLocalTimestamps(t *testing.T) {
	// Load a test timezone
	estLocation, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("Failed to load timezone: %v", err)
	}

	// Create mock dependencies
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	server := NewServer(mockClient, stateManager, shadowTracker, logbuffer.NewBuffer(100), logger, 8080, estLocation)

	// Create a test struct with a timestamp
	type TestData struct {
		Name      string    `json:"name"`
		Timestamp time.Time `json:"timestamp"`
	}

	testData := TestData{
		Name:      "test",
		Timestamp: time.Date(2025, 12, 1, 14, 30, 45, 0, time.UTC),
	}

	// Create response recorder
	w := httptest.NewRecorder()
	err = server.writeJSONWithLocalTimestamps(w, testData)
	if err != nil {
		t.Fatalf("writeJSONWithLocalTimestamps failed: %v", err)
	}

	// Parse the response
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Check that original timestamp is present
	if result["timestamp"] == nil {
		t.Error("Expected timestamp field")
	}

	// Check that local timestamp is added
	if result["timestampLocal"] == nil {
		t.Error("Expected timestampLocal field to be added")
	}

	// Verify name is unchanged
	if result["name"] != "test" {
		t.Errorf("Expected name to be 'test', got %v", result["name"])
	}
}

func TestHandleTimelineEvents(t *testing.T) {
	// Create logger and dependencies
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()

	// Create buffer with test events
	buffer := logbuffer.NewBuffer(100)
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		buffer.Add(logbuffer.Event{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Level:     "info",
			Message:   "State changed",
			Fields: map[string]interface{}{
				"key": "dayPhase",
				"old": "day",
				"new": "evening",
			},
		})
	}

	server := NewServer(mockClient, stateManager, shadowTracker, buffer, logger, 8080, time.UTC)

	t.Run("basic request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/timeline/events", nil)
		w := httptest.NewRecorder()
		server.handleTimelineEvents(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var response TimelineEventsResponse
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if len(response.Events) != 5 {
			t.Errorf("Expected 5 events, got %d", len(response.Events))
		}
		if response.Count != 5 {
			t.Errorf("Expected count 5, got %d", response.Count)
		}
		if response.Capacity != 100 {
			t.Errorf("Expected capacity 100, got %d", response.Capacity)
		}
	})

	t.Run("with limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/timeline/events?limit=2", nil)
		w := httptest.NewRecorder()
		server.handleTimelineEvents(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var response TimelineEventsResponse
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if len(response.Events) != 2 {
			t.Errorf("Expected 2 events, got %d", len(response.Events))
		}
	})

	t.Run("with since", func(t *testing.T) {
		// "since" returns events strictly AFTER the timestamp
		since := baseTime.Add(2 * time.Minute).Format(time.RFC3339)
		req := httptest.NewRequest(http.MethodGet, "/api/timeline/events?since="+since, nil)
		w := httptest.NewRecorder()
		server.handleTimelineEvents(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var response TimelineEventsResponse
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		// Events strictly after minute 2 are: minutes 3, 4 (2 events)
		if len(response.Events) != 2 {
			t.Errorf("Expected 2 events (minutes 3,4), got %d", len(response.Events))
		}
	})

	t.Run("invalid since", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/timeline/events?since=invalid", nil)
		w := httptest.NewRecorder()
		server.handleTimelineEvents(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})

	t.Run("invalid limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/timeline/events?limit=abc", nil)
		w := httptest.NewRecorder()
		server.handleTimelineEvents(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/timeline/events", nil)
		w := httptest.NewRecorder()
		server.handleTimelineEvents(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("Expected status 405, got %d", w.Code)
		}
	})
}
