package tv

import (
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

func TestTVManager_AppleTVStateChange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		appleTVState      string
		expectedIsPlaying bool
		description       string
	}{
		{
			name:              "Apple TV playing",
			appleTVState:      "playing",
			expectedIsPlaying: true,
			description:       "When Apple TV state is 'playing', isAppleTVPlaying should be true",
		},
		{
			name:              "Apple TV paused",
			appleTVState:      "paused",
			expectedIsPlaying: false,
			description:       "When Apple TV state is 'paused', isAppleTVPlaying should be false",
		},
		{
			name:              "Apple TV idle",
			appleTVState:      "idle",
			expectedIsPlaying: false,
			description:       "When Apple TV state is 'idle', isAppleTVPlaying should be false",
		},
		{
			name:              "Apple TV off",
			appleTVState:      "off",
			expectedIsPlaying: false,
			description:       "When Apple TV state is 'off', isAppleTVPlaying should be false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create mock HA client and state manager

			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Create TV manager
			manager := NewManager(mockHA, stateMgr, logger, false, nil)

			// Simulate Apple TV state change
			newState := &ha.State{
				EntityID: "media_player.big_beautiful_oled",
				State:    tt.appleTVState,
			}
			manager.handleAppleTVStateChange("media_player.big_beautiful_oled", nil, newState)

			// Verify isAppleTVPlaying state
			isPlaying, err := stateMgr.GetBool("isAppleTVPlaying")
			if err != nil {
				t.Fatalf("Failed to get isAppleTVPlaying: %v", err)
			}

			if isPlaying != tt.expectedIsPlaying {
				t.Errorf("Expected isAppleTVPlaying=%v, got %v", tt.expectedIsPlaying, isPlaying)
			}
		})
	}
}

func TestTVManager_SyncBoxPowerChange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		syncBoxState   string
		expectedIsTVOn bool
		description    string
	}{
		{
			name:           "Sync box on",
			syncBoxState:   "on",
			expectedIsTVOn: true,
			description:    "When sync box is on, isTVon should be true",
		},
		{
			name:           "Sync box off",
			syncBoxState:   "off",
			expectedIsTVOn: false,
			description:    "When sync box is off, isTVon should be false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create mock HA client and state manager

			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Create TV manager
			manager := NewManager(mockHA, stateMgr, logger, false, nil)

			// Simulate sync box state change
			newState := &ha.State{
				EntityID: "switch.sync_box_power",
				State:    tt.syncBoxState,
			}
			manager.handleSyncBoxPowerChange("switch.sync_box_power", nil, newState)

			// Verify isTVon state
			isTVOn, err := stateMgr.GetBool("isTVon")
			if err != nil {
				t.Fatalf("Failed to get isTVon: %v", err)
			}

			if isTVOn != tt.expectedIsTVOn {
				t.Errorf("Expected isTVon=%v, got %v", tt.expectedIsTVOn, isTVOn)
			}
		})
	}
}

func TestTVManager_SyncBoxOff_SetsTVPlayingFalse(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Initially set isTVPlaying to true
	if err := stateMgr.SetBool("isTVPlaying", true); err != nil {
		t.Fatalf("Failed to set initial isTVPlaying: %v", err)
	}

	// Simulate sync box turning off
	newState := &ha.State{
		EntityID: "switch.sync_box_power",
		State:    "off",
	}
	manager.handleSyncBoxPowerChange("switch.sync_box_power", nil, newState)

	// Verify isTVPlaying is now false
	isTVPlaying, err := stateMgr.GetBool("isTVPlaying")
	if err != nil {
		t.Fatalf("Failed to get isTVPlaying: %v", err)
	}

	if isTVPlaying != false {
		t.Errorf("Expected isTVPlaying=false when TV turns off, got %v", isTVPlaying)
	}
}

