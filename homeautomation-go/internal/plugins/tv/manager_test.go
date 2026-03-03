package tv

import (
	"context"
	"fmt"
	"sync"
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
			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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

func TestTVManager_TVRemoteOff_KillsLightSync(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		remoteState string
		expectKill  bool
		description string
	}{
		{
			name:        "TV remote off - kills light sync",
			remoteState: "off",
			expectKill:  true,
			description: "When TV remote turns off, isTVPlaying should be forced false",
		},
		{
			name:        "TV remote standby - kills light sync",
			remoteState: "standby",
			expectKill:  true,
			description: "When TV remote goes to standby, isTVPlaying should be forced false",
		},
		{
			name:        "TV remote on - no effect",
			remoteState: "on",
			expectKill:  false,
			description: "When TV remote turns on, isTVPlaying should not be affected (sync box drives light sync enablement)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

			// Initially set isTVPlaying to true
			if err := stateMgr.SetBool("isTVPlaying", true); err != nil {
				t.Fatalf("Failed to set initial isTVPlaying: %v", err)
			}

			// Simulate TV remote state change
			newState := &ha.State{
				EntityID: "remote.big_beautiful_oled",
				State:    tt.remoteState,
			}
			manager.handleTVRemoteChange("remote.big_beautiful_oled", nil, newState)

			// Verify isTVPlaying state
			isTVPlaying, err := stateMgr.GetBool("isTVPlaying")
			if err != nil {
				t.Fatalf("Failed to get isTVPlaying: %v", err)
			}

			if tt.expectKill && isTVPlaying {
				t.Errorf("Expected isTVPlaying=false when TV remote is %s, got true", tt.remoteState)
			}
			if !tt.expectKill && !isTVPlaying {
				t.Errorf("Expected isTVPlaying to remain true when TV remote is %s, got false", tt.remoteState)
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
			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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

	if isTVPlaying {
		t.Errorf("Expected isTVPlaying=false when sync box turns off, got true")
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
			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

			// Set isTVon to true (sync box must be on for isTVPlaying calculations)
			if err := stateMgr.SetBool("isTVon", true); err != nil {
				t.Fatalf("Failed to set isTVon: %v", err)
			}

			// Set TV remote to on (TV panel must be on for isTVPlaying to be true)
			manager.handleTVRemoteChange(TVRemoteEntity, nil, &ha.State{EntityID: TVRemoteEntity, State: "on"})

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

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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
			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

			// Set isTVon to true (sync box must be on for isTVPlaying calculations)
			if err := stateMgr.SetBool("isTVon", true); err != nil {
				t.Fatalf("Failed to set isTVon: %v", err)
			}

			// Set TV remote to on (TV panel must be on for isTVPlaying to be true)
			manager.handleTVRemoteChange(TVRemoteEntity, nil, &ha.State{EntityID: TVRemoteEntity, State: "on"})

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
	mockHA.SetState("remote.big_beautiful_oled", "on", nil)
	mockHA.SetState("switch.sync_box_power", "on", nil)
	mockHA.SetState("select.sync_box_hdmi_input", "AppleTV", nil)

	// Create TV manager
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

	// Start the manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start TV manager: %v", err)
	}

	// Verify subscriptions exist (5 HA subs: AppleTV, TV remote, sync box software power, physical power, HDMI input)
	if len(manager.subHelper.GetHASubscriptions()) != 5 {
		t.Errorf("Expected 5 HA subscriptions after Start(), got %d", len(manager.subHelper.GetHASubscriptions()))
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

func TestTVManager_SyncBoxUnavailable_TriggersRecovery(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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

func TestTVManager_SyncBoxPhysicalPowerOff_TurnsItOn(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create TV manager
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

	// Physical power is OFF - should attempt to turn it back on (not a full power cycle)
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

	// Verify only turn_on was called (not a full power cycle — no turn_off)
	calls := mockHA.GetServiceCalls()
	var turnOnCalls, turnOffCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			turnOnCalls = append(turnOnCalls, call)
		}
		if call.Domain == "switch" && call.Service == "turn_off" {
			turnOffCalls = append(turnOffCalls, call)
		}
	}

	if len(turnOnCalls) != 1 {
		t.Errorf("Expected 1 turn_on call to restore physical power, got %d", len(turnOnCalls))
	}
	if len(turnOffCalls) != 0 {
		t.Errorf("Expected 0 turn_off calls (not a full power cycle), got %d", len(turnOffCalls))
	}
	if len(turnOnCalls) > 0 && turnOnCalls[0].Data["entity_id"] != SyncBoxPhysicalPowerEntity {
		t.Errorf("Expected turn_on for %s, got %v", SyncBoxPhysicalPowerEntity, turnOnCalls[0].Data["entity_id"])
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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, true, nil)

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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	// Use a channel to control when the first recovery completes
	recoveryStarted := make(chan struct{}, 1)
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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

	// Start the manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start TV manager: %v", err)
	}

	// Verify subscriptions exist - now should be 5 (AppleTV, TV remote, sync box software power, physical power, HDMI input)
	if len(manager.subHelper.GetHASubscriptions()) != 5 {
		t.Errorf("Expected 5 HA subscriptions after Start(), got %d", len(manager.subHelper.GetHASubscriptions()))
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
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

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

func TestTVManager_LightSyncDebounce_TurnOnImmediate(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

	// Call controlSyncBoxLightSync with isTVPlaying=true
	manager.controlSyncBoxLightSync(true)

	// Verify turn_on was called immediately (no delay)
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 1 {
		t.Errorf("Expected exactly 1 switch service call, got %d", len(switchCalls))
		return
	}

	if switchCalls[0].Service != "turn_on" {
		t.Errorf("Expected turn_on service call, got %s", switchCalls[0].Service)
	}
	if switchCalls[0].Data["entity_id"] != SyncBoxLightSyncEntity {
		t.Errorf("Expected entity_id to be %s, got %v", SyncBoxLightSyncEntity, switchCalls[0].Data["entity_id"])
	}
}

func TestTVManager_LightSyncDebounce_TurnOffDelayed(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	// Use a short debounce for testing
	manager.lightSyncOffDebounce = 50 * time.Millisecond

	// Call controlSyncBoxLightSync with isTVPlaying=false
	manager.controlSyncBoxLightSync(false)

	// Verify no calls immediately
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 0 {
		t.Errorf("Expected 0 switch service calls immediately after turn-off request, got %d", len(switchCalls))
	}

	// Verify pending state
	if !manager.IsLightSyncOffPending() {
		t.Error("Expected light sync turn-off to be pending")
	}

	// Wait for debounce to elapse
	time.Sleep(100 * time.Millisecond)

	// Verify turn_off was called after debounce
	calls = mockHA.GetServiceCalls()
	switchCalls = nil
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 1 {
		t.Errorf("Expected 1 switch service call after debounce, got %d", len(switchCalls))
		return
	}

	if switchCalls[0].Service != "turn_off" {
		t.Errorf("Expected turn_off service call, got %s", switchCalls[0].Service)
	}

	// Verify no longer pending
	if manager.IsLightSyncOffPending() {
		t.Error("Expected light sync turn-off to no longer be pending")
	}
}

func TestTVManager_LightSyncDebounce_CancelledByTurnOn(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	// Use a longer debounce to give us time to cancel
	manager.lightSyncOffDebounce = 200 * time.Millisecond

	// Request turn-off (schedules debounce)
	manager.controlSyncBoxLightSync(false)

	// Verify pending
	if !manager.IsLightSyncOffPending() {
		t.Error("Expected light sync turn-off to be pending")
	}

	// Wait a bit but not long enough for debounce
	time.Sleep(50 * time.Millisecond)

	// Request turn-on (should cancel pending turn-off)
	manager.controlSyncBoxLightSync(true)

	// Verify no longer pending
	if manager.IsLightSyncOffPending() {
		t.Error("Expected light sync turn-off to be cancelled")
	}

	// Wait for original debounce period to elapse
	time.Sleep(200 * time.Millisecond)

	// Verify only turn_on was called, not turn_off
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 1 {
		t.Errorf("Expected exactly 1 switch service call (turn_on only), got %d", len(switchCalls))
		return
	}

	if switchCalls[0].Service != "turn_on" {
		t.Errorf("Expected turn_on service call, got %s", switchCalls[0].Service)
	}
}

func TestTVManager_LightSyncDebounce_MultipleOffRequests(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	// Use a short debounce for testing
	manager.lightSyncOffDebounce = 50 * time.Millisecond

	// Request multiple turn-offs
	manager.controlSyncBoxLightSync(false)
	manager.controlSyncBoxLightSync(false)
	manager.controlSyncBoxLightSync(false)

	// Wait for debounce to elapse
	time.Sleep(100 * time.Millisecond)

	// Verify only one turn_off was called
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	if len(switchCalls) != 1 {
		t.Errorf("Expected exactly 1 switch service call despite multiple requests, got %d", len(switchCalls))
		return
	}

	if switchCalls[0].Service != "turn_off" {
		t.Errorf("Expected turn_off service call, got %s", switchCalls[0].Service)
	}
}

func TestTVManager_LightSyncDebounce_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only state manager

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, true, nil) // read-only manager
	manager.lightSyncOffDebounce = 10 * time.Millisecond

	// Request turn-on in read-only mode
	manager.controlSyncBoxLightSync(true)

	// Request turn-off in read-only mode
	manager.controlSyncBoxLightSync(false)

	// Wait for potential debounce
	time.Sleep(50 * time.Millisecond)

	// Verify NO service calls were made
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

func TestTVManager_LightSyncDebounce_RapidFlapping(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.lightSyncOffDebounce = 100 * time.Millisecond

	// Simulate rapid flapping: playing -> paused -> playing -> paused -> playing
	manager.controlSyncBoxLightSync(true)  // Turn ON (immediate)
	manager.controlSyncBoxLightSync(false) // Schedule OFF
	time.Sleep(10 * time.Millisecond)
	manager.controlSyncBoxLightSync(true)  // Cancel OFF, turn ON
	manager.controlSyncBoxLightSync(false) // Schedule OFF
	time.Sleep(10 * time.Millisecond)
	manager.controlSyncBoxLightSync(true) // Cancel OFF, turn ON

	// Wait for debounce period to ensure no late turn-off
	time.Sleep(150 * time.Millisecond)

	// Verify service calls
	calls := mockHA.GetServiceCalls()
	var switchCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" {
			switchCalls = append(switchCalls, call)
		}
	}

	// Should see 3 turn_on calls and 0 turn_off calls (all turn-offs were cancelled)
	turnOnCount := 0
	turnOffCount := 0
	for _, call := range switchCalls {
		if call.Service == "turn_on" {
			turnOnCount++
		} else if call.Service == "turn_off" {
			turnOffCount++
		}
	}

	if turnOnCount != 3 {
		t.Errorf("Expected 3 turn_on calls during rapid flapping, got %d", turnOnCount)
	}
	if turnOffCount != 0 {
		t.Errorf("Expected 0 turn_off calls during rapid flapping (all cancelled), got %d", turnOffCount)
	}
}

func TestTVManager_SyncBoxUnavailable_ClearsTVStates(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

	// Set initial states: TV is on and playing
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	if err := stateMgr.SetBool("isTVPlaying", true); err != nil {
		t.Fatalf("Failed to set isTVPlaying: %v", err)
	}

	// Set up mock so recovery goroutine doesn't panic
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

	// States should be cleared synchronously (before recovery goroutine)
	isTVon, err := stateMgr.GetBool("isTVon")
	if err != nil {
		t.Fatalf("Failed to get isTVon: %v", err)
	}
	if isTVon {
		t.Error("Expected isTVon=false after sync box goes unavailable")
	}

	isTVPlaying, err := stateMgr.GetBool("isTVPlaying")
	if err != nil {
		t.Fatalf("Failed to get isTVPlaying: %v", err)
	}
	if isTVPlaying {
		t.Error("Expected isTVPlaying=false after sync box goes unavailable")
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.IsTVOn {
		t.Error("Expected shadow IsTVOn=false after sync box goes unavailable")
	}
	if shadowState.Outputs.IsTVPlaying {
		t.Error("Expected shadow IsTVPlaying=false after sync box goes unavailable")
	}
}

func TestTVManager_PowerCycleRecovery_RetryOnTurnOnFailure(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

	// Physical power is on, software is unavailable
	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	// Simulate 2 transient turn_on failures, then success on 3rd attempt
	mockHA.SetServiceFailCount("switch", "turn_on", 2, fmt.Errorf("Z-Wave error 1405: transient decoding error"))

	sleepDone := make(chan struct{})
	var sleepDurations []time.Duration
	var sleepMu sync.Mutex
	manager.sleepFunc = func(d time.Duration) {
		sleepMu.Lock()
		sleepDurations = append(sleepDurations, d)
		sleepMu.Unlock()
	}
	manager.timeNow = time.Now

	go func() {
		manager.checkAndRecoverSyncBox()
		close(sleepDone)
	}()

	// Wait for recovery to complete
	<-sleepDone

	// Verify service calls: only successful calls are recorded by the mock
	// 1 turn_off (succeeds) + 1 turn_on (the 3rd attempt that succeeds)
	calls := mockHA.GetServiceCalls()
	var turnOnCalls, turnOffCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			turnOnCalls = append(turnOnCalls, call)
		}
		if call.Domain == "switch" && call.Service == "turn_off" {
			turnOffCalls = append(turnOffCalls, call)
		}
	}

	if len(turnOffCalls) != 1 {
		t.Errorf("Expected 1 turn_off call, got %d", len(turnOffCalls))
	}
	if len(turnOnCalls) != 1 {
		t.Errorf("Expected 1 successful turn_on call (3rd attempt), got %d", len(turnOnCalls))
	}

	// Verify sleep was called for: debounce + power cycle delay + 2 retry delays
	// Debounce (30s) + PowerCycleDelay (5s) + retry1 (5s) + retry2 (10s) = 4 sleeps
	sleepMu.Lock()
	sleepCount := len(sleepDurations)
	sleepMu.Unlock()
	if sleepCount != 4 {
		t.Errorf("Expected 4 sleep calls (debounce + power cycle delay + 2 retries), got %d: %v",
			sleepCount, sleepDurations)
	}
}

func TestTVManager_PowerCycleRecovery_AllRetriesExhausted(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)

	// Physical power is on, software is unavailable
	mockHA.SetState(SyncBoxPhysicalPowerEntity, "on", nil)
	mockHA.SetState(SyncBoxSoftwarePowerEntity, "unavailable", nil)

	// Simulate permanent turn_on failure
	mockHA.SetServiceError("switch", "turn_on", fmt.Errorf("Z-Wave node unresponsive"))

	recoveryDone := make(chan struct{})
	var sleepDurations []time.Duration
	var sleepMu sync.Mutex
	manager.sleepFunc = func(d time.Duration) {
		sleepMu.Lock()
		sleepDurations = append(sleepDurations, d)
		sleepMu.Unlock()
	}
	manager.timeNow = time.Now

	// Run recovery synchronously via goroutine and wait for completion
	go func() {
		manager.checkAndRecoverSyncBox()
		close(recoveryDone)
	}()

	<-recoveryDone

	// Verify service calls: only successful calls are recorded by the mock
	// 1 turn_off (succeeds), 0 turn_on (all 3 attempts failed, so not recorded)
	calls := mockHA.GetServiceCalls()
	var turnOnCalls, turnOffCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			turnOnCalls = append(turnOnCalls, call)
		}
		if call.Domain == "switch" && call.Service == "turn_off" {
			turnOffCalls = append(turnOffCalls, call)
		}
	}

	if len(turnOffCalls) != 1 {
		t.Errorf("Expected 1 turn_off call, got %d", len(turnOffCalls))
	}
	if len(turnOnCalls) != 0 {
		t.Errorf("Expected 0 successful turn_on calls (all failed), got %d", len(turnOnCalls))
	}

	// Verify sleep was called for: debounce + power cycle delay + 2 retry delays (between attempts 1-2 and 2-3)
	sleepMu.Lock()
	sleepCount := len(sleepDurations)
	sleepMu.Unlock()
	if sleepCount != 4 {
		t.Errorf("Expected 4 sleep calls (debounce + power cycle delay + 2 retries), got %d: %v",
			sleepCount, sleepDurations)
	}
}

