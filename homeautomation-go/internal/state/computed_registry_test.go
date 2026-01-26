package state

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestManagerForRegistry(t *testing.T) (*Manager, *ha.MockClient) {
	t.Helper()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Set up all required entities with default values
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	return manager, mockClient
}

func TestComputedStateRegistry_Register(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)

	// Test successful registration
	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			nick, _ := ctx.GetBool("isNickHome")
			caroline, _ := ctx.GetBool("isCarolineHome")
			return nick || caroline, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	assert.NoError(t, err)
}

func TestComputedStateRegistry_Register_Validation(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	tests := []struct {
		name        string
		provider    *ComputedStateProvider
		expectError string
	}{
		{
			name: "missing name",
			provider: &ComputedStateProvider{
				Dependencies: []string{"isNickHome"},
				ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
					return true, nil
				},
			},
			expectError: "provider name is required",
		},
		{
			name: "missing compute func",
			provider: &ComputedStateProvider{
				Name:         "test",
				Dependencies: []string{"isNickHome"},
			},
			expectError: "ComputeFunc is required",
		},
		{
			name: "invalid dependency",
			provider: &ComputedStateProvider{
				Name:         "isAnyOwnerHome",
				Dependencies: []string{"nonExistentVariable"},
				ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
					return true, nil
				},
			},
			expectError: "is not a valid state variable",
		},
		{
			name: "periodic mode without interval",
			provider: &ComputedStateProvider{
				Name:         "isAnyOwnerHome",
				Dependencies: []string{},
				ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
					return true, nil
				},
				UpdateMode: UpdatePeriodically,
			},
			expectError: "PeriodicInterval is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			registry := NewComputedStateRegistry(manager, logger)
			err := registry.Register(tc.provider)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectError)
		})
	}
}

func TestComputedStateRegistry_Register_DuplicateName(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)

	provider := &ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
	}

	err := registry.Register(provider)
	assert.NoError(t, err)

	// Try to register again
	err = registry.Register(provider)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestComputedStateRegistry_Start_ComputesInitialValues(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	// Set Nick as home
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	manager.SyncFromHA()

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			nick, _ := ctx.GetBool("isNickHome")
			caroline, _ := ctx.GetBool("isCarolineHome")
			return nick || caroline, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	// Start should compute initial value
	err = registry.Start()
	require.NoError(t, err)
	defer registry.Stop()

	// Verify the computed value
	value, err := manager.GetBool("isAnyOwnerHome")
	assert.NoError(t, err)
	assert.True(t, value, "isAnyOwnerHome should be true because Nick is home")
}

func TestComputedStateRegistry_Start_RecomputesOnDependencyChange(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			nick, _ := ctx.GetBool("isNickHome")
			caroline, _ := ctx.GetBool("isCarolineHome")
			return nick || caroline, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	err = registry.Start()
	require.NoError(t, err)
	defer registry.Stop()

	// Initially false
	value, _ := manager.GetBool("isAnyOwnerHome")
	assert.False(t, value)

	// Nick comes home
	mockClient.SimulateStateChange("input_boolean.nick_home", "on")

	// Should now be true
	value, _ = manager.GetBool("isAnyOwnerHome")
	assert.True(t, value, "isAnyOwnerHome should be true after Nick arrives")

	// Nick leaves
	mockClient.SimulateStateChange("input_boolean.nick_home", "off")

	// Should be false again
	value, _ = manager.GetBool("isAnyOwnerHome")
	assert.False(t, value, "isAnyOwnerHome should be false after Nick leaves")
}

