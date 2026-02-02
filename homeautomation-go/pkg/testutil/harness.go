// Package testutil provides testing utilities for home automation plugins.
// This file provides a TestEnv for integration testing external plugins.
package testutil

import (
	"context"
	"fmt"

	"homeautomation/internal/ha"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
	pkgha "homeautomation/pkg/ha"
	pkgstate "homeautomation/pkg/state"

	"go.uber.org/zap"
)

// TestEnv provides a complete test environment for plugin integration tests.
// It creates real internal implementations but exposes them via pkg interfaces,
// allowing external modules to write integration tests without importing internal packages.
type TestEnv struct {
	// Public fields - exposed via pkg interfaces
	Server       *MockHAServer
	HAClient     pkgha.Client
	StateManager pkgstate.Manager
	Logger       *zap.Logger

	// Internal references for cleanup and advanced usage
	internalClient       *ha.Client
	internalStateManager *state.Manager
	stateTracking        *statetracking.Manager
}

// NewTestEnv creates a fully configured test environment with mock HA server,
// connected client, and synced state manager.
//
// Example usage:
//
//	env, err := testutil.NewTestEnv("localhost:8123", "test_token")
//	if err != nil {
//	    t.Fatal(err)
//	}
//	defer env.Cleanup()
//
//	// Use env.HAClient and env.StateManager in your plugin tests
func NewTestEnv(addr, token string) (*TestEnv, error) {
	logger := testlogger.New()

	// Start mock HA server
	server := NewMockHAServer(addr, token)
	if err := server.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mock server: %w", err)
	}

	// Create and connect client
	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", addr), token, logger)
	if err := client.Connect(); err != nil {
		server.Stop()
		return nil, fmt.Errorf("failed to connect client: %w", err)
	}

	// Create state manager and sync
	stateManager := state.NewManager(client, logger, false)
	if err := stateManager.SyncFromHA(); err != nil {
		client.Disconnect()
		server.Stop()
		return nil, fmt.Errorf("failed to sync state: %w", err)
	}

	return &TestEnv{
		Server:               server,
		HAClient:             pkgha.WrapClient(client),
		StateManager:         pkgstate.WrapManager(stateManager),
		Logger:               logger,
		internalClient:       client,
		internalStateManager: stateManager,
	}, nil
}

// StartStateTracking starts the state tracking plugin, which is a common
// dependency for other plugins that need computed state variables like
// isAnyoneHome, isEveryoneAsleep, etc.
func (e *TestEnv) StartStateTracking() error {
	e.stateTracking = statetracking.NewManager(context.Background(), e.internalClient, e.internalStateManager, e.Logger, false, nil)
	return e.stateTracking.Start()
}

// InitializeSecurityStates sets up common initial states for security plugin testing.
// This includes lockdown, doorbell, garage door, and presence states.
func (e *TestEnv) InitializeSecurityStates() {
	e.Server.InitializeStates()

	// Security-specific states
	e.Server.SetState("input_boolean.lockdown", "off", nil)
	e.Server.SetState("input_boolean.expecting_someone", "off", nil)
	e.Server.SetState("input_button.doorbell", "", nil)
	e.Server.SetState("input_button.vehicle_arriving", "", nil)
	e.Server.SetState("cover.garage_door_door", "closed", nil)
	e.Server.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)

	// Presence and sleep states (used by State Tracking plugin)
	e.Server.SetState("input_boolean.nick_home", "off", nil)
	e.Server.SetState("input_boolean.caroline_home", "off", nil)
	e.Server.SetState("input_boolean.tori_here", "off", nil)
	e.Server.SetState("input_boolean.master_asleep", "off", nil)
	e.Server.SetState("input_boolean.guest_asleep", "off", nil)
	e.Server.SetState("input_boolean.have_guests", "off", nil)
}

// Cleanup stops all components in the correct order.
// Always call this in a defer after creating the TestEnv.
func (e *TestEnv) Cleanup() {
	if e.stateTracking != nil {
		e.stateTracking.Stop()
	}
	if e.internalClient != nil {
		e.internalClient.Disconnect()
	}
	if e.Server != nil {
		e.Server.Stop()
	}
}

// GetServiceCalls returns all service calls made to the mock server.
// Useful for asserting that plugins made expected HA service calls.
func (e *TestEnv) GetServiceCalls() []ServiceCall {
	return e.Server.GetServiceCalls()
}

// ClearServiceCalls clears the recorded service calls.
func (e *TestEnv) ClearServiceCalls() {
	e.Server.ClearServiceCalls()
}
