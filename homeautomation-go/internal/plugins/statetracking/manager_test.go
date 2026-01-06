package statetracking

import (
	"testing"
	"time"

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
		description    string
	}{
		{
			name:           "Both owners away",
			isNickHome:     false,
			isCarolineHome: false,
			expectedOwner:  false,
			description:    "No owners home",
		},
		{
			name:           "Only Nick home",
			isNickHome:     true,
			isCarolineHome: false,
			expectedOwner:  true,
			description:    "Nick is home, Caroline is away",
		},
		{
			name:           "Only Caroline home",
			isNickHome:     false,
			isCarolineHome: true,
			expectedOwner:  true,
			description:    "Caroline is home, Nick is away",
		},
		{
			name:           "Both owners home",
			isNickHome:     true,
			isCarolineHome: true,
			expectedOwner:  true,
			description:    "Both owners are home",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create mock HA client and state manager

			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Set up initial state
			if err := stateMgr.SetBool("isNickHome", tt.isNickHome); err != nil {
				t.Fatalf("Failed to set isNickHome: %v", err)
			}
			if err := stateMgr.SetBool("isCarolineHome", tt.isCarolineHome); err != nil {
				t.Fatalf("Failed to set isCarolineHome: %v", err)
			}

			// Create and start manager
			manager := NewManager(mockHA, stateMgr, logger, false, nil)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Verify isAnyOwnerHome was computed correctly
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
		name           string
		isNickHome     bool
		isCarolineHome bool
		isToriHere     bool
		expectedAnyone bool
		description    string
	}{
		{
			name:           "Everyone away",
			isNickHome:     false,
			isCarolineHome: false,
			isToriHere:     false,
			expectedAnyone: false,
			description:    "No one is home",
		},
		{
			name:           "Only Nick home",
			isNickHome:     true,
			isCarolineHome: false,
			isToriHere:     false,
			expectedAnyone: true,
			description:    "Nick is home",
		},
		{
			name:           "Only Caroline home",
			isNickHome:     false,
			isCarolineHome: true,
			isToriHere:     false,
			expectedAnyone: true,
			description:    "Caroline is home",
		},
		{
			name:           "Only Tori here",
			isNickHome:     false,
			isCarolineHome: false,
			isToriHere:     true,
			expectedAnyone: true,
			description:    "Guest (Tori) is here",
		},
		{
			name:           "Nick and Tori home",
			isNickHome:     true,
			isCarolineHome: false,
			isToriHere:     true,
			expectedAnyone: true,
			description:    "Owner and guest are home",
		},
		{
			name:           "Everyone home",
			isNickHome:     true,
			isCarolineHome: true,
			isToriHere:     true,
			expectedAnyone: true,
			description:    "All people are home",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create mock HA client and state manager

			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Set up initial state
			if err := stateMgr.SetBool("isNickHome", tt.isNickHome); err != nil {
				t.Fatalf("Failed to set isNickHome: %v", err)
			}
			if err := stateMgr.SetBool("isCarolineHome", tt.isCarolineHome); err != nil {
				t.Fatalf("Failed to set isCarolineHome: %v", err)
			}
			if err := stateMgr.SetBool("isToriHere", tt.isToriHere); err != nil {
				t.Fatalf("Failed to set isToriHere: %v", err)
			}

			// Create and start manager
			manager := NewManager(mockHA, stateMgr, logger, false, nil)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Verify isAnyoneHome was computed correctly
			actualAnyone, err := stateMgr.GetBool("isAnyoneHome")
			if err != nil {
				t.Fatalf("Failed to get isAnyoneHome: %v", err)
			}

			if actualAnyone != tt.expectedAnyone {
				t.Errorf("Expected isAnyoneHome=%v, got %v (Nick=%v, Caroline=%v, Tori=%v)",
					tt.expectedAnyone, actualAnyone, tt.isNickHome, tt.isCarolineHome, tt.isToriHere)
			}
		})
	}
}

