package state

import (
	"go.uber.org/zap"
)

// Helper functions for derived state computation
// These implement the logic from Node-RED's State Tracking tab

// DerivedStatesCallback is called when derived states are computed.
// Parameters: isAnyOwnerHome, isAnyoneHome, isAnyoneAsleep, isEveryoneAsleep
type DerivedStatesCallback func(anyOwnerHome, anyoneHome, anyoneAsleep, everyoneAsleep bool)

// DerivedStateHelper manages automatic computation of derived states
type DerivedStateHelper struct {
	manager  *Manager
	logger   *zap.Logger
	subs     []Subscription
	onUpdate DerivedStatesCallback
}

// NewDerivedStateHelper creates a new helper for managing derived states
func NewDerivedStateHelper(manager *Manager, logger *zap.Logger) *DerivedStateHelper {
	return &DerivedStateHelper{
		manager: manager,
		logger:  logger,
		subs:    make([]Subscription, 0),
	}
}

// SetUpdateCallback sets a callback that is invoked whenever derived states are computed.
// The callback receives the current values of all four derived states.
func (h *DerivedStateHelper) SetUpdateCallback(cb DerivedStatesCallback) {
	h.onUpdate = cb
}

// Start begins monitoring and updating derived states
func (h *DerivedStateHelper) Start() error {
	h.logger.Info("Starting derived state helper")

	// Subscribe to presence changes to update isAnyOwnerHome and isAnyoneHome
	if err := h.setupPresenceTracking(); err != nil {
		return err
	}

	// Subscribe to sleep state changes to update isAnyoneAsleep and isEveryoneAsleep
	if err := h.setupSleepTracking(); err != nil {
		return err
	}

	// Subscribe for auto-sleep detection
	if err := h.setupAutoSleepDetection(); err != nil {
		return err
	}

	// Subscribe for guest asleep auto-sync (when no guests, mirrors master)
	if err := h.setupGuestAsleepAutoSync(); err != nil {
		return err
	}

	// Initialize derived states immediately
	h.updateIsAnyOwnerHome()
	h.updateIsAnyoneHome()
	h.updateIsAnyoneAsleep()
	h.updateIsEveryoneAsleep()
	h.syncGuestAsleepIfNoGuests() // Initialize auto-sync

	// Ensure shadow state is populated even if no values changed during init
	h.notifyCallback()

	h.logger.Info("Derived state helper started")
	return nil
}

// Stop unsubscribes from all state changes
func (h *DerivedStateHelper) Stop() {
	h.logger.Info("Stopping derived state helper")
	for _, sub := range h.subs {
		sub.Unsubscribe()
	}
	h.subs = nil
}

// notifyCallback invokes the update callback with current derived state values.
// This should be called after any derived state is updated.
func (h *DerivedStateHelper) notifyCallback() {
	if h.onUpdate == nil {
		return
	}

	// Gather current values of all derived states
	isAnyOwnerHome, _ := h.manager.GetBool("isAnyOwnerHome")
	isAnyoneHome, _ := h.manager.GetBool("isAnyoneHome")
	isAnyoneAsleep, _ := h.manager.GetBool("isAnyoneAsleep")
	isEveryoneAsleep, _ := h.manager.GetBool("isEveryoneAsleep")

	h.onUpdate(isAnyOwnerHome, isAnyoneHome, isAnyoneAsleep, isEveryoneAsleep)
}

// setupPresenceTracking subscribes to presence changes and updates isAnyOwnerHome and isAnyoneHome
func (h *DerivedStateHelper) setupPresenceTracking() error {
	// Subscribe to Nick's presence
	sub1, err := h.manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Nick's presence changed", zap.Any("old", oldValue), zap.Any("new", newValue))
		h.updateIsAnyOwnerHome()
		h.updateIsAnyoneHome()
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub1)

	// Subscribe to Caroline's presence
	sub2, err := h.manager.Subscribe("isCarolineHome", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Caroline's presence changed", zap.Any("old", oldValue), zap.Any("new", newValue))
		h.updateIsAnyOwnerHome()
		h.updateIsAnyoneHome()
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub2)

	// Subscribe to Tori's presence
	sub3, err := h.manager.Subscribe("isToriHere", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Tori's presence changed", zap.Any("old", oldValue), zap.Any("new", newValue))
		h.updateIsAnyoneHome()
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub3)

	return nil
}

