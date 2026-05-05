package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap/zaptest"
)

// fakeSynth returns a canned URL (or err) and records what it was asked to say.
type fakeSynth struct {
	mu       sync.Mutex
	url      string
	err      error
	messages []string
}

func (f *fakeSynth) SynthesizeAndServe(_ context.Context, text string) (string, error) {
	f.mu.Lock()
	f.messages = append(f.messages, text)
	f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	return f.url, nil
}

func (f *fakeSynth) Messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.messages))
	copy(out, f.messages)
	return out
}

func newTestManager(t *testing.T, mockHA *ha.MockClient, stateMgr *state.Manager, synth *fakeSynth, readOnly bool) *Manager {
	t.Helper()
	cfg := DefaultConfig()
	if synth == nil {
		synth = &fakeSynth{url: "http://test/audio/abc.mp3"}
	}
	return NewManager(mockHA, stateMgr, synth, cfg, zaptest.NewLogger(t), readOnly)
}

// equalStringSlices compares two []string when one of them comes back as
// interface{} from the mock service-call data.
func equalStringSlices(want []string, got interface{}) bool {
	gs, ok := got.([]string)
	if !ok {
		return false
	}
	if len(gs) != len(want) {
		return false
	}
	for i := range want {
		if gs[i] != want[i] {
			return false
		}
	}
	return true
}

func TestAnnounce_PlaysViaMediaPlayerWithAnnounceFlag(t *testing.T) {
	mockHA := ha.NewMockClient()
	synth := &fakeSynth{url: "http://10.212.100.100:8085/audio/deadbeef.mp3"}

	speakers := []string{"media_player.kitchen", "media_player.front_room"}
	m := newTestManager(t, mockHA, nil, synth, false)
	// Set a non-default value so this test doubles as a regression guard
	// against silently sending the wrong volume scale (PR #1084).
	m.cfg.AwakeVolumePercent = 40

	if err := m.Announce(context.Background(), "Test announcement", WithSpeakers(speakers)); err != nil {
		t.Fatalf("Announce: %v", err)
	}

	// Synthesizer should have been asked to speak the message exactly once.
	if msgs := synth.Messages(); len(msgs) != 1 || msgs[0] != "Test announcement" {
		t.Errorf("synthesizer messages: want [Test announcement], got %v", msgs)
	}

	calls := mockHA.GetServiceCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 service call (media_player.play_media), got %d: %+v", len(calls), calls)
	}
	c := calls[0]
	if c.Domain != "media_player" || c.Service != "play_media" {
		t.Fatalf("call: want media_player.play_media, got %s.%s", c.Domain, c.Service)
	}
	if !equalStringSlices(speakers, c.Data["entity_id"]) {
		t.Errorf("entity_id: want %v, got %v", speakers, c.Data["entity_id"])
	}
	if got, _ := c.Data["media_content_id"].(string); got != synth.url {
		t.Errorf("media_content_id: want %s, got %v", synth.url, c.Data["media_content_id"])
	}
	if got, _ := c.Data["media_content_type"].(string); got != "music" {
		t.Errorf("media_content_type: want music, got %v", c.Data["media_content_type"])
	}
	if got, _ := c.Data["announce"].(bool); !got {
		t.Errorf("announce: want true, got %v", c.Data["announce"])
	}
	extra, ok := c.Data["extra"].(map[string]interface{})
	if !ok {
		t.Fatalf("extra: want map, got %T (%v)", c.Data["extra"], c.Data["extra"])
	}
	// HA's Sonos integration passes extra.volume through to SoCo, which
	// expects an int 0-100. Sending a float (e.g. 0.40) gets floored to 0
	// → silent. Assert int round-trips with no rescaling.
	wantVol := 40
	if got, _ := extra["volume"].(int); got != wantVol {
		t.Errorf("extra.volume: want %v (int), got %v (%T)", wantVol, extra["volume"], extra["volume"])
	}

	// Asserts the snapshot/restore code is gone: nothing should fiddle with volume_set.
	for _, c := range calls {
		if c.Domain == "media_player" && c.Service == "volume_set" {
			t.Errorf("unexpected volume_set call: %+v", c)
		}
	}
}

func TestAnnounce_ReadOnlyDoesNothing(t *testing.T) {
	mockHA := ha.NewMockClient()
	synth := &fakeSynth{url: "http://test/audio/x.mp3"}

	m := newTestManager(t, mockHA, nil, synth, true)

	if err := m.Announce(context.Background(), "Hi", WithSpeakers([]string{"media_player.kitchen"})); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}
	if calls := mockHA.GetServiceCalls(); len(calls) != 0 {
		t.Errorf("read-only should make zero HA calls, got %d", len(calls))
	}
	if msgs := synth.Messages(); len(msgs) != 0 {
		t.Errorf("read-only should not call synthesizer, got %d messages", len(msgs))
	}
}

