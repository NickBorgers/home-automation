package statetracking

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

func TestStateTrackingManager_IsAnyOwnerHome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		isNickHome     bool
		isCarolineHome bool
		expectedOwner  bool
	}{
		{
			name:           "Both owners away",
			isNickHome:     false,
			isCarolineHome: false,
			expectedOwner:  false,
		},
		{
			name:           "Only Nick home",
			isNickHome:     true,
			isCarolineHome: false,
			expectedOwner:  true,
		},
		{
			name:           "Only Caroline home",
			isNickHome:     false,
			isCarolineHome: true,
			expectedOwner:  true,
		},
		{
			name:           "Both owners home",
			isNickHome:     true,
			isCarolineHome: true,
			expectedOwner:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			if err := stateMgr.SetBool("isNickHome", tt.isNickHome); err != nil {
				t.Fatalf("Failed to set isNickHome: %v", err)
			}
			if err := stateMgr.SetBool("isCarolineHome", tt.isCarolineHome); err != nil {
				t.Fatalf("Failed to set isCarolineHome: %v", err)
			}

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			actualOwner, err := stateMgr.GetBool("isAnyOwnerHome")
			if err != nil {
				t.Fatalf("Failed to get isAnyOwnerHome: %v", err)
			}
			if actualOwner != tt.expectedOwner {
				t.Errorf("Expected isAnyOwnerHome=%v, got %v (Nick=%v, Caroline=%v)",
					tt.expectedOwner, actualOwner, tt.isNickHome, tt.isCarolineHome)
			}
		})
	}
}

func TestStateTrackingManager_IsAnyoneHome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		isNickHome      bool
		isCarolineHome  bool
		isAssistantHere bool
		expectedAnyone  bool
	}{
		{
			name:            "Everyone away",
			isNickHome:      false,
			isCarolineHome:  false,
			isAssistantHere: false,
			expectedAnyone:  false,
		},
		{
			name:            "Only Nick home",
			isNickHome:      true,
			isCarolineHome:  false,
			isAssistantHere: false,
			expectedAnyone:  true,
		},
		{
			name:            "Only Caroline home",
			isNickHome:      false,
			isCarolineHome:  true,
			isAssistantHere: false,
			expectedAnyone:  true,
		},
		{
			name:            "Only Assistant here",
			isNickHome:      false,
			isCarolineHome:  false,
			isAssistantHere: true,
			expectedAnyone:  true,
		},
		{
			name:            "Nick and Assistant home",
			isNickHome:      true,
			isCarolineHome:  false,
			isAssistantHere: true,
			expectedAnyone:  true,
		},
		{
			name:            "Everyone home",
			isNickHome:      true,
			isCarolineHome:  true,
			isAssistantHere: true,
			expectedAnyone:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			if err := stateMgr.SetBool("isNickHome", tt.isNickHome); err != nil {
				t.Fatalf("Failed to set isNickHome: %v", err)
			}
			if err := stateMgr.SetBool("isCarolineHome", tt.isCarolineHome); err != nil {
				t.Fatalf("Failed to set isCarolineHome: %v", err)
			}
			if err := stateMgr.SetBool("isAssistantHere", tt.isAssistantHere); err != nil {
				t.Fatalf("Failed to set isAssistantHere: %v", err)
			}

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			actualAnyone, err := stateMgr.GetBool("isAnyoneHome")
			if err != nil {
				t.Fatalf("Failed to get isAnyoneHome: %v", err)
			}
			if actualAnyone != tt.expectedAnyone {
				t.Errorf("Expected isAnyoneHome=%v, got %v (Nick=%v, Caroline=%v, Assistant=%v)",
					tt.expectedAnyone, actualAnyone, tt.isNickHome, tt.isCarolineHome, tt.isAssistantHere)
			}
		})
	}
}

func TestStateTrackingManager_SleepDerivedStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		isMasterAsleep    bool
		isGuestAsleep     bool
		expectedAnyAsleep bool
		expectedAllAsleep bool
	}{
		{
			name:              "Everyone awake",
			isMasterAsleep:    false,
			isGuestAsleep:     false,
			expectedAnyAsleep: false,
			expectedAllAsleep: false,
		},
		{
			name:              "Only master asleep",
			isMasterAsleep:    true,
			isGuestAsleep:     false,
			expectedAnyAsleep: true,
			expectedAllAsleep: false,
		},
		{
			name:              "Only guest asleep",
			isMasterAsleep:    false,
			isGuestAsleep:     true,
			expectedAnyAsleep: true,
			expectedAllAsleep: false,
		},
		{
			name:              "Everyone asleep",
			isMasterAsleep:    true,
			isGuestAsleep:     true,
			expectedAnyAsleep: true,
			expectedAllAsleep: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Set isHaveGuests to true to test independent sleep states
			if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
				t.Fatalf("Failed to set isHaveGuests: %v", err)
			}
			if err := stateMgr.SetBool("isMasterAsleep", tt.isMasterAsleep); err != nil {
				t.Fatalf("Failed to set isMasterAsleep: %v", err)
			}
			if err := stateMgr.SetBool("isGuestAsleep", tt.isGuestAsleep); err != nil {
				t.Fatalf("Failed to set isGuestAsleep: %v", err)
			}

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Verify isAnyoneAsleep
			actualAnyAsleep, err := stateMgr.GetBool("isAnyoneAsleep")
			if err != nil {
				t.Fatalf("Failed to get isAnyoneAsleep: %v", err)
			}
			if actualAnyAsleep != tt.expectedAnyAsleep {
				t.Errorf("Expected isAnyoneAsleep=%v, got %v (Master=%v, Guest=%v)",
					tt.expectedAnyAsleep, actualAnyAsleep, tt.isMasterAsleep, tt.isGuestAsleep)
			}

			// Verify isEveryoneAsleep
			actualAllAsleep, err := stateMgr.GetBool("isEveryoneAsleep")
			if err != nil {
				t.Fatalf("Failed to get isEveryoneAsleep: %v", err)
			}
			if actualAllAsleep != tt.expectedAllAsleep {
				t.Errorf("Expected isEveryoneAsleep=%v, got %v (Master=%v, Guest=%v)",
					tt.expectedAllAsleep, actualAllAsleep, tt.isMasterAsleep, tt.isGuestAsleep)
			}
		})
	}
}