// setupSleepTracking subscribes to sleep state changes and updates isAnyoneAsleep and isEveryoneAsleep
func (h *DerivedStateHelper) setupSleepTracking() error {
	// Subscribe to master bedroom sleep state
	sub1, err := h.manager.Subscribe("isMasterAsleep", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Master sleep state changed", zap.Any("old", oldValue), zap.Any("new", newValue))
		h.updateIsAnyoneAsleep()
		h.updateIsEveryoneAsleep()
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub1)

	// Subscribe to guest bedroom sleep state
	sub2, err := h.manager.Subscribe("isGuestAsleep", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Guest sleep state changed", zap.Any("old", oldValue), zap.Any("new", newValue))
		h.updateIsAnyoneAsleep()
		h.updateIsEveryoneAsleep()
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub2)

	return nil
}

// setupAutoSleepDetection implements automatic guest sleep detection
// Logic: Guest falls asleep when door closes if:
// - Someone is home
// - Guest not already marked asleep
// - Have guests
// - Guest bedroom door is closed
func (h *DerivedStateHelper) setupAutoSleepDetection() error {
	// Subscribe to guest bedroom door state
	sub, err := h.manager.Subscribe("isGuestBedroomDoorOpen", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Guest bedroom door state changed", zap.Any("old", oldValue), zap.Any("new", newValue))

		// Door just closed (was open, now closed)
		wasOpen, wasOpenOk := oldValue.(bool)
		isClosed, isClosedOk := newValue.(bool)

		if wasOpenOk && isClosedOk && wasOpen && !isClosed {
			h.checkAutoGuestSleep()
		}
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub)

	return nil
}

// updateIsAnyOwnerHome computes: isAnyOwnerHome = isNickHome OR isCarolineHome
func (h *DerivedStateHelper) updateIsAnyOwnerHome() {
	isNickHome, err1 := h.manager.GetBool("isNickHome")
	isCarolineHome, err2 := h.manager.GetBool("isCarolineHome")

	if err1 != nil || err2 != nil {
		h.logger.Error("Failed to get owner presence states",
			zap.Error(err1),
			zap.Error(err2))
		return
	}

	isAnyOwnerHome := isNickHome || isCarolineHome

	// Get current value to check if it changed
	currentValue, _ := h.manager.GetBool("isAnyOwnerHome")
	if currentValue == isAnyOwnerHome {
		return // No change
	}

	if err := h.manager.SetBool("isAnyOwnerHome", isAnyOwnerHome); err != nil {
		h.logger.Error("Failed to update isAnyOwnerHome", zap.Error(err))
		return
	}

	h.logger.Info("Updated isAnyOwnerHome",
		zap.Bool("isNickHome", isNickHome),
		zap.Bool("isCarolineHome", isCarolineHome),
		zap.Bool("isAnyOwnerHome", isAnyOwnerHome))

	// Notify callback with updated derived states
	h.notifyCallback()
}

// updateIsAnyoneHome computes: isAnyoneHome = isAnyOwnerHome OR isToriHere
func (h *DerivedStateHelper) updateIsAnyoneHome() {
	isAnyOwnerHome, err1 := h.manager.GetBool("isAnyOwnerHome")
	isToriHere, err2 := h.manager.GetBool("isToriHere")

	if err1 != nil || err2 != nil {
		h.logger.Error("Failed to get presence states",
			zap.Error(err1),
			zap.Error(err2))
		return
	}

	isAnyoneHome := isAnyOwnerHome || isToriHere

	// Get current value to check if it changed
	currentValue, _ := h.manager.GetBool("isAnyoneHome")
	if currentValue == isAnyoneHome {
		return // No change
	}

	if err := h.manager.SetBool("isAnyoneHome", isAnyoneHome); err != nil {
		h.logger.Error("Failed to update isAnyoneHome", zap.Error(err))
		return
	}

	h.logger.Info("Updated isAnyoneHome",
		zap.Bool("isAnyOwnerHome", isAnyOwnerHome),
		zap.Bool("isToriHere", isToriHere),
		zap.Bool("isAnyoneHome", isAnyoneHome))

	// Notify callback with updated derived states
	h.notifyCallback()
}

