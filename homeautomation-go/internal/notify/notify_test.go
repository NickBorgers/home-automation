package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap/zaptest"
)

func newTestManager(t *testing.T, mockHA *ha.MockClient, stateMgr *state.Manager, readOnly bool, snapshotRestore bool) *Manager {
	t.Helper()
	cfg := DefaultConfig()
	cfg.SnapshotRestore = snapshotRestore
	// short restore delay so async restore tests don't take 8s
	cfg.RestoreDelaySeconds = 1
	cfg.RestoreDelay = 50 * time.Millisecond
	return NewManager(mockHA, stateMgr, cfg, zaptest.NewLogger(t), readOnly)
}

func TestAnnounce_SnapshotOverrideSpeakRestore(t *testing.T) {
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.25})
	mockHA.SetState("media_player.front_room", "playing", map[string]interface{}{"volume_level": 0.40})

	m := newTestManager(t, mockHA, nil, false, true)

	err := m.Announce(context.Background(), "Test announcement", WithSpeakers([]string{
		"media_player.kitchen",
		"media_player.front_room",
	}))
	if err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}

	// Wait for async restore
	m.WaitForRestores()

	calls := mockHA.GetServiceCalls()

	// Expect: 2 volume_set (override) + 1 tts.speak + 2 volume_set (restore) = 5
	if len(calls) < 5 {
		t.Fatalf("expected at least 5 service calls, got %d: %+v", len(calls), calls)
	}

	// First two calls: override volume to 0.6 (default awake)
	for i := 0; i < 2; i++ {
		c := calls[i]
		if c.Domain != "media_player" || c.Service != "volume_set" {
			t.Errorf("call %d: expected media_player.volume_set, got %s.%s", i, c.Domain, c.Service)
		}
		if v, _ := c.Data["volume_level"].(float64); v != 0.60 {
			t.Errorf("call %d: expected volume_level 0.60, got %v", i, c.Data["volume_level"])
		}
	}

	// Third call: tts.speak
	tts := calls[2]
	if tts.Domain != "tts" || tts.Service != "speak" {
		t.Errorf("call 2: expected tts.speak, got %s.%s", tts.Domain, tts.Service)
	}
	if msg, _ := tts.Data["message"].(string); msg != "Test announcement" {
		t.Errorf("expected message 'Test announcement', got %v", tts.Data["message"])
	}

	// Last two calls: restore prior volumes
	restoreCalls := calls[3:]
	restoredVolumes := map[string]float64{}
	for _, c := range restoreCalls {
		if c.Domain != "media_player" || c.Service != "volume_set" {
			t.Errorf("expected restore call media_player.volume_set, got %s.%s", c.Domain, c.Service)
			continue
		}
		entityID, _ := c.Data["entity_id"].(string)
		level, _ := c.Data["volume_level"].(float64)
		restoredVolumes[entityID] = level
	}
	if got := restoredVolumes["media_player.kitchen"]; got != 0.25 {
		t.Errorf("kitchen restore: expected 0.25, got %v", got)
	}
	if got := restoredVolumes["media_player.front_room"]; got != 0.40 {
		t.Errorf("front_room restore: expected 0.40, got %v", got)
	}
}

func TestAnnounce_ReadOnlyDoesNothing(t *testing.T) {
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.25})

	m := newTestManager(t, mockHA, nil, true, true)

	if err := m.Announce(context.Background(), "Hi", WithSpeakers([]string{"media_player.kitchen"})); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}
	m.WaitForRestores()

	if calls := mockHA.GetServiceCalls(); len(calls) != 0 {
		t.Errorf("read-only mode should not make service calls, got %d", len(calls))
	}
}

func TestAnnounce_EmptyMessageReturnsError(t *testing.T) {
	mockHA := ha.NewMockClient()
	m := newTestManager(t, mockHA, nil, false, true)

	if err := m.Announce(context.Background(), "", WithSpeakers([]string{"media_player.kitchen"})); err == nil {
		t.Error("expected error for empty message")
	}
}