func TestTVManager_HDMIInputChange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		hdmiInput           string
		isAppleTVPlaying    bool
		expectedIsTVPlaying bool
		description         string
	}{
		{
			name:                "Apple TV input - Apple TV playing",
			hdmiInput:           "AppleTV",
			isAppleTVPlaying:    true,
			expectedIsTVPlaying: true,
			description:         "When AppleTV input is selected and Apple TV is playing, isTVPlaying=true",
		},
		{
			name:                "Apple TV input - Apple TV not playing",
			hdmiInput:           "AppleTV",
			isAppleTVPlaying:    false,
			expectedIsTVPlaying: false,
			description:         "When AppleTV input is selected and Apple TV is not playing, isTVPlaying=false",
		},
		{
			name:                "HDMI 1 input - assume playing",
			hdmiInput:           "HDMI 1",
			isAppleTVPlaying:    false,
			expectedIsTVPlaying: true,
			description:         "When non-AppleTV input is selected, assume TV is playing",
		},
		{
			name:                "HDMI 2 input - assume playing",
			hdmiInput:           "HDMI 2",
			isAppleTVPlaying:    true,
			expectedIsTVPlaying: true,
			description:         "When non-AppleTV input is selected, assume TV is playing regardless of Apple TV state",
		},
		{
			name:                "Console input - assume playing",
			hdmiInput:           "Console",
			isAppleTVPlaying:    false,
			expectedIsTVPlaying: true,
			description:         "When Console input is selected, assume TV is playing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create mock HA client and state manager

			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Create TV manager
			manager := NewManager(mockHA, stateMgr, logger, false, nil)

			// Set isTVon to true (TV must be on for isTVPlaying calculations to work)
			if err := stateMgr.SetBool("isTVon", true); err != nil {
				t.Fatalf("Failed to set isTVon: %v", err)
			}

			// Set isAppleTVPlaying state
			if err := stateMgr.SetBool("isAppleTVPlaying", tt.isAppleTVPlaying); err != nil {
				t.Fatalf("Failed to set isAppleTVPlaying: %v", err)
			}

			// Simulate HDMI input change
			newState := &ha.State{
				EntityID: "select.sync_box_hdmi_input",
				State:    tt.hdmiInput,
			}
			manager.handleHDMIInputChange("select.sync_box_hdmi_input", nil, newState)

			// Verify isTVPlaying state
			isTVPlaying, err := stateMgr.GetBool("isTVPlaying")
			if err != nil {
				t.Fatalf("Failed to get isTVPlaying: %v", err)
			}

			if isTVPlaying != tt.expectedIsTVPlaying {
				t.Errorf("Expected isTVPlaying=%v, got %v (hdmiInput=%s, isAppleTVPlaying=%v)",
					tt.expectedIsTVPlaying, isTVPlaying, tt.hdmiInput, tt.isAppleTVPlaying)
			}
		})
	}
}

func TestTVManager_HDMIInputChange_TVOff_AlwaysFalse(t *testing.T) {
	t.Parallel()
	// Test that isTVPlaying is always false when TV is off, regardless of HDMI input

	tests := []struct {
		name             string
		hdmiInput        string
		isAppleTVPlaying bool
	}{
		{
			name:             "HDMI 1 input with TV off",
			hdmiInput:        "HDMI 1",
			isAppleTVPlaying: false,
		},
		{
			name:             "AppleTV input with TV off and AppleTV playing",
			hdmiInput:        "AppleTV",
			isAppleTVPlaying: true,
		},
		{
			name:             "Console input with TV off",
			hdmiInput:        "Console",
			isAppleTVPlaying: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			manager := NewManager(mockHA, stateMgr, logger, false, nil)

			// Set isTVon to false (TV is off)
			if err := stateMgr.SetBool("isTVon", false); err != nil {
				t.Fatalf("Failed to set isTVon: %v", err)
			}

			// Set isAppleTVPlaying state
			if err := stateMgr.SetBool("isAppleTVPlaying", tt.isAppleTVPlaying); err != nil {
				t.Fatalf("Failed to set isAppleTVPlaying: %v", err)
			}

			// Simulate HDMI input change
			newState := &ha.State{
				EntityID: "select.sync_box_hdmi_input",
				State:    tt.hdmiInput,
			}
			manager.handleHDMIInputChange("select.sync_box_hdmi_input", nil, newState)

			// Verify isTVPlaying is false when TV is off
			isTVPlaying, err := stateMgr.GetBool("isTVPlaying")
			if err != nil {
				t.Fatalf("Failed to get isTVPlaying: %v", err)
			}

			if isTVPlaying != false {
				t.Errorf("Expected isTVPlaying=false when TV is off, got %v (hdmiInput=%s)",
					isTVPlaying, tt.hdmiInput)
			}
		})
	}
}