// updateIsAnyoneAsleep computes: isAnyoneAsleep = isMasterAsleep OR isGuestAsleep
func (h *DerivedStateHelper) updateIsAnyoneAsleep() {
	isMasterAsleep, err1 := h.manager.GetBool("isMasterAsleep")
	isGuestAsleep, err2 := h.manager.GetBool("isGuestAsleep")

	if err1 != nil || err2 != nil {
		h.logger.Error("Failed to get sleep states for isAnyoneAsleep",
			zap.Error(err1),
			zap.Error(err2))
		return
	}

	isAnyoneAsleep := isMasterAsleep || isGuestAsleep

	// Get current value to check if it changed
	currentValue, _ := h.manager.GetBool("isAnyoneAsleep")
	if currentValue == isAnyoneAsleep {
		return // No change
	}

	if err := h.manager.SetBool("isAnyoneAsleep", isAnyoneAsleep); err != nil {
		h.logger.Error("Failed to update isAnyoneAsleep", zap.Error(err))
		return
	}

	h.logger.Info("Updated isAnyoneAsleep",
		zap.Bool("isMasterAsleep", isMasterAsleep),
		zap.Bool("isGuestAsleep", isGuestAsleep),
		zap.Bool("isAnyoneAsleep", isAnyoneAsleep))

	// Notify callback with updated derived states
	h.notifyCallback()
}

// updateIsEveryoneAsleep computes: isEveryoneAsleep = isMasterAsleep AND isGuestAsleep
func (h *DerivedStateHelper) updateIsEveryoneAsleep() {
	isMasterAsleep, err1 := h.manager.GetBool("isMasterAsleep")
	isGuestAsleep, err2 := h.manager.GetBool("isGuestAsleep")

	if err1 != nil || err2 != nil {
		h.logger.Error("Failed to get sleep states",
			zap.Error(err1),
			zap.Error(err2))
		return
	}

	isEveryoneAsleep := isMasterAsleep && isGuestAsleep

	// Get current value to check if it changed
	currentValue, _ := h.manager.GetBool("isEveryoneAsleep")
	if currentValue == isEveryoneAsleep {
		return // No change
	}

	if err := h.manager.SetBool("isEveryoneAsleep", isEveryoneAsleep); err != nil {
		h.logger.Error("Failed to update isEveryoneAsleep", zap.Error(err))
		return
	}

	h.logger.Info("Updated isEveryoneAsleep",
		zap.Bool("isMasterAsleep", isMasterAsleep),
		zap.Bool("isGuestAsleep", isGuestAsleep),
		zap.Bool("isEveryoneAsleep", isEveryoneAsleep))

	// Notify callback with updated derived states
	h.notifyCallback()
}

// checkAutoGuestSleep checks if guest should be automatically marked as asleep
func (h *DerivedStateHelper) checkAutoGuestSleep() {
	// Applicability checks
	isAnyoneHome, _ := h.manager.GetBool("isAnyoneHome")
	if !isAnyoneHome {
		h.logger.Debug("Auto-sleep: No one home")
		return
	}

	isGuestAsleep, _ := h.manager.GetBool("isGuestAsleep")
	if isGuestAsleep {
		h.logger.Debug("Auto-sleep: Guest already marked asleep")
		return
	}

	isHaveGuests, _ := h.manager.GetBool("isHaveGuests")
	if !isHaveGuests {
		h.logger.Debug("Auto-sleep: No guests present")
		return
	}

	isGuestBedroomDoorOpen, _ := h.manager.GetBool("isGuestBedroomDoorOpen")
	if isGuestBedroomDoorOpen {
		h.logger.Debug("Auto-sleep: Guest bedroom door is open")
		return
	}

	// All conditions met - mark guest as asleep
	h.logger.Info("Auto-detecting guest sleep (door closed)")
	if err := h.manager.SetBool("isGuestAsleep", true); err != nil {
		h.logger.Error("Failed to auto-set guest asleep", zap.Error(err))
	}
}