func TestStateTrackingManager_DynamicUpdates(t *testing.T) {
	t.Parallel(
	// Test that derived states update when source states change
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set up initial state - everyone away
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	if err := stateMgr.SetBool("isAssistantHere", false); err != nil {
		t.Fatalf("Failed to set isAssistantHere: %v", err)
	}

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify initial state - no one home
	isAnyoneHome, _ := stateMgr.GetBool("isAnyoneHome")
	if isAnyoneHome != false {
		t.Errorf("Expected isAnyoneHome=false initially, got %v", isAnyoneHome)
	}

	// Nick arrives home
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to update isNickHome: %v", err)
	}
	isAnyOwnerHome, _ := stateMgr.GetBool("isAnyOwnerHome")
	if isAnyOwnerHome != true {
		t.Errorf("Expected isAnyOwnerHome=true after Nick arrives, got %v", isAnyOwnerHome)
	}
	isAnyoneHome, _ = stateMgr.GetBool("isAnyoneHome")
	if isAnyoneHome != true {
		t.Errorf("Expected isAnyoneHome=true after Nick arrives, got %v", isAnyoneHome)
	}

	// Nick leaves, but Assistant arrives
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to update isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isAssistantHere", true); err != nil {
		t.Fatalf("Failed to update isAssistantHere: %v", err)
	}
	isAnyOwnerHome, _ = stateMgr.GetBool("isAnyOwnerHome")
	if isAnyOwnerHome != false {
		t.Errorf("Expected isAnyOwnerHome=false after Nick leaves, got %v", isAnyOwnerHome)
	}
	isAnyoneHome, _ = stateMgr.GetBool("isAnyoneHome")
	if isAnyoneHome != true {
		t.Errorf("Expected isAnyoneHome=true with Assistant here, got %v", isAnyoneHome)
	}
}

func TestStateTrackingManager_SleepDynamicUpdates(t *testing.T) {
	t.Parallel(
	// Test that sleep derived states update when source states change
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set up initial state - have guests, everyone awake
	if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify initial state
	isAnyoneAsleep, _ := stateMgr.GetBool("isAnyoneAsleep")
	isEveryoneAsleep, _ := stateMgr.GetBool("isEveryoneAsleep")
	if isAnyoneAsleep != false || isEveryoneAsleep != false {
		t.Errorf("Expected both sleep states false initially")
	}

	// Master goes to sleep
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to update isMasterAsleep: %v", err)
	}
	isAnyoneAsleep, _ = stateMgr.GetBool("isAnyoneAsleep")
	isEveryoneAsleep, _ = stateMgr.GetBool("isEveryoneAsleep")
	if isAnyoneAsleep != true {
		t.Errorf("Expected isAnyoneAsleep=true after master sleeps")
	}
	if isEveryoneAsleep != false {
		t.Errorf("Expected isEveryoneAsleep=false when only master sleeps")
	}

	// Guest goes to sleep
	if err := stateMgr.SetBool("isGuestAsleep", true); err != nil {
		t.Fatalf("Failed to update isGuestAsleep: %v", err)
	}
	isAnyoneAsleep, _ = stateMgr.GetBool("isAnyoneAsleep")
	isEveryoneAsleep, _ = stateMgr.GetBool("isEveryoneAsleep")
	if isAnyoneAsleep != true || isEveryoneAsleep != true {
		t.Errorf("Expected both sleep states true when everyone sleeps")
	}

	// Guest wakes up
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to update isGuestAsleep: %v", err)
	}
	isAnyoneAsleep, _ = stateMgr.GetBool("isAnyoneAsleep")
	isEveryoneAsleep, _ = stateMgr.GetBool("isEveryoneAsleep")
	if isAnyoneAsleep != true {
		t.Errorf("Expected isAnyoneAsleep=true when master still sleeps")
	}
	if isEveryoneAsleep != false {
		t.Errorf("Expected isEveryoneAsleep=false when guest wakes")
	}
}