// ============================================================================
// Bravia Staleness Detection Tests
// ============================================================================

func TestTVManager_BraviaStaleness_HDMIInputChange_TriggersReload(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Set up initial states: sync box is on, TV remote reports "off" (stale)
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	// TV remote is off (stale Bravia integration)
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	// Also set sync box HDMI input state for post-reload recalculation
	mockHA.SetState(SyncBoxHDMIInputEntity, "Nintendo Switch", nil)
	// After reload, TV remote should report "on"
	mockHA.SetState(TVRemoteEntity, "on", nil)

	// Trigger HDMI input change
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Nintendo Switch",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	// Wait for async reload
	time.Sleep(100 * time.Millisecond)

	// Verify Bravia reload was triggered
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 1 {
		t.Fatalf("Expected 1 config entry reload, got %d", len(reloads))
	}
	if reloads[0].EntryID != BraviaEntryID {
		t.Errorf("Expected reload for entry %s, got %s", BraviaEntryID, reloads[0].EntryID)
	}

	// Verify reload state was tracked
	lastReload, dailyCount, _ := manager.GetBraviaReloadState()
	if lastReload.IsZero() {
		t.Error("Expected lastBraviaReload to be set")
	}
	if dailyCount != 1 {
		t.Errorf("Expected dailyBraviaReloadCount to be 1, got %d", dailyCount)
	}
}

