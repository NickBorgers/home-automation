// Package testutil provides testing utilities for home automation plugins.
// This file provides shared test setup helpers to reduce boilerplate across plugin tests.
package testutil

import (
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"go.uber.org/zap"
)

// Env bundles the common test dependencies used by nearly every plugin test.
// It replaces the repeated 3-line setup pattern:
//
//	logger := testlogger.New()
//	mockClient := ha.NewMockClient()
//	stateManager := state.NewManager(mockClient, logger, false)
type Env struct {
	Logger   *zap.Logger
	MockHA   *ha.MockClient
	StateMgr *state.Manager
}

// NewEnv creates a standard test environment with logger, mock HA client, and state manager.
func NewEnv(_ *testing.T) *Env {
	logger := testlogger.New()
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, logger, false)
	return &Env{
		Logger:   logger,
		MockHA:   mockHA,
		StateMgr: stateMgr,
	}
}

// NewEnvWithStates creates a test environment and initializes state variables.
// The states map keys are state variable names (e.g., "isAnyoneHome") and values
// are set via the appropriate setter based on type:
//   - bool: SetBool
//   - string: SetString
//   - float64: SetFloat
func NewEnvWithStates(t *testing.T, states map[string]interface{}) *Env {
	t.Helper()
	env := NewEnv(t)
	for key, value := range states {
		switch v := value.(type) {
		case bool:
			if err := env.StateMgr.SetBool(key, v); err != nil {
				t.Fatalf("Failed to set bool state %q: %v", key, err)
			}
		case string:
			if err := env.StateMgr.SetString(key, v); err != nil {
				t.Fatalf("Failed to set string state %q: %v", key, err)
			}
		case float64:
			if err := env.StateMgr.SetNumber(key, v); err != nil {
				t.Fatalf("Failed to set number state %q: %v", key, err)
			}
		default:
			t.Fatalf("Unsupported state type for %q: %T", key, value)
		}
	}
	return env
}

// AssertServiceCall asserts that at least one service call matching domain and service exists.
// Returns the first matching call for further inspection.
func AssertServiceCall(t *testing.T, calls []ha.ServiceCall, domain, service string) ha.ServiceCall {
	t.Helper()
	for _, call := range calls {
		if call.Domain == domain && call.Service == service {
			return call
		}
	}
	t.Errorf("Expected service call %s.%s not found in %d calls", domain, service, len(calls))
	return ha.ServiceCall{}
}

// AssertNoServiceCall asserts that no service call matching domain and service exists.
func AssertNoServiceCall(t *testing.T, calls []ha.ServiceCall, domain, service string) {
	t.Helper()
	for _, call := range calls {
		if call.Domain == domain && call.Service == service {
			t.Errorf("Unexpected service call %s.%s found", domain, service)
			return
		}
	}
}

// AssertServiceCallWithEntity asserts that a service call was made for a specific entity.
// The entityID is expected in call.Data["entity_id"] as a string.
func AssertServiceCallWithEntity(t *testing.T, calls []ha.ServiceCall, domain, service, entityID string) ha.ServiceCall {
	t.Helper()
	for _, call := range calls {
		if call.Domain == domain && call.Service == service {
			if eid, ok := call.Data["entity_id"].(string); ok && eid == entityID {
				return call
			}
		}
	}
	t.Errorf("Expected service call %s.%s for entity %s not found in %d calls", domain, service, entityID, len(calls))
	return ha.ServiceCall{}
}

// FindServiceCall finds the first service call matching domain and service.
// Returns nil if no match is found (non-failing variant of AssertServiceCall).
func FindServiceCall(calls []ha.ServiceCall, domain, service string) *ha.ServiceCall {
	for i := range calls {
		if calls[i].Domain == domain && calls[i].Service == service {
			return &calls[i]
		}
	}
	return nil
}

// FindServiceCallWithEntity finds the first service call matching domain, service, and entity_id.
// Returns nil if no match is found.
func FindServiceCallWithEntity(calls []ha.ServiceCall, domain, service, entityID string) *ha.ServiceCall {
	for i := range calls {
		call := &calls[i]
		if call.Domain == domain && call.Service == service {
			if eid, ok := call.Data["entity_id"].(string); ok && eid == entityID {
				return call
			}
		}
	}
	return nil
}

// CountServiceCalls counts how many service calls match the given domain and service.
func CountServiceCalls(calls []ha.ServiceCall, domain, service string) int {
	count := 0
	for _, call := range calls {
		if call.Domain == domain && call.Service == service {
			count++
		}
	}
	return count
}