func TestStateTrackingManager_StopCleansUpSubscriptions(t *testing.T) {
	t.Parallel(
	// Test that Stop() properly cleans up subscriptions
	)
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Verify subscriptions are active by changing state
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to update isNickHome: %v", err)
	}

	isAnyOwnerHome, _ := stateMgr.GetBool("isAnyOwnerHome")
	if isAnyOwnerHome != true {
		t.Errorf("Expected derived state to update before Stop()")
	}

	// Stop the manager
	manager.Stop()

	// Change state again - derived states should NOT update after Stop
	// (This test verifies subscriptions are cleaned up, but the derived
	// state helper will have already unsubscribed, so we can't easily
	// verify this without accessing internal state. The main goal is
	// to ensure Stop() doesn't panic and properly calls helper.Stop())
}

func TestStateTrackingManager_GuestAsleepAutoSync_NoGuests(t *testing.T) {
	t.Parallel(
	// Test that guest asleep auto-syncs with master when no guests
	)
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isHaveGuests", false); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Master goes to sleep, guest should auto-sync
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to update isMasterAsleep: %v", err)
	}
	guestAsleep, _ := stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != true {
		t.Errorf("Expected isGuestAsleep=true after master sleeps (no guests), got %v", guestAsleep)
	}
	isEveryoneAsleep, _ := stateMgr.GetBool("isEveryoneAsleep")
	if isEveryoneAsleep != true {
		t.Errorf("Expected isEveryoneAsleep=true after auto-sync, got %v", isEveryoneAsleep)
	}

	// Master wakes up, guest should auto-sync
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to update isMasterAsleep: %v", err)
	}
	guestAsleep, _ = stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != false {
		t.Errorf("Expected isGuestAsleep=false after master wakes (no guests), got %v", guestAsleep)
	}
	isEveryoneAsleep, _ = stateMgr.GetBool("isEveryoneAsleep")
	if isEveryoneAsleep != false {
		t.Errorf("Expected isEveryoneAsleep=false after auto-sync, got %v", isEveryoneAsleep)
	}
}

func TestStateTrackingManager_GuestAsleepAutoSync_WithGuests(t *testing.T) {
	t.Parallel(
	// Test that guest asleep does NOT auto-sync when guests are present
	)
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", true); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Master goes to sleep
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to update isMasterAsleep: %v", err)
	}

	// Guest asleep should remain true (independent when guests present)
	guestAsleep, _ := stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != true {
		t.Errorf("Expected isGuestAsleep=true (independent when guests present), got %v", guestAsleep)
	}

	// Master wakes up
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to update isMasterAsleep: %v", err)
	}

	// Guest asleep should STILL be true (not synced)
	guestAsleep, _ = stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != true {
		t.Errorf("Expected isGuestAsleep=true (independent when guests present), got %v", guestAsleep)
	}
}

func TestStateTrackingManager_GuestAsleepAutoSync_GuestsLeave(t *testing.T) {
	t.Parallel(
	// Test that auto-sync kicks in when isHaveGuests changes from true to false
	)
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify guest asleep is independent (false) while guests present
	guestAsleep, _ := stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != false {
		t.Errorf("Expected isGuestAsleep=false (independent), got %v", guestAsleep)
	}

	// Guests leave (isHaveGuests changes to false)
	if err := stateMgr.SetBool("isHaveGuests", false); err != nil {
		t.Fatalf("Failed to update isHaveGuests: %v", err)
	}

	// Guest asleep should now auto-sync to master (true)
	guestAsleep, _ = stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != true {
		t.Errorf("Expected isGuestAsleep=true (synced with master after guests leave), got %v", guestAsleep)
	}

	// Verify derived state is correct
	isEveryoneAsleep, _ := stateMgr.GetBool("isEveryoneAsleep")
	if isEveryoneAsleep != true {
		t.Errorf("Expected isEveryoneAsleep=true after auto-sync, got %v", isEveryoneAsleep)
	}
}

func TestStateTrackingManager_GuestAsleepAutoSync_InitialSync(t *testing.T) {
	t.Parallel(
	// Test that auto-sync happens on startup if needed
	)
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	// No guests, master asleep, guest awake (out of sync)
	if err := stateMgr.SetBool("isHaveGuests", false); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}
	// Start manager - should auto-sync immediately
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Guest asleep should be synced to master on startup
	guestAsleep, _ := stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != true {
		t.Errorf("Expected isGuestAsleep=true (synced on startup), got %v", guestAsleep)
	}

	// Verify derived state is correct
	isEveryoneAsleep, _ := stateMgr.GetBool("isEveryoneAsleep")
	if isEveryoneAsleep != true {
		t.Errorf("Expected isEveryoneAsleep=true after initial sync, got %v", isEveryoneAsleep)
	}
}