func TestTVManager_AppleTVPlayingChange_RecalculatesTVPlaying(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		hdmiInput           string
		oldAppleTVPlaying   bool
		newAppleTVPlaying   bool
		expectedIsTVPlaying bool
		description         string
	}{
		{
			name:                "AppleTV input - changes from playing to not playing",
			hdmiInput:           "AppleTV",
			oldAppleTVPlaying:   true,
			newAppleTVPlaying:   false,
			expectedIsTVPlaying: false,
			description:         "When Apple TV stops playing on AppleTV input, isTVPlaying should become false",
		},
		{
			name:                "AppleTV input - changes from not playing to playing",
			hdmiInput:           "AppleTV",
			oldAppleTVPlaying:   false,
			newAppleTVPlaying:   true,
			expectedIsTVPlaying: true,
			description:         "When Apple TV starts playing on AppleTV input, isTVPlaying should become true",
		},
		{
			name:                "Non-AppleTV input - Apple TV state doesn't affect isTVPlaying",
			hdmiInput:           "HDMI 1",
			oldAppleTVPlaying:   true,
			newAppleTVPlaying:   false,
			expectedIsTVPlaying: true,
			description:         "When on non-AppleTV input, Apple TV state changes don't affect isTVPlaying",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create mock HA client and state manager

			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Create TV manager
			manager := NewManager(mockHA, stateMgr, logger, false, nil)

			// Set isTVon to true (TV must be on for isTVPlaying calculations to work)
			if err := stateMgr.SetBool("isTVon", true); err != nil {
				t.Fatalf("Failed to set isTVon: %v", err)
			}

			// Set initial HDMI input in mock HA client
			mockHA.SetState("select.sync_box_hdmi_input", tt.hdmiInput, nil)

			// Set initial isAppleTVPlaying state
			if err := stateMgr.SetBool("isAppleTVPlaying", tt.oldAppleTVPlaying); err != nil {
				t.Fatalf("Failed to set initial isAppleTVPlaying: %v", err)
			}

			// Update to new isAppleTVPlaying state
			if err := stateMgr.SetBool("isAppleTVPlaying", tt.newAppleTVPlaying); err != nil {
				t.Fatalf("Failed to set new isAppleTVPlaying: %v", err)
			}

			// Simulate the state change handler
			manager.handleAppleTVPlayingChange("isAppleTVPlaying", tt.oldAppleTVPlaying, tt.newAppleTVPlaying)

			// Small delay to allow state propagation
			time.Sleep(10 * time.Millisecond)

			// Verify isTVPlaying state
			isTVPlaying, err := stateMgr.GetBool("isTVPlaying")
			if err != nil {
				t.Fatalf("Failed to get isTVPlaying: %v", err)
			}

			if isTVPlaying != tt.expectedIsTVPlaying {
				t.Errorf("Expected isTVPlaying=%v, got %v (hdmiInput=%s, isAppleTVPlaying=%v->%v)",
					tt.expectedIsTVPlaying, isTVPlaying, tt.hdmiInput, tt.oldAppleTVPlaying, tt.newAppleTVPlaying)
			}
		})
	}
}

func TestTVManager_Start_InitializesStates(t *testing.T) {
	t.Parallel(
	// Create mock HA client
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set up initial entity states in mock HA
	mockHA.SetState("media_player.big_beautiful_oled", "playing", nil)
	mockHA.SetState("switch.sync_box_power", "on", nil)
	mockHA.SetState("select.sync_box_hdmi_input", "AppleTV", nil)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Start the manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start TV manager: %v", err)
	}

	// Small delay to allow initialization
	time.Sleep(50 * time.Millisecond)

	// Verify states were initialized correctly
	isAppleTVPlaying, err := stateMgr.GetBool("isAppleTVPlaying")
	if err != nil {
		t.Fatalf("Failed to get isAppleTVPlaying: %v", err)
	}
	if !isAppleTVPlaying {
		t.Errorf("Expected isAppleTVPlaying=true after initialization, got false")
	}

	isTVOn, err := stateMgr.GetBool("isTVon")
	if err != nil {
		t.Fatalf("Failed to get isTVon: %v", err)
	}
	if !isTVOn {
		t.Errorf("Expected isTVon=true after initialization, got false")
	}

	isTVPlaying, err := stateMgr.GetBool("isTVPlaying")
	if err != nil {
		t.Fatalf("Failed to get isTVPlaying: %v", err)
	}
	if !isTVPlaying {
		t.Errorf("Expected isTVPlaying=true after initialization (AppleTV input + playing), got false")
	}

	// Clean up
	manager.Stop()
}