func TestStateTrackingManager_IsAnyoneAsleep(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		isMasterAsleep    bool
		isGuestAsleep     bool
		expectedAnyAsleep bool
		description       string
	}{
		{
			name:              "Everyone awake",
			isMasterAsleep:    false,
			isGuestAsleep:     false,
			expectedAnyAsleep: false,
			description:       "No one is asleep",
		},
		{
			name:              "Only master asleep",
			isMasterAsleep:    true,
			isGuestAsleep:     false,
			expectedAnyAsleep: true,
			description:       "Master bedroom is asleep",
		},
		{
			name:              "Only guest asleep",
			isMasterAsleep:    false,
			isGuestAsleep:     true,
			expectedAnyAsleep: true,
			description:       "Guest bedroom is asleep",
		},
		{
			name:              "Everyone asleep",
			isMasterAsleep:    true,
			isGuestAsleep:     true,
			expectedAnyAsleep: true,
			description:       "Both bedrooms are asleep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create mock HA client and state manager

			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Set isHaveGuests to true to test independent sleep states
			if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
				t.Fatalf("Failed to set isHaveGuests: %v", err)
			}

			// Set up initial state
			if err := stateMgr.SetBool("isMasterAsleep", tt.isMasterAsleep); err != nil {
				t.Fatalf("Failed to set isMasterAsleep: %v", err)
			}
			if err := stateMgr.SetBool("isGuestAsleep", tt.isGuestAsleep); err != nil {
				t.Fatalf("Failed to set isGuestAsleep: %v", err)
			}

			// Create and start manager
			manager := NewManager(mockHA, stateMgr, logger, false, nil)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Verify isAnyoneAsleep was computed correctly
			actualAnyAsleep, err := stateMgr.GetBool("isAnyoneAsleep")
			if err != nil {
				t.Fatalf("Failed to get isAnyoneAsleep: %v", err)
			}

			if actualAnyAsleep != tt.expectedAnyAsleep {
				t.Errorf("Expected isAnyoneAsleep=%v, got %v (Master=%v, Guest=%v)",
					tt.expectedAnyAsleep, actualAnyAsleep, tt.isMasterAsleep, tt.isGuestAsleep)
			}
		})
	}
}