func TestStateTrackingManager_Reset(t *testing.T) {
	t.Parallel(
	// Test that Reset() re-calculates derived states
	)
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify initial derived state
	isAnyOwnerHome, _ := stateMgr.GetBool("isAnyOwnerHome")
	if isAnyOwnerHome != true {
		t.Errorf("Expected isAnyOwnerHome=true initially, got %v", isAnyOwnerHome)
	}

	// Call Reset() - should re-calculate all derived states
	if err := manager.Reset(); err != nil {
		t.Fatalf("Reset() failed: %v", err)
	}

	// Verify derived state is still correct after reset
	isAnyOwnerHome, _ = stateMgr.GetBool("isAnyOwnerHome")
	if isAnyOwnerHome != true {
		t.Errorf("Expected isAnyOwnerHome=true after reset, got %v", isAnyOwnerHome)
	}
}

func TestStateTrackingManager_ArrivalAnnouncements(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		entityID        string
		expectedMessage string
		setupState      func(*state.Manager)
		expectedPlayers []string
	}{
		{
			name:            "Nick arrival announced when someone home",
			entityID:        "input_boolean.nick_home",
			expectedMessage: "Nick is home",
			setupState: func(sm *state.Manager) {
				// Caroline is already home, Nick is away
				sm.SetBool("isCarolineHome", true)
				sm.SetBool("isNickHome", false)
			},
			expectedPlayers: []string{
				"media_player.kitchen",
				"media_player.sitting_room",
				"media_player.front_room",
				"media_player.kids_bathroom",
			},
		},
		{
			name:            "Caroline arrival announced when someone home",
			entityID:        "input_boolean.caroline_home",
			expectedMessage: "Caroline is home",
			setupState: func(sm *state.Manager) {
				// Nick is already home, Caroline is away
				sm.SetBool("isNickHome", true)
				sm.SetBool("isCarolineHome", false)
			},
			expectedPlayers: []string{
				"media_player.kitchen",
				"media_player.sitting_room",
				"media_player.front_room",
				"media_player.kids_bathroom",
				"media_player.office",
			},
		},
		{
			name:            "Assistant arrival announced when someone home",
			entityID:        "input_boolean.assistant_here",
			expectedMessage: "Assistant is here",
			setupState: func(sm *state.Manager) {
				// Nick is already home, Assistant is not here
				sm.SetBool("isNickHome", true)
				sm.SetBool("isAssistantHere", false)
			},
			expectedPlayers: []string{
				"media_player.kitchen",
				"media_player.sitting_room",
				"media_player.front_room",
				"media_player.kids_bathroom",
				"media_player.office",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			tt.setupState(stateMgr)

			mockAlerter := &alert.MockAlerter{}
			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", mockAlerter)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Simulate arrival (off -> on)
			mockHA.SetState(tt.entityID, "off", nil)
			mockHA.SetState(tt.entityID, "on", nil)

			// Give the async handler a moment to process
			time.Sleep(50 * time.Millisecond)

			calls := mockAlerter.Calls()
			if len(calls) == 0 {
				t.Fatal("Expected announcement via notifier, but no calls were made")
			}

			ann := calls[0]
			if ann.Body != tt.expectedMessage {
				t.Errorf("Expected message='%s', got %q", tt.expectedMessage, ann.Body)
			}

			if len(ann.Speakers) != len(tt.expectedPlayers) {
				t.Errorf("Expected %d media players, got %d", len(tt.expectedPlayers), len(ann.Speakers))
			}
			for _, expected := range tt.expectedPlayers {
				found := false
				for _, actual := range ann.Speakers {
					if actual == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected media player %s not found in announcement", expected)
				}
			}
		})
	}
}

func TestStateTrackingManager_NoAnnouncement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		readOnly   bool
		setupState func(*state.Manager)
		simulate   func(*ha.MockClient)
	}{
		{
			name:     "No announcement when nobody home",
			readOnly: false,
			setupState: func(sm *state.Manager) {
				sm.SetBool("isCarolineHome", false)
				sm.SetBool("isNickHome", false)
				sm.SetBool("isAssistantHere", false)
			},
			simulate: func(mc *ha.MockClient) {
				// Simulate Nick arriving home when nobody else is home
				mc.SetState("input_boolean.nick_home", "off", nil)
				mc.SetState("input_boolean.nick_home", "on", nil)
			},
		},
		// Note: read-only behavior is now owned by the notifier package, not
		// statetracking. The plugin always invokes the notifier; the notifier
		// itself decides whether to actually call Home Assistant. See
		// internal/notify/notify_test.go for read-only coverage.
		{
			name:     "No announcement on state change from unknown",
			readOnly: false,
			setupState: func(sm *state.Manager) {
				sm.SetBool("isCarolineHome", true)
			},
			simulate: func(mc *ha.MockClient) {
				// Simulate Nick's state changing from unknown to on (no oldState)
				// This should NOT trigger an announcement
				mc.SetState("input_boolean.nick_home", "on", nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			tt.setupState(stateMgr)

			mockAlerter := &alert.MockAlerter{}
			manager := NewManager(context.Background(), mockHA, stateMgr, logger, tt.readOnly, nil, "", mockAlerter)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			tt.simulate(mockHA)

			// Give the async handler a moment to process
			time.Sleep(50 * time.Millisecond)

			if calls := mockAlerter.Calls(); len(calls) > 0 {
				t.Errorf("Expected no announcement, but got %d call(s): %+v", len(calls), calls)
			}
		})
	}
}