// setupGuestAsleepAutoSync subscribes to changes that should trigger guest asleep auto-sync
// Logic: When isHaveGuests is false, isGuestAsleep should mirror isMasterAsleep
// This matches Node-RED behavior in flows.json:2366-2396
func (h *DerivedStateHelper) setupGuestAsleepAutoSync() error {
	// Subscribe to master bedroom sleep state changes
	sub1, err := h.manager.Subscribe("isMasterAsleep", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Master sleep state changed (for guest auto-sync)", zap.Any("old", oldValue), zap.Any("new", newValue))
		h.syncGuestAsleepIfNoGuests()
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub1)

	// Subscribe to isHaveGuests changes
	sub2, err := h.manager.Subscribe("isHaveGuests", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Have guests state changed (for guest auto-sync)", zap.Any("old", oldValue), zap.Any("new", newValue))
		h.syncGuestAsleepIfNoGuests()
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub2)

	// Subscribe to guest asleep changes (to handle edge cases)
	sub3, err := h.manager.Subscribe("isGuestAsleep", func(key string, oldValue, newValue interface{}) {
		h.logger.Debug("Guest sleep state changed (for guest auto-sync)", zap.Any("old", oldValue), zap.Any("new", newValue))
		h.syncGuestAsleepIfNoGuests()
	})
	if err != nil {
		return err
	}
	h.subs = append(h.subs, sub3)

	return nil
}

// syncGuestAsleepIfNoGuests auto-syncs isGuestAsleep with isMasterAsleep when no guests are present
// This implements the Node-RED logic: "If we don't have guests, they're asleep if master is asleep"
func (h *DerivedStateHelper) syncGuestAsleepIfNoGuests() {
	isHaveGuests, err := h.manager.GetBool("isHaveGuests")
	if err != nil {
		h.logger.Error("Failed to get isHaveGuests for auto-sync", zap.Error(err))
		return
	}

	// Only auto-sync when there are NO guests
	if isHaveGuests {
		return
	}

	// Get master sleep state
	isMasterAsleep, err := h.manager.GetBool("isMasterAsleep")
	if err != nil {
		h.logger.Error("Failed to get isMasterAsleep for auto-sync", zap.Error(err))
		return
	}

	// Get current guest sleep state
	currentGuestAsleep, err := h.manager.GetBool("isGuestAsleep")
	if err != nil {
		h.logger.Error("Failed to get isGuestAsleep for auto-sync", zap.Error(err))
		return
	}

	// If already in sync, no need to update
	if currentGuestAsleep == isMasterAsleep {
		return
	}

	// Sync guest asleep state with master
	if err := h.manager.SetBool("isGuestAsleep", isMasterAsleep); err != nil {
		h.logger.Error("Failed to auto-sync isGuestAsleep", zap.Error(err))
		return
	}

	h.logger.Info("Auto-synced guest asleep state (no guests present)",
		zap.Bool("isMasterAsleep", isMasterAsleep),
		zap.Bool("isGuestAsleep", isMasterAsleep))
}

// Recalculate forces re-computation of all derived states
// Used during system reset to ensure all derived states are correct
func (h *DerivedStateHelper) Recalculate() error {
	h.logger.Info("Recalculating all derived states")

	h.updateIsAnyOwnerHome()
	h.updateIsAnyoneHome()
	h.updateIsAnyoneAsleep()
	h.updateIsEveryoneAsleep()
	h.syncGuestAsleepIfNoGuests()

	h.logger.Info("Recalculation complete")
	return nil
}