func TestAnnounce_NoSpeakersReturnsError(t *testing.T) {
	mockHA := ha.NewMockClient()
	m := newTestManager(t, mockHA, nil, false, true)
	// Override default speakers to empty
	m.cfg.DefaultSpeakers = nil

	if err := m.Announce(context.Background(), "Hi"); err == nil {
		t.Error("expected error when no speakers configured")
	}
}

func TestAnnounce_TTSFailureRestoresImmediately(t *testing.T) {
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.30})
	mockHA.SetServiceError("tts", "speak", errors.New("tts unavailable"))

	m := newTestManager(t, mockHA, nil, false, true)

	err := m.Announce(context.Background(), "Hi", WithSpeakers([]string{"media_player.kitchen"}))
	if err == nil {
		t.Fatal("expected error from failed TTS")
	}

	// Errored services don't get recorded by the mock, so we expect:
	//   1. override volume_set (recorded)
	//   2. tts.speak (errored - not recorded)
	//   3. restore volume_set (recorded)
	calls := mockHA.GetServiceCalls()
	if len(calls) != 2 {
		t.Fatalf("expected exactly 2 recorded calls (override + restore), got %d: %+v", len(calls), calls)
	}

	// Override set to 0.60 (default awake), restore set back to 0.30
	if v, _ := calls[0].Data["volume_level"].(float64); v != 0.60 {
		t.Errorf("expected override to 0.60, got %v", v)
	}
	if v, _ := calls[1].Data["volume_level"].(float64); v != 0.30 {
		t.Errorf("expected restore to 0.30, got %v", v)
	}

	// No goroutine restore should be pending
	doneCh := make(chan struct{})
	go func() { m.WaitForRestores(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Error("WaitForRestores did not return promptly after TTS failure")
	}
}

func TestAnnounce_SleepAwareVolumeRoutine(t *testing.T) {
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.40})

	stateMgr := state.NewManager(mockHA, zaptest.NewLogger(t), false)
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("SetBool failed: %v", err)
	}
	// Clear the service calls SetBool may have made (e.g. input_boolean.turn_on)
	// so we only observe Announce's calls below.
	mockHA.ClearServiceCalls()

	m := newTestManager(t, mockHA, stateMgr, false, true)

	if err := m.Announce(context.Background(), "Hi", WithSpeakers([]string{"media_player.kitchen"})); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}
	m.WaitForRestores()

	// First call is the volume override - should use AsleepVolumePercent (30%)
	calls := mockHA.GetServiceCalls()
	if len(calls) < 1 {
		t.Fatalf("expected at least one call")
	}
	override := calls[0]
	if override.Domain != "media_player" || override.Service != "volume_set" {
		t.Fatalf("expected first call to be volume_set, got %s.%s", override.Domain, override.Service)
	}
	if v, _ := override.Data["volume_level"].(float64); v != 0.30 {
		t.Errorf("routine announcement while asleep: expected volume 0.30, got %v", v)
	}
}

func TestAnnounce_SleepAwareVolumeUrgent(t *testing.T) {
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.40})

	stateMgr := state.NewManager(mockHA, zaptest.NewLogger(t), false)
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("SetBool failed: %v", err)
	}
	// Clear the service calls SetBool may have made (e.g. input_boolean.turn_on)
	// so we only observe Announce's calls below.
	mockHA.ClearServiceCalls()

	m := newTestManager(t, mockHA, stateMgr, false, true)

	if err := m.Announce(context.Background(), "Person at door", WithSpeakers([]string{"media_player.kitchen"}), WithUrgency(UrgencyUrgent)); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}
	m.WaitForRestores()

	calls := mockHA.GetServiceCalls()
	override := calls[0]
	if v, _ := override.Data["volume_level"].(float64); v != 0.60 {
		t.Errorf("urgent announcement while asleep: expected volume 0.60 (awake), got %v", v)
	}
}