func TestTVManager_BraviaStaleness_SyncBoxPowerOn_TriggersReload(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// TV remote reports "off" (stale Bravia integration)
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	// Set up mock states for post-reload verification
	mockHA.SetState(SyncBoxHDMIInputEntity, "Xbox", nil)
	mockHA.SetState(TVRemoteEntity, "on", nil)

	// Simulate sync box turning on (which sets isTVon=true internally)
	newState := &ha.State{
		EntityID: SyncBoxSoftwarePowerEntity,
		State:    "on",
	}
	manager.handleSyncBoxPowerChange(SyncBoxSoftwarePowerEntity, nil, newState)

	// Wait for async reload
	time.Sleep(100 * time.Millisecond)

	// Verify Bravia reload was triggered
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 1 {
		t.Fatalf("Expected 1 config entry reload, got %d", len(reloads))
	}
	if reloads[0].EntryID != BraviaEntryID {
		t.Errorf("Expected reload for entry %s, got %s", BraviaEntryID, reloads[0].EntryID)
	}
}

func TestTVManager_BraviaStaleness_NoReload_WhenTVRemoteOn(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Sync box is on, TV remote is also on (no mismatch)
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = true
	manager.tvRemoteMu.Unlock()

	mockHA.SetState(SyncBoxHDMIInputEntity, "Nintendo Switch", nil)

	// Trigger HDMI input change
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Nintendo Switch",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	// Wait for any potential async operations
	time.Sleep(100 * time.Millisecond)

	// Verify NO reload was triggered (no mismatch)
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 0 {
		t.Errorf("Expected 0 config entry reloads when TV remote is on, got %d", len(reloads))
	}
}

