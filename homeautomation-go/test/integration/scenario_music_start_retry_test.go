package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/plugins/music"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ============================================================================
// Zone Start Retry Tests
//
// PRODUCTION INCIDENT (2026-11-17, 22:00 CST):
// Day phase changed to night, so the music plugin started the sleep-prep zone
// with Pantry as the lead speaker. Pantry stopped answering for about 50
// seconds. SoCo-CLI timed out on every Pantry request, including clear_queue.
// The plugin logged "Playback verification failed, continuing with fade-in
// anyway", never loaded the rain playlist, and recorded the zone as playing.
// Pantry recovered 0.8 seconds later. Nothing retried, so the house stayed
// silent all night.
//
// INVARIANTS:
// - A zone whose lead speaker could not be given its playlist is not
//   reported as playing.
// - While the zone is still wanted, the plugin keeps retrying the full start
//   sequence until the lead speaker plays.
// ============================================================================

const startRetryRainURI = "http://rain.test/playlists/sleep-rain-1.m3u"

const startRetryMusicConfig = `
zones:
  - name: "sleep-prep"
    priority: 90
    trigger:
      - variable: dayPhase
        value: night
      - variable: isAnyoneHome
        value: true
      - variable: isMasterAsleep
        value: false
music:
  sleep-prep:
    participants:
      - player_name: "Pantry"
        base_volume: 9
        leave_muted_if: []
      - player_name: "Lounge"
        base_volume: 9
        leave_muted_if: []
    playback_options:
      - uri: ` + startRetryRainURI + `
        media_type: music
        volume_multiplier: 1.0
`

// fakeSonosSpeaker is the state the fake SoCo-CLI server keeps for one speaker.
type fakeSonosSpeaker struct {
	queue     []string
	transport string
	volume    int
}

// fakeSoCoServer imitates the SoCo-CLI HTTP API closely enough for the music
// plugin's start sequence. A speaker marked unreachable answers every request
// with a SoCo error, as Pantry did during the incident.
type fakeSoCoServer struct {
	mu          sync.Mutex
	speakers    map[string]*fakeSonosSpeaker
	unreachable map[string]bool
	failedCalls map[string]int // "speaker/action" -> count of refused requests
}

func newFakeSoCoServer() *fakeSoCoServer {
	return &fakeSoCoServer{
		speakers:    make(map[string]*fakeSonosSpeaker),
		unreachable: make(map[string]bool),
		failedCalls: make(map[string]int),
	}
}

func (f *fakeSoCoServer) setUnreachable(speaker string, down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unreachable[speaker] = down
}

func (f *fakeSoCoServer) failedCallCount(speaker, action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failedCalls[speaker+"/"+action]
}

// isPlaying reports whether the speaker is playing the given URI.
func (f *fakeSoCoServer) isPlaying(speaker, uri string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.speakers[speaker]
	if !ok || s.transport != "PLAYING" {
		return false
	}
	for _, queued := range s.queue {
		if queued == uri {
			return true
		}
	}
	return false
}

func (f *fakeSoCoServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Split the escaped path so an encoded URI argument keeps its slashes.
	segments := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	for i, seg := range segments {
		if decoded, err := url.PathUnescape(seg); err == nil {
			segments[i] = decoded
		}
	}
	if len(segments) < 2 {
		http.NotFound(w, r)
		return
	}
	speaker, action, args := segments[0], segments[1], segments[2:]

	f.mu.Lock()
	defer f.mu.Unlock()

	reply := func(exitCode int, result, errMsg string) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"speaker": speaker, "action": action, "exit_code": exitCode, "result": result, "error_msg": errMsg,
		})
	}

	if f.unreachable[speaker] {
		f.failedCalls[speaker+"/"+action]++
		reply(1, "", "HTTPConnectionPool(port=1400): Read timed out. (read timeout=20.0)")
		return
	}

	s, ok := f.speakers[speaker]
	if !ok {
		s = &fakeSonosSpeaker{transport: "STOPPED"}
		f.speakers[speaker] = s
	}

	switch action {
	case "volume":
		if len(args) == 0 {
			reply(0, strconv.Itoa(s.volume), "")
			return
		}
		s.volume, _ = strconv.Atoi(args[0])
	case "clear_queue":
		s.queue = nil
		s.transport = "STOPPED"
	case "add_uri_to_queue":
		s.queue = append(s.queue, strings.Join(args, "/"))
	case "play_from_queue", "play":
		if len(s.queue) == 0 {
			reply(1, "", "queue is empty")
			return
		}
		s.transport = "PLAYING"
	case "pause", "stop":
		s.transport = "STOPPED"
	case "playback":
		reply(0, s.transport, "")
		return
	}
	reply(0, "", "")
}

// zonePlaybackStatus returns the playback status of an active zone, or "" if
// the zone is not active.
func zonePlaybackStatus(m *music.Manager, zoneName string) music.ZonePlaybackStatus {
	for _, zone := range m.GetActiveZones() {
		if zone.Name == zoneName {
			return zone.PlaybackStatus
		}
	}
	return ""
}