func TestAnnounce_SnapshotRestoreDisabled(t *testing.T) {
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.25})

	m := newTestManager(t, mockHA, nil, false, false)

	if err := m.Announce(context.Background(), "Hi", WithSpeakers([]string{"media_player.kitchen"})); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}
	m.WaitForRestores()

	// Only the tts.speak call should happen - no volume snapshot or restore.
	calls := mockHA.GetServiceCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 call (tts.speak), got %d: %+v", len(calls), calls)
	}
	if calls[0].Domain != "tts" || calls[0].Service != "speak" {
		t.Errorf("expected tts.speak, got %s.%s", calls[0].Domain, calls[0].Service)
	}
}

func TestAnnounce_MissingSpeakerStateOmittedFromRestore(t *testing.T) {
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.25})
	// Note: media_player.bedroom intentionally not set.

	m := newTestManager(t, mockHA, nil, false, true)

	if err := m.Announce(context.Background(), "Hi", WithSpeakers([]string{
		"media_player.kitchen",
		"media_player.bedroom",
	})); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}
	m.WaitForRestores()

	calls := mockHA.GetServiceCalls()

	// Count restore calls (volume_set) that happen after the tts.speak call
	ttsIdx := -1
	for i, c := range calls {
		if c.Domain == "tts" && c.Service == "speak" {
			ttsIdx = i
			break
		}
	}
	if ttsIdx == -1 {
		t.Fatalf("tts.speak call not found")
	}

	// Restore calls only for entities with snapshotted state.
	restoreCount := 0
	for _, c := range calls[ttsIdx+1:] {
		if c.Domain == "media_player" && c.Service == "volume_set" {
			restoreCount++
		}
	}
	if restoreCount != 1 {
		t.Errorf("expected exactly 1 restore call (kitchen only), got %d", restoreCount)
	}
}

func TestMockNotifier_RecordsCalls(t *testing.T) {
	mock := &MockNotifier{}

	if err := mock.Announce(context.Background(), "first", WithSpeakers([]string{"media_player.kitchen"})); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}
	if err := mock.Announce(context.Background(), "second", WithUrgency(UrgencyUrgent)); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}

	calls := mock.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Message != "first" || calls[0].Speakers[0] != "media_player.kitchen" {
		t.Errorf("call 0 unexpected: %+v", calls[0])
	}
	if calls[1].Message != "second" || calls[1].Urgency != UrgencyUrgent {
		t.Errorf("call 1 unexpected: %+v", calls[1])
	}

	mock.Reset()
	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("Reset did not clear calls: %d remaining", len(calls))
	}
}

func TestMockNotifier_ConcurrentSafe(t *testing.T) {
	mock := &MockNotifier{}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mock.Announce(context.Background(), "concurrent")
		}()
	}
	wg.Wait()
	if len(mock.Calls()) != 10 {
		t.Errorf("expected 10 calls from concurrent goroutines, got %d", len(mock.Calls()))
	}
}

func TestLoadConfig_AppliesDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.AwakeVolumePercent != defaultAwakeVolumePercent {
		t.Errorf("AwakeVolumePercent: expected %d, got %d", defaultAwakeVolumePercent, cfg.AwakeVolumePercent)
	}
	if cfg.AsleepVolumePercent != defaultAsleepVolumePercent {
		t.Errorf("AsleepVolumePercent: expected %d, got %d", defaultAsleepVolumePercent, cfg.AsleepVolumePercent)
	}
	if cfg.TTSDomain != defaultTTSDomain {
		t.Errorf("TTSDomain: expected %s, got %s", defaultTTSDomain, cfg.TTSDomain)
	}
	if cfg.TTSEntityID != defaultTTSEntityID {
		t.Errorf("TTSEntityID: expected %s, got %s", defaultTTSEntityID, cfg.TTSEntityID)
	}
	if !cfg.SnapshotRestore {
		t.Error("SnapshotRestore should default to true")
	}
	if len(cfg.DefaultSpeakers) == 0 {
		t.Error("DefaultSpeakers should not be empty")
	}
}

func TestConfigValidate_RejectsInvalidVolume(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AwakeVolumePercent = 150
	if err := cfg.validate(); err == nil {
		t.Error("expected validate to reject volume > 100")
	}

	cfg = DefaultConfig()
	cfg.AsleepVolumePercent = -1
	if err := cfg.validate(); err == nil {
		t.Error("expected validate to reject negative volume")
	}
}