func TestTVManager_BraviaStaleness_NoReload_WhenSyncBoxOff(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Sync box is off, TV remote is off (consistent — both off)
	if err := stateMgr.SetBool("isTVon", false); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	mockHA.SetState(SyncBoxHDMIInputEntity, "Xbox", nil)

	// Trigger HDMI input change
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Xbox",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	time.Sleep(100 * time.Millisecond)

	// Verify NO reload when sync box is off
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 0 {
		t.Errorf("Expected 0 config entry reloads when sync box is off, got %d", len(reloads))
	}
}

func TestTVManager_BraviaReload_Cooldown(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}

	currentTime := time.Now()
	manager.timeNow = func() time.Time { return currentTime }

	// Set up stale state
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	mockHA.SetState(TVRemoteEntity, "on", nil)
	mockHA.SetState(SyncBoxHDMIInputEntity, "Xbox", nil)

	// Simulate a recent reload (within cooldown period)
	manager.braviaMu.Lock()
	manager.lastBraviaReload = currentTime.Add(-2 * time.Minute) // 2 min ago, cooldown is 5 min
	manager.braviaMu.Unlock()

	// Trigger HDMI input change
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Xbox",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	time.Sleep(100 * time.Millisecond)

	// Verify NO reload (in cooldown)
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 0 {
		t.Errorf("Expected 0 config entry reloads during cooldown, got %d", len(reloads))
	}
}