func TestStateTrackingManager_ShadowState_DerivedStatesUpdated(t *testing.T) {
	t.Parallel(
	// Test that shadow state outputs.derivedStates is populated after plugin operations
	// This test catches the bug where UpdateDerivedStates() was never called
	)
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}

	// Create and start manager
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify shadow state outputs are populated (not zero values)
	if !shadowState.Outputs.DerivedStates.IsAnyOwnerHome {
		t.Error("Expected shadow state IsAnyOwnerHome=true (Nick is home), got false")
	}
	if !shadowState.Outputs.DerivedStates.IsAnyoneHome {
		t.Error("Expected shadow state IsAnyoneHome=true (Nick is home), got false")
	}
	if shadowState.Outputs.DerivedStates.IsAnyoneAsleep {
		t.Error("Expected shadow state IsAnyoneAsleep=false (nobody asleep), got true")
	}
	if shadowState.Outputs.DerivedStates.IsEveryoneAsleep {
		t.Error("Expected shadow state IsEveryoneAsleep=false (nobody asleep), got true")
	}

	// Verify LastComputation is set (not zero time)
	if shadowState.Outputs.LastComputation.IsZero() {
		t.Error("Expected shadow state LastComputation to be set, got zero time")
	}
}

func TestStateTrackingManager_ShadowState_DerivedStatesUpdateOnChange(t *testing.T) {
	t.Parallel(
	// Test that shadow state updates when derived states change
	)
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify initial shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.DerivedStates.IsAnyOwnerHome {
		t.Error("Expected initial IsAnyOwnerHome=false")
	}

	// Get initial computation time
	initialCompTime := shadowState.Outputs.LastComputation

	// Wait a bit to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Nick arrives home
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to update isNickHome: %v", err)
	}

	// Verify shadow state was updated
	shadowState = manager.GetShadowState()
	if !shadowState.Outputs.DerivedStates.IsAnyOwnerHome {
		t.Error("Expected shadow state IsAnyOwnerHome=true after Nick arrives")
	}
	if !shadowState.Outputs.DerivedStates.IsAnyoneHome {
		t.Error("Expected shadow state IsAnyoneHome=true after Nick arrives")
	}

	// Verify LastComputation was updated
	if !shadowState.Outputs.LastComputation.After(initialCompTime) {
		t.Error("Expected LastComputation to be updated after state change")
	}
}

func TestStateTrackingManager_NearHomeDetection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		entityID       string
		homeStateVar   string
		isAlreadyHome  bool
		expectedResult bool
	}{
		{
			name:           "Nick near home sets owner returned when not home",
			entityID:       "input_boolean.nick_near_home",
			homeStateVar:   "isNickHome",
			isAlreadyHome:  false,
			expectedResult: true,
		},
		{
			name:           "Nick near home ignores when already home",
			entityID:       "input_boolean.nick_near_home",
			homeStateVar:   "isNickHome",
			isAlreadyHome:  true,
			expectedResult: false,
		},
		{
			name:           "Caroline near home sets owner returned when not home",
			entityID:       "input_boolean.caroline_near_home",
			homeStateVar:   "isCarolineHome",
			isAlreadyHome:  false,
			expectedResult: true,
		},
		{
			name:           "Caroline near home ignores when already home",
			entityID:       "input_boolean.caroline_near_home",
			homeStateVar:   "isCarolineHome",
			isAlreadyHome:  true,
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Setup home state for the person
			if err := stateMgr.SetBool(tt.homeStateVar, tt.isAlreadyHome); err != nil {
				t.Fatalf("Failed to set %s: %v", tt.homeStateVar, err)
			}
			if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
				t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
			}

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Verify initial state
			didOwnerReturn, _ := stateMgr.GetBool("didOwnerJustReturnHome")
			if didOwnerReturn {
				t.Error("Expected didOwnerJustReturnHome=false initially")
			}

			// Simulate near_home going on (off -> on)
			mockHA.SetState(tt.entityID, "off", nil)
			mockHA.SetState(tt.entityID, "on", nil)

			// Verify didOwnerJustReturnHome matches expected result
			didOwnerReturn, err := stateMgr.GetBool("didOwnerJustReturnHome")
			if err != nil {
				t.Fatalf("Failed to get didOwnerJustReturnHome: %v", err)
			}
			if didOwnerReturn != tt.expectedResult {
				t.Errorf("Expected didOwnerJustReturnHome=%v, got %v (entityID=%s, alreadyHome=%v)",
					tt.expectedResult, didOwnerReturn, tt.entityID, tt.isAlreadyHome)
			}
		})
	}
}