func TestAnnounce_EmptyMessageReturnsError(t *testing.T) {
	mockHA := ha.NewMockClient()
	m := newTestManager(t, mockHA, nil, nil, false)

	if err := m.Announce(context.Background(), "", WithSpeakers([]string{"media_player.kitchen"})); err == nil {
		t.Error("expected error for empty message")
	}
}

func TestAnnounce_NoSpeakersReturnsError(t *testing.T) {
	mockHA := ha.NewMockClient()
	m := newTestManager(t, mockHA, nil, nil, false)
	m.cfg.DefaultSpeakers = nil

	if err := m.Announce(context.Background(), "Hi"); err == nil {
		t.Error("expected error when no speakers configured")
	}
}

func TestAnnounce_SynthesizerFailureSkipsHA(t *testing.T) {
	mockHA := ha.NewMockClient()
	synth := &fakeSynth{err: errors.New("kokoro down")}

	m := newTestManager(t, mockHA, nil, synth, false)

	err := m.Announce(context.Background(), "Hi", WithSpeakers([]string{"media_player.kitchen"}))
	if err == nil {
		t.Fatal("expected error from synthesizer failure")
	}
	if calls := mockHA.GetServiceCalls(); len(calls) != 0 {
		t.Errorf("synthesis failure should not trigger HA calls, got %d: %+v", len(calls), calls)
	}
}

func TestAnnounce_PlayMediaFailurePropagates(t *testing.T) {
	mockHA := ha.NewMockClient()
	mockHA.SetServiceError("media_player", "play_media", errors.New("HA down"))
	synth := &fakeSynth{url: "http://test/audio/x.mp3"}

	m := newTestManager(t, mockHA, nil, synth, false)

	err := m.Announce(context.Background(), "Hi", WithSpeakers([]string{"media_player.kitchen"}))
	if err == nil {
		t.Fatal("expected error from media_player.play_media failure")
	}
}

func TestAnnounce_DeferableWhileAsleep_SuppressesWithSentinel(t *testing.T) {
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, zaptest.NewLogger(t), false)
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("SetBool failed: %v", err)
	}
	mockHA.ClearServiceCalls()
	synth := &fakeSynth{url: "http://test/audio/x.mp3"}

	m := newTestManager(t, mockHA, stateMgr, synth, false)

	err := m.Announce(context.Background(), "Vacuum needs attention",
		WithSpeakers([]string{"media_player.kitchen"}),
		WithUrgency(UrgencyDeferable))
	if !errors.Is(err, ErrSuppressedAsleep) {
		t.Fatalf("expected ErrSuppressedAsleep, got %v", err)
	}
	if calls := mockHA.GetServiceCalls(); len(calls) != 0 {
		t.Errorf("deferable+asleep should make zero HA calls, got %d: %+v", len(calls), calls)
	}
	if msgs := synth.Messages(); len(msgs) != 0 {
		t.Errorf("suppressed announcement should not synthesize, got %d messages", len(msgs))
	}
}

func TestAnnounce_DeferableWhileAwake_PlaysAtAwakeVolume(t *testing.T) {
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, zaptest.NewLogger(t), false)
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("SetBool failed: %v", err)
	}
	mockHA.ClearServiceCalls()

	m := newTestManager(t, mockHA, stateMgr, nil, false)

	if err := m.Announce(context.Background(), "Hi",
		WithSpeakers([]string{"media_player.kitchen"}),
		WithUrgency(UrgencyDeferable)); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}

	calls := mockHA.GetServiceCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(calls), calls)
	}
	extra, _ := calls[0].Data["extra"].(map[string]interface{})
	want := defaultAwakeVolumePercent
	if got, _ := extra["volume"].(int); got != want {
		t.Errorf("deferable+awake: want extra.volume %v (int), got %v (%T)", want, extra["volume"], extra["volume"])
	}
}

func TestAnnounce_UrgentWhileAsleep_PlaysAtAwakeVolume(t *testing.T) {
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, zaptest.NewLogger(t), false)
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("SetBool failed: %v", err)
	}
	mockHA.ClearServiceCalls()

	m := newTestManager(t, mockHA, stateMgr, nil, false)

	if err := m.Announce(context.Background(), "Person at door",
		WithSpeakers([]string{"media_player.kitchen"}),
		WithUrgency(UrgencyUrgent)); err != nil {
		t.Fatalf("Announce returned error: %v", err)
	}

	calls := mockHA.GetServiceCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(calls), calls)
	}
	extra, _ := calls[0].Data["extra"].(map[string]interface{})
	want := defaultAwakeVolumePercent
	if got, _ := extra["volume"].(int); got != want {
		t.Errorf("urgent+asleep: want extra.volume %v (int), got %v (%T)", want, extra["volume"], extra["volume"])
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
		go func(n int) {
			defer wg.Done()
			_ = mock.Announce(context.Background(), fmt.Sprintf("concurrent-%d", n))
		}(i)
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
	cfg.AwakeVolumePercent = -1
	if err := cfg.validate(); err == nil {
		t.Error("expected validate to reject negative volume")
	}
}