func TestComputedStateRegistry_DependencyOrder(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	// Set Nick as home
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	manager.SyncFromHA()

	registry := NewComputedStateRegistry(manager, logger)

	// Level 1: isAnyOwnerHome depends on isNickHome, isCarolineHome
	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			nick, _ := ctx.GetBool("isNickHome")
			caroline, _ := ctx.GetBool("isCarolineHome")
			return nick || caroline, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	// Level 2: isAnyoneHome depends on isAnyOwnerHome, isToriHere
	err = registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneHome",
		Dependencies: []string{"isAnyOwnerHome", "isToriHere"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			anyOwner, _ := ctx.GetBool("isAnyOwnerHome")
			tori, _ := ctx.GetBool("isToriHere")
			return anyOwner || tori, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	// Start - should compute in dependency order
	err = registry.Start()
	require.NoError(t, err)
	defer registry.Stop()

	// Verify Level 1
	anyOwnerHome, _ := manager.GetBool("isAnyOwnerHome")
	assert.True(t, anyOwnerHome, "Level 1: isAnyOwnerHome should be true")

	// Verify Level 2
	anyoneHome, _ := manager.GetBool("isAnyoneHome")
	assert.True(t, anyoneHome, "Level 2: isAnyoneHome should be true (depends on isAnyOwnerHome)")
}

func TestComputedStateRegistry_TopologicalSort(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)

	// Register in reverse dependency order to test sorting
	// isAnyoneHome depends on isAnyOwnerHome
	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneHome",
		Dependencies: []string{"isAnyOwnerHome", "isToriHere"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	// isAnyOwnerHome has no computed dependencies
	err = registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	// Get dependency graph
	graph := registry.GetDependencyGraph()
	assert.Contains(t, graph["isAnyoneHome"], "isAnyOwnerHome")
	assert.Contains(t, graph["isAnyOwnerHome"], "isNickHome")
}

func TestComputedStateRegistry_CircularDependency(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)

	// Create circular dependency: A depends on B, B depends on A
	// We'll use isAnyOwnerHome and isAnyoneHome for this test

	// First, temporarily modify our understanding - in real code this wouldn't work
	// because the variables need to exist, but we can test the sorting logic

	// For this test, let's verify the topological sort catches cycles
	// by creating a scenario where we have:
	// isAnyOwnerHome depends on isAnyoneHome (fake circular dep)
	// isAnyoneHome depends on isAnyOwnerHome (creates cycle)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isAnyoneHome"}, // Depends on computed state
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	err = registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneHome",
		Dependencies: []string{"isAnyOwnerHome"}, // Creates cycle
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	// Start should fail due to circular dependency
	err = registry.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circular dependency")
}

func TestComputedStateRegistry_Recalculate(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	// Track computation count
	var computeCount int32

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			atomic.AddInt32(&computeCount, 1)
			nick, _ := ctx.GetBool("isNickHome")
			caroline, _ := ctx.GetBool("isCarolineHome")
			return nick || caroline, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	err = registry.Start()
	require.NoError(t, err)
	defer registry.Stop()

	initialCount := atomic.LoadInt32(&computeCount)
	assert.Equal(t, int32(1), initialCount, "Should have computed once on start")

	// Force recalculation (should compute again even though nothing changed)
	err = registry.Recalculate("isAnyOwnerHome")
	assert.NoError(t, err)

	finalCount := atomic.LoadInt32(&computeCount)
	assert.Equal(t, int32(2), finalCount, "Should have computed again after Recalculate")
}