func TestTVManager_BraviaReload_MaxDailyLimit(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}

	currentTime := time.Now()
	manager.timeNow = func() time.Time { return currentTime }

	// Set up stale state
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	mockHA.SetState(TVRemoteEntity, "on", nil)
	mockHA.SetState(SyncBoxHDMIInputEntity, "Xbox", nil)

	// Already at max daily reloads
	manager.braviaMu.Lock()
	manager.dailyBraviaReloadCount = BraviaMaxDailyReloads
	manager.braviaReloadCountResetAt = currentTime
	manager.braviaMu.Unlock()

	// Trigger HDMI input change
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Xbox",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	time.Sleep(100 * time.Millisecond)

	// Verify NO reload (max daily reached)
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 0 {
		t.Errorf("Expected 0 config entry reloads at max daily limit, got %d", len(reloads))
	}
}

func TestTVManager_BraviaReload_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	// Use a non-read-only state manager so we can set isTVon, but create the
	// Manager with readOnly=true so it skips the actual reload call.
	stateMgr := state.NewManager(mockHA, logger, false)
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, true, nil)
	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Set up stale state (sync box on, TV remote off)
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	mockHA.SetState(TVRemoteEntity, "on", nil)
	mockHA.SetState(SyncBoxHDMIInputEntity, "Xbox", nil)

	// Trigger HDMI input change
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Xbox",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	time.Sleep(100 * time.Millisecond)

	// Verify NO reload in read-only mode
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 0 {
		t.Errorf("Expected 0 config entry reloads in read-only mode, got %d", len(reloads))
	}
}

