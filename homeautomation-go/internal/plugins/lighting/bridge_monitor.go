package lighting

import (
	"context"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// Bridge monitor constants
const (
	// BridgeVerifyDelay is how long to wait after scene activation before checking brightness.
	// The Hue bridge needs time to process and HA needs to poll the updated state.
	BridgeVerifyDelay = 15 * time.Second

	// BridgeBrightnessTolerance is the minimum brightness change (0-255) that indicates
	// the scene actually took effect. A delta smaller than this is treated as "no change".
	BridgeBrightnessTolerance = 10

	// BridgeStaleRoomThreshold is the number of distinct rooms that must fail verification
	// before we consider the bridge itself stale (vs. a single-bulb issue).
	BridgeStaleRoomThreshold = 2

	// BridgeNotificationCooldown prevents notification spam. After sending a notification,
	// wait at least this long before sending another.
	BridgeNotificationCooldown = 1 * time.Hour

	// BridgeFailureWindow is how long a room failure remains "active" before it ages out.
	// This prevents old failures from contributing to the bridge-stale threshold.
	BridgeFailureWindow = 30 * time.Minute

	// BridgeNotificationDevice is the device to send notifications to.
	BridgeNotificationDevice = "nicks_iphone"

	// BridgeNotificationTag is used to create replaceable notifications.
	BridgeNotificationTag = "hue_bridge_stale"
)

// SceneVerification represents the result of verifying a scene activation.
type SceneVerification struct {
	RoomName         string    `json:"roomName"`
	SceneName        string    `json:"sceneName"`
	LightEntityID    string    `json:"lightEntityId"`
	BrightnessBefore *int      `json:"brightnessBefore,omitempty"`
	BrightnessAfter  *int      `json:"brightnessAfter,omitempty"`
	ExpectedChange   bool      `json:"expectedChange"`
	ActuallyChanged  bool      `json:"actuallyChanged"`
	Verified         bool      `json:"verified"`
	FailureReason    string    `json:"failureReason,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
}

// BridgeMonitorState tracks the overall Hue bridge health.
type BridgeMonitorState struct {
	RecentFailures       []SceneVerification `json:"recentFailures,omitempty"`
	BridgeStale          bool                `json:"bridgeStale"`
	LastNotificationTime time.Time           `json:"lastNotificationTime,omitempty"`
	LastVerification     *SceneVerification  `json:"lastVerification,omitempty"`
	ConsecutiveFailures  int                 `json:"consecutiveFailures"`
}

// BridgeMonitor verifies that Hue scene activations actually take effect by reading
// back brightness from light entities after activation.
type BridgeMonitor struct {
	haClient      ha.HAClient
	logger        *zap.Logger
	readOnly      bool
	shadowTracker *shadowstate.LightingTracker

	mu                   sync.Mutex
	recentFailures       []SceneVerification
	lastNotificationTime time.Time

	// For testing: allow overriding the delay and time functions
	verifyDelay time.Duration
	nowFunc     func() time.Time
	sleepFunc   func(ctx context.Context, d time.Duration) error
}

// NewBridgeMonitor creates a new bridge monitor.
func NewBridgeMonitor(haClient ha.HAClient, logger *zap.Logger, readOnly bool, shadowTracker *shadowstate.LightingTracker) *BridgeMonitor {
	return &BridgeMonitor{
		haClient:       haClient,
		logger:         logger.Named("bridge-monitor"),
		readOnly:       readOnly,
		shadowTracker:  shadowTracker,
		recentFailures: make([]SceneVerification, 0),
		verifyDelay:    BridgeVerifyDelay,
		nowFunc:        time.Now,
		sleepFunc:      defaultSleep,
	}
}

// defaultSleep sleeps for the given duration, respecting context cancellation.
func defaultSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// VerifySceneActivation asynchronously verifies that a scene activation took effect.
// It reads the brightness before activation, waits for the bridge to process, then reads again.
// This should be called in a goroutine after activateScene succeeds.
func (bm *BridgeMonitor) VerifySceneActivation(ctx context.Context, room *RoomConfig, dayPhase string) {
	lightEntityID := room.LightEntityID
	if lightEntityID == "" {
		// No light entity configured for verification - skip
		return
	}

	roomName := room.HueGroup

	// Read brightness BEFORE (use the current state since scene was just activated)
	brightnessBefore, err := bm.readBrightness(lightEntityID)
	if err != nil {
		bm.logger.Debug("Cannot read pre-activation brightness, skipping verification",
			zap.String("room", roomName),
			zap.String("entity", lightEntityID),
			zap.Error(err))
		return
	}

	// Wait for the bridge to process and HA to poll
	if err := bm.sleepFunc(ctx, bm.verifyDelay); err != nil {
		bm.logger.Debug("Verification cancelled during wait",
			zap.String("room", roomName))
		return
	}

	// Read brightness AFTER
	brightnessAfter, err := bm.readBrightness(lightEntityID)
	if err != nil {
		bm.logger.Debug("Cannot read post-activation brightness, skipping verification",
			zap.String("room", roomName),
			zap.String("entity", lightEntityID),
			zap.Error(err))
		return
	}

	// Determine if brightness actually changed
	delta := abs(brightnessAfter - brightnessBefore)
	actuallyChanged := delta >= BridgeBrightnessTolerance

	now := bm.nowFunc()
	verification := SceneVerification{
		RoomName:         roomName,
		SceneName:        dayPhase,
		LightEntityID:    lightEntityID,
		BrightnessBefore: &brightnessBefore,
		BrightnessAfter:  &brightnessAfter,
		ExpectedChange:   true, // We always expect a scene activation to change brightness
		ActuallyChanged:  actuallyChanged,
		Verified:         actuallyChanged,
		Timestamp:        now,
	}

	if !actuallyChanged {
		verification.FailureReason = fmt.Sprintf(
			"brightness unchanged after scene activation (before=%d, after=%d, delta=%d, tolerance=%d)",
			brightnessBefore, brightnessAfter, delta, BridgeBrightnessTolerance)

		bm.logger.Warn("Scene activation may not have taken effect",
			zap.String("room", roomName),
			zap.String("scene", dayPhase),
			zap.String("entity", lightEntityID),
			zap.Int("brightness_before", brightnessBefore),
			zap.Int("brightness_after", brightnessAfter),
			zap.Int("delta", delta))

		bm.recordFailure(verification)
	} else {
		bm.logger.Info("Scene activation verified",
			zap.String("room", roomName),
			zap.String("scene", dayPhase),
			zap.Int("brightness_before", brightnessBefore),
			zap.Int("brightness_after", brightnessAfter))
	}

	// Update shadow state
	bm.updateShadowState(&verification)
}

// readBrightness reads the current brightness attribute from a light entity.
// Returns brightness as an int (0-255).
func (bm *BridgeMonitor) readBrightness(entityID string) (int, error) {
	state, err := bm.haClient.GetState(entityID)
	if err != nil {
		return 0, fmt.Errorf("failed to get state for %s: %w", entityID, err)
	}

	if state.State == "off" {
		return 0, nil
	}

	brightnessRaw, ok := state.Attributes["brightness"]
	if !ok {
		return 0, fmt.Errorf("no brightness attribute on %s", entityID)
	}

	// Brightness can be float64 (from JSON) or int
	switch v := brightnessRaw.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	default:
		return 0, fmt.Errorf("unexpected brightness type %T on %s", brightnessRaw, entityID)
	}
}

// recordFailure records a verification failure and checks if the bridge appears stale.
func (bm *BridgeMonitor) recordFailure(verification SceneVerification) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	now := bm.nowFunc()

	// Add the failure
	bm.recentFailures = append(bm.recentFailures, verification)

	// Prune old failures outside the window
	bm.pruneOldFailures(now)

	// Count distinct rooms with recent failures
	failedRooms := bm.countDistinctFailedRooms()

	bm.logger.Info("Bridge verification failure recorded",
		zap.String("room", verification.RoomName),
		zap.Int("distinct_failed_rooms", failedRooms),
		zap.Int("threshold", BridgeStaleRoomThreshold))

	// Check if we've exceeded the threshold
	if failedRooms >= BridgeStaleRoomThreshold {
		bm.handleBridgeStale(failedRooms, now)
	}
}

// pruneOldFailures removes failures older than the failure window.
// Must be called with bm.mu held.
func (bm *BridgeMonitor) pruneOldFailures(now time.Time) {
	cutoff := now.Add(-BridgeFailureWindow)
	fresh := make([]SceneVerification, 0, len(bm.recentFailures))
	for _, f := range bm.recentFailures {
		if f.Timestamp.After(cutoff) {
			fresh = append(fresh, f)
		}
	}
	bm.recentFailures = fresh
}

// countDistinctFailedRooms counts the number of distinct rooms with recent failures.
// Must be called with bm.mu held.
func (bm *BridgeMonitor) countDistinctFailedRooms() int {
	rooms := make(map[string]bool)
	for _, f := range bm.recentFailures {
		rooms[f.RoomName] = true
	}
	return len(rooms)
}

// handleBridgeStale sends a notification that the Hue bridge appears stale.
// Must be called with bm.mu held.
func (bm *BridgeMonitor) handleBridgeStale(failedRooms int, now time.Time) {
	// Check cooldown
	if !bm.lastNotificationTime.IsZero() && now.Sub(bm.lastNotificationTime) < BridgeNotificationCooldown {
		bm.logger.Info("Bridge stale detected but notification on cooldown",
			zap.Int("failed_rooms", failedRooms),
			zap.Duration("cooldown_remaining", BridgeNotificationCooldown-now.Sub(bm.lastNotificationTime)))
		return
	}

	bm.logger.Error("Hue bridge appears stale - scenes not taking effect",
		zap.Int("failed_rooms", failedRooms))

	if bm.readOnly {
		bm.logger.Info("READ-ONLY: Would send bridge stale notification")
		return
	}

	// Build failure details for the notification message
	roomDetails := ""
	rooms := make(map[string]bool)
	for _, f := range bm.recentFailures {
		if !rooms[f.RoomName] {
			rooms[f.RoomName] = true
			if roomDetails != "" {
				roomDetails += ", "
			}
			roomDetails += f.RoomName
		}
	}

	notification := &ha.Notification{
		Title:   "Hue Bridge Stale",
		Message: fmt.Sprintf("Scene activations are not taking effect in %d rooms (%s). The Hue bridge may need a power cycle.", failedRooms, roomDetails),
		Data: &ha.NotificationData{
			Tag:        BridgeNotificationTag,
			Group:      "home-automation",
			Importance: "high",
			Channel:    "alerts",
		},
	}

	if err := bm.haClient.SendNotification(BridgeNotificationDevice, notification); err != nil {
		bm.logger.Error("Failed to send bridge stale notification", zap.Error(err))
		return
	}

	bm.lastNotificationTime = now
	bm.logger.Info("Bridge stale notification sent",
		zap.Int("failed_rooms", failedRooms),
		zap.String("rooms", roomDetails))
}

// updateShadowState updates the shadow state with the latest verification result.
func (bm *BridgeMonitor) updateShadowState(verification *SceneVerification) {
	bm.mu.Lock()
	failedRooms := bm.countDistinctFailedRooms()
	failures := make([]SceneVerification, len(bm.recentFailures))
	copy(failures, bm.recentFailures)
	lastNotification := bm.lastNotificationTime
	bm.mu.Unlock()

	// Convert to shadow state types
	recentFailures := make([]shadowstate.LightingVerificationFailure, 0, len(failures))
	for _, f := range failures {
		recentFailures = append(recentFailures, shadowstate.LightingVerificationFailure{
			RoomName:      f.RoomName,
			SceneName:     f.SceneName,
			FailureReason: f.FailureReason,
			Timestamp:     f.Timestamp,
		})
	}

	monitor := &shadowstate.LightingBridgeMonitor{
		BridgeStale:          failedRooms >= BridgeStaleRoomThreshold,
		ConsecutiveFailures:  len(failures),
		RecentFailures:       recentFailures,
		LastNotificationTime: lastNotification,
	}

	bm.shadowTracker.SetBridgeMonitor(monitor)
}

// GetState returns the current bridge monitor state for shadow state.
func (bm *BridgeMonitor) GetState() BridgeMonitorState {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	now := bm.nowFunc()
	bm.pruneOldFailures(now)

	failedRooms := bm.countDistinctFailedRooms()
	failures := make([]SceneVerification, len(bm.recentFailures))
	copy(failures, bm.recentFailures)

	return BridgeMonitorState{
		RecentFailures:       failures,
		BridgeStale:          failedRooms >= BridgeStaleRoomThreshold,
		LastNotificationTime: bm.lastNotificationTime,
		ConsecutiveFailures:  len(failures),
	}
}

// ClearFailures resets the failure tracking (e.g., after a bridge power cycle).
func (bm *BridgeMonitor) ClearFailures() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.recentFailures = make([]SceneVerification, 0)
	bm.logger.Info("Bridge monitor failures cleared")
}

// abs returns the absolute value of an int.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
