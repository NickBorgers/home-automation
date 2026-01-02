package shadowstate

import (
	"sort"
	"sync"
	"testing"
)

func TestNewSubscriptionRegistry(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	if registry == nil {
		t.Fatal("expected non-nil registry")
	}

	if registry.haSubscriptions == nil {
		t.Error("expected haSubscriptions to be initialized")
	}

	if registry.stateSubscriptions == nil {
		t.Error("expected stateSubscriptions to be initialized")
	}
}

func TestRegisterHASubscription(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	// Register a subscription
	registry.RegisterHASubscription("energy", "sensor.battery")

	subs := registry.GetHASubscriptions("energy")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0] != "sensor.battery" {
		t.Errorf("expected sensor.battery, got %s", subs[0])
	}
}

func TestRegisterHASubscription_Multiple(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	registry.RegisterHASubscription("energy", "sensor.battery")
	registry.RegisterHASubscription("energy", "sensor.solar")
	registry.RegisterHASubscription("energy", "sensor.grid")

	subs := registry.GetHASubscriptions("energy")
	if len(subs) != 3 {
		t.Fatalf("expected 3 subscriptions, got %d", len(subs))
	}
}

func TestRegisterHASubscription_Duplicate(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	registry.RegisterHASubscription("energy", "sensor.battery")
	registry.RegisterHASubscription("energy", "sensor.battery") // Duplicate

	subs := registry.GetHASubscriptions("energy")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription (no duplicates), got %d", len(subs))
	}
}

func TestRegisterStateSubscription(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	registry.RegisterStateSubscription("energy", "batteryEnergyLevel")

	subs := registry.GetStateSubscriptions("energy")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0] != "batteryEnergyLevel" {
		t.Errorf("expected batteryEnergyLevel, got %s", subs[0])
	}
}

func TestRegisterStateSubscription_Multiple(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	registry.RegisterStateSubscription("lighting", "dayPhase")
	registry.RegisterStateSubscription("lighting", "isAnyoneHome")
	registry.RegisterStateSubscription("lighting", "sunevent")

	subs := registry.GetStateSubscriptions("lighting")
	if len(subs) != 3 {
		t.Fatalf("expected 3 subscriptions, got %d", len(subs))
	}
}

func TestRegisterStateSubscription_Duplicate(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	registry.RegisterStateSubscription("lighting", "dayPhase")
	registry.RegisterStateSubscription("lighting", "dayPhase") // Duplicate

	subs := registry.GetStateSubscriptions("lighting")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription (no duplicates), got %d", len(subs))
	}
}

func TestGetHASubscriptions_NonExistentPlugin(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	subs := registry.GetHASubscriptions("nonexistent")
	if subs != nil {
		t.Errorf("expected nil for nonexistent plugin, got %v", subs)
	}
}

func TestGetStateSubscriptions_NonExistentPlugin(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	subs := registry.GetStateSubscriptions("nonexistent")
	if subs != nil {
		t.Errorf("expected nil for nonexistent plugin, got %v", subs)
	}
}

func TestGetHASubscriptions_ReturnsCopy(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	registry.RegisterHASubscription("energy", "sensor.battery")

	subs := registry.GetHASubscriptions("energy")

	// Modify the returned slice
	subs[0] = "modified"

	// Original should be unchanged
	original := registry.GetHASubscriptions("energy")
	if original[0] != "sensor.battery" {
		t.Error("modifying returned slice affected internal state")
	}
}

func TestGetStateSubscriptions_ReturnsCopy(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	registry.RegisterStateSubscription("energy", "batteryEnergyLevel")

	subs := registry.GetStateSubscriptions("energy")

	// Modify the returned slice
	subs[0] = "modified"

	// Original should be unchanged
	original := registry.GetStateSubscriptions("energy")
	if original[0] != "batteryEnergyLevel" {
		t.Error("modifying returned slice affected internal state")
	}
}

func TestUnregisterPlugin(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	// Register subscriptions
	registry.RegisterHASubscription("energy", "sensor.battery")
	registry.RegisterStateSubscription("energy", "batteryEnergyLevel")

	// Verify they exist
	if len(registry.GetHASubscriptions("energy")) != 1 {
		t.Fatal("HA subscription should exist before unregister")
	}
	if len(registry.GetStateSubscriptions("energy")) != 1 {
		t.Fatal("state subscription should exist before unregister")
	}

	// Unregister
	registry.UnregisterPlugin("energy")

	// Verify they're gone
	if registry.GetHASubscriptions("energy") != nil {
		t.Error("HA subscriptions should be nil after unregister")
	}
	if registry.GetStateSubscriptions("energy") != nil {
		t.Error("state subscriptions should be nil after unregister")
	}
}

func TestUnregisterPlugin_NonExistent(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	// Should not panic
	registry.UnregisterPlugin("nonexistent")
}

func TestGetAllPlugins(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	registry.RegisterHASubscription("energy", "sensor.battery")
	registry.RegisterStateSubscription("lighting", "dayPhase")
	registry.RegisterHASubscription("security", "sensor.door")
	registry.RegisterStateSubscription("security", "isAnyoneHome")

	plugins := registry.GetAllPlugins()

	// Sort for consistent comparison
	sort.Strings(plugins)

	expected := []string{"energy", "lighting", "security"}
	if len(plugins) != len(expected) {
		t.Fatalf("expected %d plugins, got %d", len(expected), len(plugins))
	}

	for i, name := range expected {
		if plugins[i] != name {
			t.Errorf("expected plugin %s at index %d, got %s", name, i, plugins[i])
		}
	}
}

func TestGetAllPlugins_Empty(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	plugins := registry.GetAllPlugins()
	if len(plugins) != 0 {
		t.Errorf("expected empty plugins list, got %d", len(plugins))
	}
}

func TestRegistry_MultiplePlugins(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	// Register for multiple plugins
	registry.RegisterHASubscription("energy", "sensor.battery")
	registry.RegisterHASubscription("energy", "sensor.solar")
	registry.RegisterStateSubscription("energy", "batteryEnergyLevel")

	registry.RegisterHASubscription("lighting", "light.living_room")
	registry.RegisterStateSubscription("lighting", "dayPhase")
	registry.RegisterStateSubscription("lighting", "isAnyoneHome")

	// Verify isolation
	energyHA := registry.GetHASubscriptions("energy")
	if len(energyHA) != 2 {
		t.Errorf("expected 2 HA subscriptions for energy, got %d", len(energyHA))
	}

	lightingState := registry.GetStateSubscriptions("lighting")
	if len(lightingState) != 2 {
		t.Errorf("expected 2 state subscriptions for lighting, got %d", len(lightingState))
	}
}

func TestRegistry_ThreadSafety(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent registration
	for i := 0; i < 10; i++ {
		pluginName := "plugin" + string(rune('A'+i))
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				registry.RegisterHASubscription(name, "sensor.test")
				registry.RegisterStateSubscription(name, "testVar")
				_ = registry.GetHASubscriptions(name)
				_ = registry.GetStateSubscriptions(name)
			}
		}(pluginName)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = registry.GetAllPlugins()
			}
		}()
	}

	// Concurrent unregister
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iterations; j++ {
			registry.UnregisterPlugin("nonexistent")
		}
	}()

	wg.Wait()

	// Should complete without deadlock or panic
	plugins := registry.GetAllPlugins()
	if len(plugins) != 10 {
		t.Errorf("expected 10 plugins after concurrent operations, got %d", len(plugins))
	}
}