func TestTVManager_BraviaReload_InProgressSkipsDuplicate(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.timeNow = time.Now

	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	mockHA.SetState(TVRemoteEntity, "on", nil)
	mockHA.SetState(SyncBoxHDMIInputEntity, "Xbox", nil)

	// Use a channel to control when the first reload completes
	reloadStarted := make(chan struct{}, 1)
	reloadComplete := make(chan struct{})

	manager.sleepFunc = func(d time.Duration) {
		select {
		case reloadStarted <- struct{}{}:
		default:
		}
		<-reloadComplete
	}

	// Start first reload
	go manager.reloadBraviaIntegration("test")

	// Wait for first reload to start
	<-reloadStarted

	// Verify reload is in progress
	_, _, inProgress := manager.GetBraviaReloadState()
	if !inProgress {
		t.Error("Expected reload to be in progress")
	}

	// Try to start second reload (should skip)
	secondDone := make(chan struct{})
	go func() {
		manager.reloadBraviaIntegration("test2")
		close(secondDone)
	}()

	// Second reload should return immediately
	select {
	case <-secondDone:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("Second reload should have returned immediately")
	}

	// Allow first reload to complete
	close(reloadComplete)
	time.Sleep(50 * time.Millisecond)

	// Verify only one reload happened
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 1 {
		t.Errorf("Expected exactly 1 config entry reload, got %d", len(reloads))
	}
}