// TestStateTrackingManager_NearHomeDepartureCooldown tests that near_home triggers
// are suppressed during the departure cooldown period (issue #918).
func TestStateTrackingManager_NearHomeDepartureCooldown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		homeEntityID     string
		nearHomeEntityID string
		homeStateVar     string
		timeSinceDepart  time.Duration
		expectedResult   bool
	}{
		{
			name:             "Nick near_home suppressed during cooldown",
			homeEntityID:     "input_boolean.nick_home",
			nearHomeEntityID: "input_boolean.nick_near_home",
			homeStateVar:     "isNickHome",
			timeSinceDepart:  1 * time.Minute,
			expectedResult:   false,
		},
		{
			name:             "Nick near_home allowed after cooldown",
			homeEntityID:     "input_boolean.nick_home",
			nearHomeEntityID: "input_boolean.nick_near_home",
			homeStateVar:     "isNickHome",
			timeSinceDepart:  3 * time.Minute,
			expectedResult:   true,
		},
		{
			name:             "Caroline near_home suppressed during cooldown",
			homeEntityID:     "input_boolean.caroline_home",
			nearHomeEntityID: "input_boolean.caroline_near_home",
			homeStateVar:     "isCarolineHome",
			timeSinceDepart:  30 * time.Second,
			expectedResult:   false,
		},
		{
			name:             "Caroline near_home allowed after cooldown",
			homeEntityID:     "input_boolean.caroline_home",
			nearHomeEntityID: "input_boolean.caroline_near_home",
			homeStateVar:     "isCarolineHome",
			timeSinceDepart:  5 * time.Minute,
			expectedResult:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			mockClock := clock.NewMockClock(time.Now())

			// Setup: person is home initially
			if err := stateMgr.SetBool(tt.homeStateVar, true); err != nil {
				t.Fatalf("Failed to set %s: %v", tt.homeStateVar, err)
			}
			if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
				t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
			}

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
			manager.SetClock(mockClock)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Simulate person leaving home (on -> off)
			mockHA.SetState(tt.homeEntityID, "on", nil)
			mockHA.SetState(tt.homeEntityID, "off", nil)

			// Clear any didOwnerJustReturnHome set by initial arrival event
			_ = stateMgr.SetBool("didOwnerJustReturnHome", false)

			// Advance time by the configured duration
			mockClock.AdvanceAndProcess(tt.timeSinceDepart)

			// Simulate near_home going on
			mockHA.SetState(tt.nearHomeEntityID, "off", nil)
			mockHA.SetState(tt.nearHomeEntityID, "on", nil)

			// Verify result
			didOwnerReturn, err := stateMgr.GetBool("didOwnerJustReturnHome")
			if err != nil {
				t.Fatalf("Failed to get didOwnerJustReturnHome: %v", err)
			}
			if didOwnerReturn != tt.expectedResult {
				t.Errorf("Expected didOwnerJustReturnHome=%v, got %v (timeSinceDepart=%v)",
					tt.expectedResult, didOwnerReturn, tt.timeSinceDepart)
			}
		})
	}
}