func TestTVManager_Stop_CleansUpSubscriptions(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Start the manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start TV manager: %v", err)
	}

	// Verify subscriptions exist (4 HA subs: AppleTV, sync box software power, physical power, HDMI input)
	if len(manager.subHelper.GetHASubscriptions()) != 4 {
		t.Errorf("Expected 4 HA subscriptions after Start(), got %d", len(manager.subHelper.GetHASubscriptions()))
	}
	if len(manager.subHelper.GetStateSubscriptions()) != 1 {
		t.Errorf("Expected 1 state subscription after Start(), got %d", len(manager.subHelper.GetStateSubscriptions()))
	}

	// Stop the manager
	manager.Stop()

	// Verify subscriptions were cleaned up
	if len(manager.subHelper.GetHASubscriptions()) != 0 {
		t.Error("Expected haSubscriptions to be empty after Stop()")
	}
	if len(manager.subHelper.GetStateSubscriptions()) != 0 {
		t.Error("Expected stateSubscriptions to be empty after Stop()")
	}
}

func TestTVManager_ReadOnlyMode(t *testing.T) {
	t.Parallel(
	// Create mock HA client
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()

	// Create state manager in read-only mode
	stateMgr := state.NewManager(mockHA, logger, true)

	// Initialize state from HA (this populates the cache)
	mockHA.SetState("input_boolean.apple_tv_playing", "off", nil)
	if err := stateMgr.SyncFromHA(); err != nil {
		t.Fatalf("Failed to sync from HA: %v", err)
	}

	// Create TV manager in read-only mode
	_ = NewManager(mockHA, stateMgr, logger, true, nil)

	// Simulate HA state change (this should update local cache)
	mockHA.SimulateStateChange("input_boolean.apple_tv_playing", "on")

	// Small delay to allow state propagation
	time.Sleep(10 * time.Millisecond)

	// In read-only mode, local cache should still be updated when HA sends changes
	isPlaying, err := stateMgr.GetBool("isAppleTVPlaying")
	if err != nil {
		t.Fatalf("Failed to get isAppleTVPlaying: %v", err)
	}
	if !isPlaying {
		t.Error("Expected isAppleTVPlaying=true (local cache should update from HA changes even in read-only mode)")
	}

	// Verify that the manager doesn't try to write back to HA (this is implicit -
	// if it tried, it would error, but the state manager only prevents writes,
	// not reads or cache updates from HA)
}

func TestTVManager_SyncBoxUnavailable_TriggersRecovery(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Set up initial states - physical power is on
	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil) // Will stay unavailable after debounce

	// Use fast sleep for testing
	var sleepCalled int
	manager.sleepFunc = func(d time.Duration) {
		sleepCalled++
	}
	manager.timeNow = time.Now

	// Trigger unavailable state handling
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "unavailable",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async recovery to complete
	time.Sleep(50 * time.Millisecond)

	// Verify service calls were made (turn off then turn on)
	calls := mockHA.GetServiceCalls()

	// Filter for switch service calls
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 2 {
		t.Errorf("Expected 2 switch service calls for power cycle, got %d", len(switchCalls))
		return
	}

	// First call should be turn_off
	if switchCalls[0].Service != "turn_off" {
		t.Errorf("Expected first call to be turn_off, got %s", switchCalls[0].Service)
	}
	if switchCalls[0].Data["entity_id"] != SyncBoxPhysicalPowerEntity {
		t.Errorf("Expected entity_id to be %s, got %v", SyncBoxPhysicalPowerEntity, switchCalls[0].Data["entity_id"])
	}

	// Second call should be turn_on
	if switchCalls[1].Service != "turn_on" {
		t.Errorf("Expected second call to be turn_on, got %s", switchCalls[1].Service)
	}

	// Verify recovery state
	lastReboot, dailyCount, _ := manager.GetRecoveryState()
	if lastReboot.IsZero() {
		t.Error("Expected lastReboot to be set")
	}
	if dailyCount != 1 {
		t.Errorf("Expected dailyRebootCount to be 1, got %d", dailyCount)
	}
}