func TestTVManager_BraviaReload_PostReloadRecalculation(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Set up stale state
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	// After reload, TV remote will report "on"
	mockHA.SetState(TVRemoteEntity, "on", nil)
	mockHA.SetState(SyncBoxHDMIInputEntity, "Nintendo Switch", nil)

	// Trigger staleness detection
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Nintendo Switch",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	// Wait for async reload and recalculation
	time.Sleep(100 * time.Millisecond)

	// Verify TV remote state was updated after reload
	manager.tvRemoteMu.RLock()
	tvPanelOn := manager.tvRemoteOn
	manager.tvRemoteMu.RUnlock()

	if !tvPanelOn {
		t.Error("Expected tvRemoteOn to be true after Bravia reload")
	}

	// Verify isTVPlaying was recalculated (non-AppleTV input + TV on = playing)
	isTVPlaying, err := stateMgr.GetBool("isTVPlaying")
	if err != nil {
		t.Fatalf("Failed to get isTVPlaying: %v", err)
	}
	if !isTVPlaying {
		t.Error("Expected isTVPlaying=true after Bravia reload (Nintendo Switch input, TV panel on)")
	}
}

func TestTVManager_BraviaReload_ShadowStateTracking(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}
	manager.timeNow = time.Now

	// Verify initial shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.BraviaReloadCount != 0 {
		t.Errorf("Expected initial BraviaReloadCount to be 0, got %d", shadowState.Outputs.BraviaReloadCount)
	}
	if !shadowState.Outputs.LastBraviaReload.IsZero() {
		t.Error("Expected initial LastBraviaReload to be zero")
	}

	// Set up stale state and trigger reload
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	mockHA.SetState(TVRemoteEntity, "on", nil)
	mockHA.SetState(SyncBoxHDMIInputEntity, "Xbox", nil)

	// Trigger
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Xbox",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	time.Sleep(100 * time.Millisecond)

	// Verify shadow state was updated
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.BraviaReloadCount != 1 {
		t.Errorf("Expected BraviaReloadCount to be 1, got %d", shadowState.Outputs.BraviaReloadCount)
	}
	if shadowState.Outputs.LastBraviaReload.IsZero() {
		t.Error("Expected LastBraviaReload to be set after reload")
	}
}

func TestTVManager_BraviaReload_DailyCounterReset(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil)
	manager.sleepFunc = func(d time.Duration) {}

	currentTime := time.Now()
	manager.timeNow = func() time.Time { return currentTime }

	// Set up stale state
	if err := stateMgr.SetBool("isTVon", true); err != nil {
		t.Fatalf("Failed to set isTVon: %v", err)
	}
	manager.tvRemoteMu.Lock()
	manager.tvRemoteOn = false
	manager.tvRemoteMu.Unlock()

	mockHA.SetState(TVRemoteEntity, "on", nil)
	mockHA.SetState(SyncBoxHDMIInputEntity, "Xbox", nil)

	// Simulate reloads from more than 24 hours ago
	manager.braviaMu.Lock()
	manager.dailyBraviaReloadCount = 5
	manager.braviaReloadCountResetAt = currentTime.Add(-25 * time.Hour)
	manager.braviaMu.Unlock()

	// Trigger HDMI input change
	newState := &ha.State{
		EntityID: SyncBoxHDMIInputEntity,
		State:    "Xbox",
	}
	manager.handleHDMIInputChange(SyncBoxHDMIInputEntity, nil, newState)

	time.Sleep(100 * time.Millisecond)

	// Verify reload WAS triggered (counter was reset)
	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 1 {
		t.Errorf("Expected 1 config entry reload after daily reset, got %d", len(reloads))
	}

	// Verify daily count was reset and incremented to 1
	_, dailyCount, _ := manager.GetBraviaReloadState()
	if dailyCount != 1 {
		t.Errorf("Expected dailyBraviaReloadCount to be 1 after reset, got %d", dailyCount)
	}
}