func TestComputedStateRegistry_RecalculateAll(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	var computeCount1, computeCount2 int32

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			atomic.AddInt32(&computeCount1, 1)
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	err = registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneHome",
		Dependencies: []string{"isAnyOwnerHome", "isToriHere"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			atomic.AddInt32(&computeCount2, 1)
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	err = registry.Start()
	require.NoError(t, err)
	defer registry.Stop()

	assert.Equal(t, int32(1), atomic.LoadInt32(&computeCount1))
	assert.Equal(t, int32(1), atomic.LoadInt32(&computeCount2))

	// Recalculate all
	err = registry.RecalculateAll()
	assert.NoError(t, err)

	assert.Equal(t, int32(2), atomic.LoadInt32(&computeCount1))
	assert.Equal(t, int32(2), atomic.LoadInt32(&computeCount2))
}

func TestComputedStateRegistry_Stop_CleansUp(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	var computeCount int32

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			atomic.AddInt32(&computeCount, 1)
			nick, _ := ctx.GetBool("isNickHome")
			caroline, _ := ctx.GetBool("isCarolineHome")
			return nick || caroline, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	err = registry.Start()
	require.NoError(t, err)

	initialCount := atomic.LoadInt32(&computeCount)

	// Stop the registry
	registry.Stop()

	// Simulate state change - should NOT trigger computation
	mockClient.SimulateStateChange("input_boolean.nick_home", "on")

	// Give it a moment to potentially process
	time.Sleep(10 * time.Millisecond)

	finalCount := atomic.LoadInt32(&computeCount)
	assert.Equal(t, initialCount, finalCount, "Should not compute after Stop()")
}

func TestComputedStateRegistry_OnComputed_Callback(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	var callbackCalled bool
	var callbackValue interface{}
	var mu sync.Mutex

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			nick, _ := ctx.GetBool("isNickHome")
			caroline, _ := ctx.GetBool("isCarolineHome")
			return nick || caroline, nil
		},
		UpdateMode: UpdateOnDependencyChange,
		OnComputed: func(newValue interface{}) {
			mu.Lock()
			defer mu.Unlock()
			callbackCalled = true
			callbackValue = newValue
		},
	})
	require.NoError(t, err)

	err = registry.Start()
	require.NoError(t, err)
	defer registry.Stop()

	mu.Lock()
	assert.True(t, callbackCalled, "OnComputed callback should be called")
	assert.Equal(t, false, callbackValue, "Callback should receive the computed value")
	mu.Unlock()
}

func TestComputedStateRegistry_PeriodicUpdate(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	var computeCount int32

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			atomic.AddInt32(&computeCount, 1)
			return true, nil
		},
		UpdateMode:       UpdatePeriodically,
		PeriodicInterval: 50 * time.Millisecond,
	})
	require.NoError(t, err)

	err = registry.Start()
	require.NoError(t, err)

	// Wait for a few periodic updates
	time.Sleep(175 * time.Millisecond)

	registry.Stop()

	// Should have computed: 1 initial + ~3 periodic (50ms interval over 175ms)
	count := atomic.LoadInt32(&computeCount)
	assert.GreaterOrEqual(t, count, int32(3), "Should have computed multiple times")
}

func TestComputedStateRegistry_GetProviderNames(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	err = registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneHome",
		Dependencies: []string{"isAnyOwnerHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	names := registry.GetProviderNames()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "isAnyOwnerHome")
	assert.Contains(t, names, "isAnyoneHome")
}

func TestComputedStateRegistry_RegisterAfterStart(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)

	err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	err = registry.Start()
	require.NoError(t, err)
	defer registry.Stop()

	// Try to register after start
	err = registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneHome",
		Dependencies: []string{"isAnyOwnerHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			return true, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot register provider after registry has started")
}

func TestComputedStateRegistry_ReadOnlyMode(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Set up required entities
	mockClient.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.Connect()

	// Create manager in read-only mode
	manager := NewManager(mockClient, logger, true)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	registry := NewComputedStateRegistry(manager, logger)

	err = registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			nick, _ := ctx.GetBool("isNickHome")
			caroline, _ := ctx.GetBool("isCarolineHome")
			return nick || caroline, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	})
	require.NoError(t, err)

	// Start should work because isAnyOwnerHome is a ComputedOutput
	err = registry.Start()
	require.NoError(t, err)
	defer registry.Stop()

	// Value should be computed correctly
	value, err := manager.GetBool("isAnyOwnerHome")
	assert.NoError(t, err)
	assert.True(t, value, "Computed state should work in read-only mode for ComputedOutput variables")
}