// TestScenario_SleepPrepRain_RetriesWhenLeadSpeakerHangsDuringStart reproduces
// the 2026-11-17 incident: the lead speaker is unreachable at the moment the
// zone starts, then recovers.
func TestScenario_SleepPrepRain_RetriesWhenLeadSpeakerHangsDuringStart(t *testing.T) {
	t.Parallel()

	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	var musicConfig music.MusicConfig
	require.NoError(t, yaml.Unmarshal([]byte(startRetryMusicConfig), &musicConfig))

	soco := newFakeSoCoServer()
	socoServer := httptest.NewServer(soco)
	defer socoServer.Close()

	logger := testlogger.New()
	musicMgr := music.NewManager(context.Background(), client, stateManager, &musicConfig, logger, false, nil, nil)
	musicMgr.SetSleepFunc(func(time.Duration) {})
	musicMgr.SetSoCoClient(music.NewSoCoClient(socoServer.URL, logger, false))
	musicMgr.SetZoneStartRetryDelays([]time.Duration{20 * time.Millisecond})

	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "winddown", map[string]interface{}{})

	require.NoError(t, musicMgr.Start())
	defer musicMgr.Stop()
	waitForProcessing(t, stateManager)

	// GIVEN: Pantry, the sleep-prep lead speaker, has stopped answering.
	t.Log("GIVEN: Pantry is unreachable")
	soco.setUnreachable("Pantry", true)

	// WHEN: Night begins, so the sleep-prep zone starts.
	t.Log("WHEN: Day phase changes to night and sleep-prep starts")
	server.SetState("input_text.day_phase", "night", map[string]interface{}{})

	waitForCondition(t, func() bool {
		return soco.failedCallCount("Pantry", "clear_queue") >= 1
	}, "the plugin should try to load the playlist onto Pantry")

	// THEN (during the outage): the zone is not reported as playing.
	t.Log("THEN: While Pantry is unreachable, sleep-prep is not reported as playing")
	waitForCondition(t, func() bool {
		return zonePlaybackStatus(musicMgr, "sleep-prep") == music.ZonePlaybackRetrying
	}, "sleep-prep should be marked as retrying, not playing")
	require.False(t, soco.isPlaying("Pantry", startRetryRainURI))

	// WHEN: Pantry starts answering again.
	t.Log("WHEN: Pantry recovers")
	soco.setUnreachable("Pantry", false)

	// THEN: The plugin retries and the rain playlist plays on Pantry.
	t.Log("THEN: Rain sounds play on Pantry")
	waitForCondition(t, func() bool {
		return soco.isPlaying("Pantry", startRetryRainURI)
	}, "Pantry should play the rain playlist after it recovers")
	waitForCondition(t, func() bool {
		return zonePlaybackStatus(musicMgr, "sleep-prep") == music.ZonePlaybackPlaying
	}, "sleep-prep should be marked as playing once Pantry plays")
}

// TestScenario_SleepPrepRain_RetryStopsWhenZoneEnds checks that a pending retry
// does not start rain sounds after the household has left the sleep-prep zone.
//
// INVARIANT: Retries run only while their zone is active. A speaker that
// recovers later must not start music for a zone that has already ended.
func TestScenario_SleepPrepRain_RetryStopsWhenZoneEnds(t *testing.T) {
	t.Parallel()

	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	var musicConfig music.MusicConfig
	require.NoError(t, yaml.Unmarshal([]byte(startRetryMusicConfig), &musicConfig))

	soco := newFakeSoCoServer()
	socoServer := httptest.NewServer(soco)
	defer socoServer.Close()

	logger := testlogger.New()
	musicMgr := music.NewManager(context.Background(), client, stateManager, &musicConfig, logger, false, nil, nil)
	musicMgr.SetSleepFunc(func(time.Duration) {})
	musicMgr.SetSoCoClient(music.NewSoCoClient(socoServer.URL, logger, false))
	musicMgr.SetZoneStartRetryDelays([]time.Duration{20 * time.Millisecond})

	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "winddown", map[string]interface{}{})

	require.NoError(t, musicMgr.Start())
	defer musicMgr.Stop()
	waitForProcessing(t, stateManager)

	// GIVEN: sleep-prep failed to start because Pantry is unreachable.
	t.Log("GIVEN: sleep-prep is retrying because Pantry is unreachable")
	soco.setUnreachable("Pantry", true)
	server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	waitForCondition(t, func() bool {
		return zonePlaybackStatus(musicMgr, "sleep-prep") == music.ZonePlaybackRetrying
	}, "sleep-prep should be retrying")

	// WHEN: Night ends, so sleep-prep is no longer wanted. Then Pantry recovers.
	t.Log("WHEN: Day phase leaves night, then Pantry recovers")
	server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	waitForCondition(t, func() bool {
		return zonePlaybackStatus(musicMgr, "sleep-prep") == ""
	}, "sleep-prep should stop when night ends")
	soco.setUnreachable("Pantry", false)

	// THEN: Rain sounds do not start. Allow many retry periods to pass.
	t.Log("THEN: Rain sounds never start on Pantry")
	require.Never(t, func() bool {
		return soco.isPlaying("Pantry", startRetryRainURI)
	}, 500*time.Millisecond, 10*time.Millisecond, "a retry must not start rain after sleep-prep ended")
}