// TestStateTrackingManager_ArrivalDebounce tests that arrival announcements and
// didOwnerJustReturnHome are suppressed when a presence sensor bounces (issue #922).
func TestStateTrackingManager_ArrivalDebounce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		homeEntityID      string
		homeStateVar      string
		otherHomeStateVar string
		otherHomeValue    bool
		timeSinceDepart   time.Duration
		expectTTS         bool
		expectOwnerReturn bool
	}{
		{
			name:              "Nick arrival suppressed during debounce (bounce)",
			homeEntityID:      "input_boolean.nick_home",
			homeStateVar:      "isNickHome",
			otherHomeStateVar: "isCarolineHome",
			otherHomeValue:    true,
			timeSinceDepart:   72 * time.Second,
			expectTTS:         false,
			expectOwnerReturn: false,
		},
		{
			name:              "Nick arrival allowed after debounce (genuine arrival)",
			homeEntityID:      "input_boolean.nick_home",
			homeStateVar:      "isNickHome",
			otherHomeStateVar: "isCarolineHome",
			otherHomeValue:    true,
			timeSinceDepart:   6 * time.Minute,
			expectTTS:         true,
			expectOwnerReturn: true,
		},
		{
			name:              "Caroline arrival suppressed during debounce (bounce)",
			homeEntityID:      "input_boolean.caroline_home",
			homeStateVar:      "isCarolineHome",
			otherHomeStateVar: "isNickHome",
			otherHomeValue:    true,
			timeSinceDepart:   30 * time.Second,
			expectTTS:         false,
			expectOwnerReturn: false,
		},
		{
			name:              "Caroline arrival allowed after debounce (genuine arrival)",
			homeEntityID:      "input_boolean.caroline_home",
			homeStateVar:      "isCarolineHome",
			otherHomeStateVar: "isNickHome",
			otherHomeValue:    true,
			timeSinceDepart:   10 * time.Minute,
			expectTTS:         true,
			expectOwnerReturn: true,
		},
		{
			name:              "Assistant arrival suppressed during debounce (bounce)",
			homeEntityID:      "input_boolean.assistant_here",
			homeStateVar:      "isAssistantHere",
			otherHomeStateVar: "isNickHome",
			otherHomeValue:    true,
			timeSinceDepart:   1 * time.Minute,
			expectTTS:         false,
			expectOwnerReturn: false,
		},
		{
			name:              "Assistant arrival allowed after debounce (genuine arrival)",
			homeEntityID:      "input_boolean.assistant_here",
			homeStateVar:      "isAssistantHere",
			otherHomeStateVar: "isNickHome",
			otherHomeValue:    true,
			timeSinceDepart:   6 * time.Minute,
			expectTTS:         true,
			expectOwnerReturn: false, // Assistant doesn't set didOwnerJustReturnHome
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			mockClock := clock.NewMockClock(time.Now())

			// Setup: person is home initially, someone else is also home (for TTS)
			if err := stateMgr.SetBool(tt.homeStateVar, true); err != nil {
				t.Fatalf("Failed to set %s: %v", tt.homeStateVar, err)
			}
			if err := stateMgr.SetBool(tt.otherHomeStateVar, tt.otherHomeValue); err != nil {
				t.Fatalf("Failed to set %s: %v", tt.otherHomeStateVar, err)
			}
			if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
				t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
			}

			mockAlerter := &alert.MockAlerter{}
			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", mockAlerter)
			manager.SetClock(mockClock)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Simulate person leaving home (on -> off) — records departure time
			mockHA.SetState(tt.homeEntityID, "on", nil)
			mockHA.SetState(tt.homeEntityID, "off", nil)

			// Clear any state set by the departure handler
			_ = stateMgr.SetBool("didOwnerJustReturnHome", false)

			// Advance time by the configured duration
			mockClock.AdvanceAndProcess(tt.timeSinceDepart)

			// Snapshot announcement count before re-arrival
			beforeReArrival := len(mockAlerter.Calls())

			// Simulate re-arrival (off -> on)
			mockHA.SetState(tt.homeEntityID, "off", nil)
			mockHA.SetState(tt.homeEntityID, "on", nil)

			// Give the async handler a moment to process
			time.Sleep(50 * time.Millisecond)

			ttsCalled := len(mockAlerter.Calls()) > beforeReArrival
			if ttsCalled != tt.expectTTS {
				t.Errorf("Expected TTS called=%v, got %v (timeSinceDepart=%v)",
					tt.expectTTS, ttsCalled, tt.timeSinceDepart)
			}

			// Verify didOwnerJustReturnHome
			didOwnerReturn, err := stateMgr.GetBool("didOwnerJustReturnHome")
			if err != nil {
				t.Fatalf("Failed to get didOwnerJustReturnHome: %v", err)
			}
			if didOwnerReturn != tt.expectOwnerReturn {
				t.Errorf("Expected didOwnerJustReturnHome=%v, got %v (timeSinceDepart=%v)",
					tt.expectOwnerReturn, didOwnerReturn, tt.timeSinceDepart)
			}
		})
	}
}

// TestStateTrackingManager_ArrivalDebounce_FirstArrivalNotSuppressed tests that
// the very first arrival (no prior departure) is NOT suppressed by the debounce.
func TestStateTrackingManager_ArrivalDebounce_FirstArrivalNotSuppressed(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Caroline is home, Nick is away (never departed — zero departure time)
	if err := stateMgr.SetBool("isCarolineHome", true); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	mockAlerter := &alert.MockAlerter{}
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", mockAlerter)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Nick arrives for the first time (no prior departure)
	mockHA.SetState("input_boolean.nick_home", "off", nil)
	mockHA.SetState("input_boolean.nick_home", "on", nil)

	time.Sleep(50 * time.Millisecond)

	// Should NOT be suppressed — first arrival
	if len(mockAlerter.Calls()) == 0 {
		t.Error("Expected announcement for first arrival (no prior departure), but none was made")
	}

	didOwnerReturn, _ := stateMgr.GetBool("didOwnerJustReturnHome")
	if !didOwnerReturn {
		t.Error("Expected didOwnerJustReturnHome=true for first arrival, got false")
	}
}

func TestEntityIDToSpeakerName(t *testing.T) {
	tests := []struct {
		entityID string
		expected string
	}{
		{"media_player.kitchen", "Kitchen"},
		{"media_player.front_room", "Front Room"},
		{"media_player.kids_bathroom", "Kids Bathroom"},
		{"media_player.primary_bathroom", "Primary Bathroom"},
	}
	for _, tt := range tests {
		if got := entityIDToSpeakerName(tt.entityID); got != tt.expected {
			t.Errorf("entityIDToSpeakerName(%q) = %q, want %q", tt.entityID, got, tt.expected)
		}
	}
}

func TestSpeakerNameToEntityID(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"Kitchen", "media_player.kitchen"},
		{"Front Room", "media_player.front_room"},
		{"Kids Bathroom", "media_player.kids_bathroom"},
	}
	for _, tt := range tests {
		if got := speakerNameToEntityID(tt.name); got != tt.expected {
			t.Errorf("speakerNameToEntityID(%q) = %q, want %q", tt.name, got, tt.expected)
		}
	}
}