func TestTVManager_SyncBoxRecoversOnItsOwn_NoRecovery(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Physical power is on, but software power will recover after debounce
	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)

	// Start with unavailable, but it will be "on" when we check after debounce
	sleepCount := 0
	manager.sleepFunc = func(d time.Duration) {
		sleepCount++
		// After debounce sleep, change state to recovered
		if sleepCount == 1 {
			mockHA.SetState(SyncBoxSoftwarePowerEntity, "on", nil)
		}
	}
	manager.timeNow = time.Now

	// Initially set to unavailable
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	// Trigger unavailable state handling
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "unavailable",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async recovery to complete
	time.Sleep(50 * time.Millisecond)

	// Verify NO service calls were made (device recovered on its own)
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 0 {
		t.Errorf("Expected 0 switch service calls since device recovered, got %d", len(switchCalls))
	}
}

func TestTVManager_SyncBoxPhysicalPowerOff_NoRecovery(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Physical power is OFF - no recovery should happen
	mockHA.SetState(SyncBoxPhysicalPowerEntity, "off", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Trigger unavailable state handling
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "unavailable",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async recovery to complete
	time.Sleep(50 * time.Millisecond)

	// Verify NO service calls were made (physical power is off intentionally)
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 0 {
		t.Errorf("Expected 0 switch service calls since physical power is off, got %d", len(switchCalls))
	}
}

func TestTVManager_SyncBoxRecoveryCooldown(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	manager.sleepFunc = func(d time.Duration) {}

	// Set time to now
	currentTime := time.Now()
	manager.timeNow = func() time.Time { return currentTime }

	// Simulate a recent reboot (within cooldown period)
	manager.recoveryMu.Lock()
	manager.lastSyncBoxReboot = currentTime.Add(-5 * time.Minute) // 5 minutes ago, cooldown is 10 minutes
	manager.recoveryMu.Unlock()

	// Trigger unavailable state handling
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "unavailable",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async recovery to complete
	time.Sleep(50 * time.Millisecond)

	// Verify NO service calls were made (in cooldown)
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 0 {
		t.Errorf("Expected 0 switch service calls during cooldown, got %d", len(switchCalls))
	}
}

func TestTVManager_SyncBoxMaxDailyReboots(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	manager.sleepFunc = func(d time.Duration) {}

	currentTime := time.Now()
	manager.timeNow = func() time.Time { return currentTime }

	// Simulate already at max daily reboots
	manager.recoveryMu.Lock()
	manager.dailyRebootCount = SyncBoxMaxDailyReboots
	manager.rebootCountResetAt = currentTime
	manager.recoveryMu.Unlock()

	// Trigger unavailable state handling
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "unavailable",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async recovery to complete
	time.Sleep(50 * time.Millisecond)

	// Verify NO service calls were made (max daily reboots reached)
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 0 {
		t.Errorf("Expected 0 switch service calls when at max daily reboots, got %d", len(switchCalls))
	}
}

func TestTVManager_SyncBoxRecoveryReadOnlyMode(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // Read-only mode

	// Create TV manager in read-only mode
	manager := NewManager(mockHA, stateMgr, logger, true, nil)

	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Trigger unavailable state handling
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "unavailable",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async recovery to complete
	time.Sleep(50 * time.Millisecond)

	// Verify NO service calls were made (read-only mode)
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 0 {
		t.Errorf("Expected 0 switch service calls in read-only mode, got %d", len(switchCalls))
	}
}

func TestTVManager_SyncBoxDailyCounterReset(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	manager.sleepFunc = func(d time.Duration) {}

	// Set time to "tomorrow" from when the reboot count was set
	currentTime := time.Now()
	manager.timeNow = func() time.Time { return currentTime }

	// Simulate reboots from more than 24 hours ago
	manager.recoveryMu.Lock()
	manager.dailyRebootCount = 5
	manager.rebootCountResetAt = currentTime.Add(-25 * time.Hour) // More than 24 hours ago
	manager.recoveryMu.Unlock()

	// Trigger unavailable state handling
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "unavailable",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async recovery to complete
	time.Sleep(50 * time.Millisecond)

	// Verify service calls WERE made (counter was reset)
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 2 {
		t.Errorf("Expected 2 switch service calls after daily reset, got %d", len(switchCalls))
	}

	// Verify daily count was reset and incremented to 1
	_, dailyCount, _ := manager.GetRecoveryState()
	if dailyCount != 1 {
		t.Errorf("Expected dailyRebootCount to be 1 after reset, got %d", dailyCount)
	}
}

func TestTVManager_SyncBoxRecoveryInProgress_SkipsDuplicate(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	// Use a channel to control when the first recovery completes
	recoveryStarted := make(chan struct{})
	recoveryComplete := make(chan struct{})

	manager.sleepFunc = func(d time.Duration) {
		select {
		case recoveryStarted <- struct{}{}:
		default:
		}
		<-recoveryComplete // Block until we signal completion
	}
	manager.timeNow = time.Now

	// Start first recovery
	go manager.checkAndRecoverSyncBox()

	// Wait for first recovery to start
	<-recoveryStarted

	// Verify recovery is in progress
	_, _, inProgress := manager.GetRecoveryState()
	if !inProgress {
		t.Error("Expected recovery to be in progress")
	}

	// Try to start second recovery (should skip)
	secondRecoveryDone := make(chan struct{})
	go func() {
		manager.checkAndRecoverSyncBox()
		close(secondRecoveryDone)
	}()

	// Second recovery should return immediately
	select {
	case <-secondRecoveryDone:
		// Good - second recovery returned quickly
	case <-time.After(100 * time.Millisecond):
		t.Error("Second recovery should have returned immediately")
	}

	// Now allow first recovery to complete
	close(recoveryComplete)

	// Wait for first recovery to finish
	time.Sleep(50 * time.Millisecond)

	// Verify only one set of service calls
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 2 {
		t.Errorf("Expected exactly 2 switch service calls (one power cycle), got %d", len(switchCalls))
	}
}

func TestTVManager_Start_AddsPhysicalPowerSubscription(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Start the manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start TV manager: %v", err)
	}

	// Verify subscriptions exist - now should be 4 (added physical power subscription)
	if len(manager.subHelper.GetHASubscriptions()) != 4 {
		t.Errorf("Expected 4 HA subscriptions after Start(), got %d", len(manager.subHelper.GetHASubscriptions()))
	}
	if len(manager.subHelper.GetStateSubscriptions()) != 1 {
		t.Errorf("Expected 1 state subscription after Start(), got %d", len(manager.subHelper.GetStateSubscriptions()))
	}

	// Clean up
	manager.Stop()
}

func TestTVManager_ShadowState_TracksRecovery(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Check initial shadow state
	shadowState := manager.GetShadowState()
	if !shadowState.Outputs.SyncBoxAvailable {
		t.Error("Expected SyncBoxAvailable to be true initially")
	}
	if shadowState.Outputs.DailyRebootCount != 0 {
		t.Errorf("Expected DailyRebootCount to be 0, got %d", shadowState.Outputs.DailyRebootCount)
	}

	// Set up for recovery
	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Trigger unavailable state
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "unavailable",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async recovery
	time.Sleep(50 * time.Millisecond)

	// Check shadow state after recovery
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.SyncBoxAvailable {
		t.Error("Expected SyncBoxAvailable to be false after unavailable state")
	}
	if shadowState.Outputs.DailyRebootCount != 1 {
		t.Errorf("Expected DailyRebootCount to be 1, got %d", shadowState.Outputs.DailyRebootCount)
	}
	if shadowState.Outputs.LastSyncBoxReboot.IsZero() {
		t.Error("Expected LastSyncBoxReboot to be set")
	}
}