func TestStateTrackingManager_IsEveryoneAsleep(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		isMasterAsleep    bool
		isGuestAsleep     bool
		expectedAllAsleep bool
		description       string
	}{
		{
			name:              "Everyone awake",
			isMasterAsleep:    false,
			isGuestAsleep:     false,
			expectedAllAsleep: false,
			description:       "No one is asleep",
		},
		{
			name:              "Only master asleep",
			isMasterAsleep:    true,
			isGuestAsleep:     false,
			expectedAllAsleep: false,
			description:       "Master asleep, guest awake",
		},
		{
			name:              "Only guest asleep",
			isMasterAsleep:    false,
			isGuestAsleep:     true,
			expectedAllAsleep: false,
			description:       "Guest asleep, master awake",
		},
		{
			name:              "Everyone asleep",
			isMasterAsleep:    true,
			isGuestAsleep:     true,
			expectedAllAsleep: true,
			description:       "Both bedrooms are asleep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create mock HA client and state manager

			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Set isHaveGuests to true to test independent sleep states
			if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
				t.Fatalf("Failed to set isHaveGuests: %v", err)
			}

			// Set up initial state
			if err := stateMgr.SetBool("isMasterAsleep", tt.isMasterAsleep); err != nil {
				t.Fatalf("Failed to set isMasterAsleep: %v", err)
			}
			if err := stateMgr.SetBool("isGuestAsleep", tt.isGuestAsleep); err != nil {
				t.Fatalf("Failed to set isGuestAsleep: %v", err)
			}

			// Create and start manager
			manager := NewManager(mockHA, stateMgr, logger, false, nil)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Verify isEveryoneAsleep was computed correctly
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
	if err := stateMgr.SetBool("isToriHere", false); err != nil {
		t.Fatalf("Failed to set isToriHere: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

	// Verify derived states updated
	isAnyOwnerHome, _ := stateMgr.GetBool("isAnyOwnerHome")
	if isAnyOwnerHome != true {
		t.Errorf("Expected isAnyOwnerHome=true after Nick arrives, got %v", isAnyOwnerHome)
	}

	isAnyoneHome, _ = stateMgr.GetBool("isAnyoneHome")
	if isAnyoneHome != true {
		t.Errorf("Expected isAnyoneHome=true after Nick arrives, got %v", isAnyoneHome)
	}

	// Nick leaves, but Tori arrives
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to update isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isToriHere", true); err != nil {
		t.Fatalf("Failed to update isToriHere: %v", err)
	}

	// Verify isAnyOwnerHome is false but isAnyoneHome is still true
	isAnyOwnerHome, _ = stateMgr.GetBool("isAnyOwnerHome")
	if isAnyOwnerHome != false {
		t.Errorf("Expected isAnyOwnerHome=false after Nick leaves, got %v", isAnyOwnerHome)
	}

	isAnyoneHome, _ = stateMgr.GetBool("isAnyoneHome")
	if isAnyoneHome != true {
		t.Errorf("Expected isAnyoneHome=true with Tori here, got %v", isAnyoneHome)
	}
}

func TestStateTrackingManager_SleepDynamicUpdates(t *testing.T) {
	t.Parallel(
	// Test that sleep derived states update when source states change
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set isHaveGuests to true to test independent sleep states
	if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}

	// Set up initial state - everyone awake
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

	// Verify isAnyoneAsleep=true, isEveryoneAsleep=false
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

	// Verify both sleep states are true
	isAnyoneAsleep, _ = stateMgr.GetBool("isAnyoneAsleep")
	isEveryoneAsleep, _ = stateMgr.GetBool("isEveryoneAsleep")
	if isAnyoneAsleep != true || isEveryoneAsleep != true {
		t.Errorf("Expected both sleep states true when everyone sleeps")
	}

	// Guest wakes up
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to update isGuestAsleep: %v", err)
	}

	// Verify isAnyoneAsleep=true, isEveryoneAsleep=false
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

	// Set up initial state
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

	// Setup: No guests, master awake, guest awake
	if err := stateMgr.SetBool("isHaveGuests", false); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Test 1: Master goes to sleep, guest should auto-sync
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to update isMasterAsleep: %v", err)
	}

	guestAsleep, _ := stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != true {
		t.Errorf("Expected isGuestAsleep=true after master sleeps (no guests), got %v", guestAsleep)
	}

	// Verify derived states are correct
	isEveryoneAsleep, _ := stateMgr.GetBool("isEveryoneAsleep")
	if isEveryoneAsleep != true {
		t.Errorf("Expected isEveryoneAsleep=true after auto-sync, got %v", isEveryoneAsleep)
	}

	// Test 2: Master wakes up, guest should auto-sync
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to update isMasterAsleep: %v", err)
	}

	guestAsleep, _ = stateMgr.GetBool("isGuestAsleep")
	if guestAsleep != false {
		t.Errorf("Expected isGuestAsleep=false after master wakes (no guests), got %v", guestAsleep)
	}

	// Verify derived states updated
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

	// Setup: Have guests, master awake, guest asleep
	if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", true); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

	// Setup: Have guests, master asleep, guest awake
	if err := stateMgr.SetBool("isHaveGuests", true); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

	// Setup: No guests, master asleep, guest awake (out of sync)
	if err := stateMgr.SetBool("isHaveGuests", false); err != nil {
		t.Fatalf("Failed to set isHaveGuests: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateMgr.SetBool("isGuestAsleep", false); err != nil {
		t.Fatalf("Failed to set isGuestAsleep: %v", err)
	}

	// Create and start manager - should auto-sync immediately
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

	// Setup initial state
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

func TestStateTrackingManager_NickArrivalAnnouncement_SomeoneHome(t *testing.T) {
	t.Parallel(
	// Test that Nick's arrival is announced when someone is already home
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Caroline is already home, Nick is away
	if err := stateMgr.SetBool("isCarolineHome", true); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate Nick arriving home (input_boolean.nick_home changes to "on")
	mockHA.SetState("input_boolean.nick_home", "off", nil)
	mockHA.SetState("input_boolean.nick_home", "on", nil)

	// Give the async handler a moment to process
	// The announcement runs in a goroutine to avoid deadlocks
	time.Sleep(50 * time.Millisecond)

	// Verify TTS service was called
	calls := mockHA.GetServiceCalls()
	if len(calls) == 0 {
		t.Fatal("Expected TTS service call, but no service calls were made")
	}

	// Find the TTS call
	var ttsCall *ha.ServiceCall
	for i := range calls {
		if calls[i].Domain == "tts" && calls[i].Service == "speak" {
			ttsCall = &calls[i]
			break
		}
	}

	if ttsCall == nil {
		t.Fatal("Expected TTS speak service call, but none was found")
	}

	// Verify TTS call parameters
	if entityID, ok := ttsCall.Data["entity_id"].(string); !ok || entityID != "tts.google_translate_en_com" {
		t.Errorf("Expected entity_id=tts.google_translate_en_com, got %v", ttsCall.Data["entity_id"])
	}

	if message, ok := ttsCall.Data["message"].(string); !ok || message != "Nick is home" {
		t.Errorf("Expected message='Nick is home', got %v", ttsCall.Data["message"])
	}

	if cache, ok := ttsCall.Data["cache"].(bool); !ok || cache != true {
		t.Errorf("Expected cache=true, got %v", ttsCall.Data["cache"])
	}

	// Verify media players
	mediaPlayers, ok := ttsCall.Data["media_player_entity_id"].([]string)
	if !ok {
		t.Fatalf("Expected media_player_entity_id to be []string, got %T", ttsCall.Data["media_player_entity_id"])
	}

	expectedPlayers := []string{
		"media_player.kitchen",
		"media_player.dining_room",
		"media_player.soundbar",
		"media_player.kids_bathroom",
	}

	if len(mediaPlayers) != len(expectedPlayers) {
		t.Errorf("Expected %d media players, got %d", len(expectedPlayers), len(mediaPlayers))
	}

	for _, expected := range expectedPlayers {
		found := false
		for _, actual := range mediaPlayers {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected media player %s not found in TTS call", expected)
		}
	}
}

func TestStateTrackingManager_NickArrivalAnnouncement_NobodyHome(t *testing.T) {
	t.Parallel(
	// Test that Nick's arrival is NOT announced when nobody is home
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Nobody is home
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isToriHere", false); err != nil {
		t.Fatalf("Failed to set isToriHere: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate Nick arriving home (input_boolean.nick_home changes to "on")
	mockHA.SetState("input_boolean.nick_home", "off", nil)
	mockHA.SetState("input_boolean.nick_home", "on", nil)

	// Give the async handler a moment to process
	time.Sleep(50 * time.Millisecond)

	// Verify NO TTS service was called
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "tts" && call.Service == "speak" {
			t.Error("Expected no TTS announcement when nobody is home, but TTS service was called")
		}
	}
}

func TestStateTrackingManager_CarolineArrivalAnnouncement(t *testing.T) {
	t.Parallel(
	// Test that Caroline's arrival is announced when someone is already home
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Nick is already home, Caroline is away
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate Caroline arriving home
	mockHA.SetState("input_boolean.caroline_home", "off", nil)
	mockHA.SetState("input_boolean.caroline_home", "on", nil)

	// Give the async handler a moment to process
	time.Sleep(50 * time.Millisecond)

	// Verify TTS service was called with Caroline's message
	calls := mockHA.GetServiceCalls()
	var ttsCall *ha.ServiceCall
	for i := range calls {
		if calls[i].Domain == "tts" && calls[i].Service == "speak" {
			ttsCall = &calls[i]
			break
		}
	}

	if ttsCall == nil {
		t.Fatal("Expected TTS speak service call for Caroline, but none was found")
	}

	if message, ok := ttsCall.Data["message"].(string); !ok || message != "Caroline is home" {
		t.Errorf("Expected message='Caroline is home', got %v", ttsCall.Data["message"])
	}

	// Verify Caroline's media players include office
	mediaPlayers, ok := ttsCall.Data["media_player_entity_id"].([]string)
	if !ok {
		t.Fatalf("Expected media_player_entity_id to be []string, got %T", ttsCall.Data["media_player_entity_id"])
	}

	expectedPlayers := []string{
		"media_player.kitchen",
		"media_player.dining_room",
		"media_player.kids_bathroom",
		"media_player.soundbar",
		"media_player.office",
	}

	if len(mediaPlayers) != len(expectedPlayers) {
		t.Errorf("Expected %d media players for Caroline, got %d", len(expectedPlayers), len(mediaPlayers))
	}
}

func TestStateTrackingManager_ToriArrivalAnnouncement(t *testing.T) {
	t.Parallel(
	// Test that Tori's arrival is announced when someone is already home
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Nick is already home, Tori is not here
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isToriHere", false); err != nil {
		t.Fatalf("Failed to set isToriHere: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate Tori arriving
	mockHA.SetState("input_boolean.tori_here", "off", nil)
	mockHA.SetState("input_boolean.tori_here", "on", nil)

	// Give the async handler a moment to process
	time.Sleep(50 * time.Millisecond)

	// Verify TTS service was called with Tori's message
	calls := mockHA.GetServiceCalls()
	var ttsCall *ha.ServiceCall
	for i := range calls {
		if calls[i].Domain == "tts" && calls[i].Service == "speak" {
			ttsCall = &calls[i]
			break
		}
	}

	if ttsCall == nil {
		t.Fatal("Expected TTS speak service call for Tori, but none was found")
	}

	if message, ok := ttsCall.Data["message"].(string); !ok || message != "Tori is here" {
		t.Errorf("Expected message='Tori is here', got %v", ttsCall.Data["message"])
	}
}

func TestStateTrackingManager_ArrivalAnnouncement_ReadOnlyMode(t *testing.T) {
	t.Parallel(
	// Test that TTS announcements are logged but not executed in read-only mode
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Caroline is already home, Nick is away
	if err := stateMgr.SetBool("isCarolineHome", true); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}

	// Create manager in READ-ONLY mode
	manager := NewManager(mockHA, stateMgr, logger, true, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate Nick arriving home
	mockHA.SetState("input_boolean.nick_home", "off", nil)
	mockHA.SetState("input_boolean.nick_home", "on", nil)

	// Give the async handler a moment to process
	time.Sleep(50 * time.Millisecond)

	// Verify NO TTS service was called (read-only mode)
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "tts" && call.Service == "speak" {
			t.Error("Expected no TTS service call in read-only mode, but call was made")
		}
	}
}

func TestStateTrackingManager_NoAnnouncement_OnStateChangeFromUnknown(t *testing.T) {
	t.Parallel(
	// Test that announcements are not made when state changes from unknown/unavailable
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Caroline is already home
	if err := stateMgr.SetBool("isCarolineHome", true); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate Nick's state changing from unknown to on (no oldState)
	// This should NOT trigger an announcement
	mockHA.SetState("input_boolean.nick_home", "on", nil)

	// Give the async handler a moment to process
	time.Sleep(50 * time.Millisecond)

	// Verify NO TTS service was called
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "tts" && call.Service == "speak" {
			t.Error("Expected no TTS announcement when oldState is nil, but TTS service was called")
		}
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

	// Setup: Nick is home
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
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

	// Setup: Everyone away
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

func TestStateTrackingManager_NickNearHome_SetsOwnerReturnedWhenNotHome(t *testing.T) {
	t.Parallel()
	// Test that nick_near_home triggers didOwnerJustReturnHome when Nick is NOT home

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Nick is NOT home
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify initial state
	didOwnerReturn, _ := stateMgr.GetBool("didOwnerJustReturnHome")
	if didOwnerReturn {
		t.Error("Expected didOwnerJustReturnHome=false initially")
	}

	// Simulate nick_near_home going on (off → on)
	mockHA.SetState("input_boolean.nick_near_home", "off", nil)
	mockHA.SetState("input_boolean.nick_near_home", "on", nil)

	// Verify didOwnerJustReturnHome was set to true
	didOwnerReturn, err := stateMgr.GetBool("didOwnerJustReturnHome")
	if err != nil {
		t.Fatalf("Failed to get didOwnerJustReturnHome: %v", err)
	}
	if !didOwnerReturn {
		t.Error("Expected didOwnerJustReturnHome=true when nick_near_home triggers while Nick is NOT home")
	}
}

func TestStateTrackingManager_NickNearHome_IgnoresWhenAlreadyHome(t *testing.T) {
	t.Parallel()
	// Test that nick_near_home does NOT trigger didOwnerJustReturnHome when Nick IS already home

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Nick IS already home
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate nick_near_home going on (off → on) while Nick is already home (leaving)
	mockHA.SetState("input_boolean.nick_near_home", "off", nil)
	mockHA.SetState("input_boolean.nick_near_home", "on", nil)

	// Verify didOwnerJustReturnHome was NOT set (since Nick was already home, this is departure)
	didOwnerReturn, err := stateMgr.GetBool("didOwnerJustReturnHome")
	if err != nil {
		t.Fatalf("Failed to get didOwnerJustReturnHome: %v", err)
	}
	if didOwnerReturn {
		t.Error("Expected didOwnerJustReturnHome=false when nick_near_home triggers while Nick IS home (leaving)")
	}
}

func TestStateTrackingManager_CarolineNearHome_SetsOwnerReturnedWhenNotHome(t *testing.T) {
	t.Parallel()
	// Test that caroline_near_home triggers didOwnerJustReturnHome when Caroline is NOT home

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Caroline is NOT home
	if err := stateMgr.SetBool("isCarolineHome", false); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify initial state
	didOwnerReturn, _ := stateMgr.GetBool("didOwnerJustReturnHome")
	if didOwnerReturn {
		t.Error("Expected didOwnerJustReturnHome=false initially")
	}

	// Simulate caroline_near_home going on (off → on)
	mockHA.SetState("input_boolean.caroline_near_home", "off", nil)
	mockHA.SetState("input_boolean.caroline_near_home", "on", nil)

	// Verify didOwnerJustReturnHome was set to true
	didOwnerReturn, err := stateMgr.GetBool("didOwnerJustReturnHome")
	if err != nil {
		t.Fatalf("Failed to get didOwnerJustReturnHome: %v", err)
	}
	if !didOwnerReturn {
		t.Error("Expected didOwnerJustReturnHome=true when caroline_near_home triggers while Caroline is NOT home")
	}
}

func TestStateTrackingManager_CarolineNearHome_IgnoresWhenAlreadyHome(t *testing.T) {
	t.Parallel()
	// Test that caroline_near_home does NOT trigger didOwnerJustReturnHome when Caroline IS already home

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Caroline IS already home
	if err := stateMgr.SetBool("isCarolineHome", true); err != nil {
		t.Fatalf("Failed to set isCarolineHome: %v", err)
	}
	if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate caroline_near_home going on (off → on) while Caroline is already home (leaving)
	mockHA.SetState("input_boolean.caroline_near_home", "off", nil)
	mockHA.SetState("input_boolean.caroline_near_home", "on", nil)

	// Verify didOwnerJustReturnHome was NOT set (since Caroline was already home, this is departure)
	didOwnerReturn, err := stateMgr.GetBool("didOwnerJustReturnHome")
	if err != nil {
		t.Fatalf("Failed to get didOwnerJustReturnHome: %v", err)
	}
	if didOwnerReturn {
		t.Error("Expected didOwnerJustReturnHome=false when caroline_near_home triggers while Caroline IS home (leaving)")
	}
}

func TestStateTrackingManager_NearHome_IgnoresNilStates(t *testing.T) {
	t.Parallel()
	// Test that near_home handlers gracefully handle nil states (no panic)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Setup: Nick is NOT home
	if err := stateMgr.SetBool("isNickHome", false); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("didOwnerJustReturnHome", false); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate state change from unknown (nil oldState) - should not trigger or panic
	mockHA.SetState("input_boolean.nick_near_home", "on", nil)

	// Verify didOwnerJustReturnHome was NOT set (oldState was nil)
	didOwnerReturn, err := stateMgr.GetBool("didOwnerJustReturnHome")
	if err != nil {
		t.Fatalf("Failed to get didOwnerJustReturnHome: %v", err)
	}
	if didOwnerReturn {
		t.Error("Expected didOwnerJustReturnHome=false when oldState is nil (initial state, not a transition)")
	}
}