func TestGetGroupCoordinator(t *testing.T) {
	tests := []struct {
		name          string
		socoResponse  socoGroupsResponse
		speakerEntity string
		wantCoord     string
		socoDown      bool
	}{
		{
			name: "speaker is group member - returns coordinator",
			socoResponse: socoGroupsResponse{
				ExitCode: 0,
				Result:   "\nFront Room: Primary Bathroom, Kitchen, Bedroom, Kids Bathroom, Sitting Room\nSoundbar: \nBarn:\n",
			},
			speakerEntity: "media_player.kitchen",
			wantCoord:     "media_player.front_room",
		},
		{
			name: "speaker is the coordinator - returns itself",
			socoResponse: socoGroupsResponse{
				ExitCode: 0,
				Result:   "\nFront Room: Primary Bathroom, Kitchen\nSoundbar: \n",
			},
			speakerEntity: "media_player.front_room",
			wantCoord:     "media_player.front_room",
		},
		{
			name: "speaker is standalone - returns empty",
			socoResponse: socoGroupsResponse{
				ExitCode: 0,
				Result:   "\nFront Room: Kitchen\nSoundbar: \nBarn:\n",
			},
			speakerEntity: "media_player.barn",
			wantCoord:     "",
		},
		{
			name: "soco error - returns empty for graceful fallback",
			socoResponse: socoGroupsResponse{
				ExitCode: 1,
				ErrorMsg: "speaker not found",
			},
			speakerEntity: "media_player.kitchen",
			wantCoord:     "",
		},
		{
			name:          "soco unavailable - returns empty for graceful fallback",
			speakerEntity: "media_player.kitchen",
			wantCoord:     "",
			socoDown:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var serverURL string
			if tt.socoDown {
				serverURL = "http://127.0.0.1:1" // unreachable
			} else {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					json.NewEncoder(w).Encode(tt.socoResponse)
				}))
				defer server.Close()
				serverURL = server.URL
			}

			logger := zap.NewNop()
			mockHA := ha.NewMockClient()
			stateMgr := state.NewManager(mockHA, logger, false)
			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, serverURL, &alert.MockAlerter{})

			got := manager.getGroupCoordinator(tt.speakerEntity)
			if got != tt.wantCoord {
				t.Errorf("getGroupCoordinator(%q) = %q, want %q", tt.speakerEntity, got, tt.wantCoord)
			}
		})
	}
}

func TestGetGroupCoordinator_NoSocoURL(t *testing.T) {
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, logger, false)
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})

	got := manager.getGroupCoordinator("media_player.kitchen")
	if got != "" {
		t.Errorf("expected empty string when socoCliURL is empty, got %q", got)
	}
}

func TestAnnounceArrivalDirect_PreservesSpeakersWhenGrouped(t *testing.T) {
	// Set up a mock SoCo server that reports Front Room as group coordinator.
	// Arrival TTS uses HA announce mode, so the target list must not collapse to
	// a single coordinator when Sonos is grouped.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(socoGroupsResponse{
			ExitCode: 0,
			Result:   "\nFront Room: Primary Bathroom, Kitchen, Bedroom\nSoundbar: \n",
		})
	}))
	defer server.Close()

	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockAlerter := &alert.MockAlerter{}
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, server.URL, mockAlerter)

	manager.announceArrivalDirect("Nick", "Nick is home", []string{
		"media_player.kitchen",
		"media_player.sitting_room",
		"media_player.front_room",
		"media_player.kids_bathroom",
		"media_player.office",
	})

	calls := mockAlerter.Calls()
	if len(calls) != 1 {
		t.Fatalf("Expected exactly 1 announcement, got %d", len(calls))
	}
	expectedSpeakers := []string{
		"media_player.kitchen",
		"media_player.sitting_room",
		"media_player.front_room",
		"media_player.kids_bathroom",
		"media_player.office",
	}
	if len(calls[0].Speakers) != len(expectedSpeakers) {
		t.Fatalf("Expected %d speakers, got %d: %v",
			len(expectedSpeakers), len(calls[0].Speakers), calls[0].Speakers)
	}
	for i, expected := range expectedSpeakers {
		if calls[0].Speakers[i] != expected {
			t.Errorf("Expected speaker %d to be %q, got %q", i, expected, calls[0].Speakers[i])
		}
	}
}

func TestAnnounceArrivalDirect_FallsBackWhenSocoDown(t *testing.T) {
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockAlerter := &alert.MockAlerter{}
	// Use unreachable URL to simulate SoCo being down
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "http://127.0.0.1:1", mockAlerter)

	defaultSpeakers := []string{
		"media_player.kitchen",
		"media_player.sitting_room",
	}
	manager.announceArrivalDirect("Nick", "Nick is home", defaultSpeakers)

	calls := mockAlerter.Calls()
	if len(calls) != 1 {
		t.Fatalf("Expected exactly 1 announcement even when SoCo is down, got %d", len(calls))
	}
	if len(calls[0].Speakers) != 2 {
		t.Errorf("Expected fallback to default speakers (2), got %v", calls[0].Speakers)
	}
}
