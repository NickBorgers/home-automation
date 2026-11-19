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
	"homeautomation/internal/shadowstate"
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

const fadeVerificationMusicConfig = `
zones:
  - name: "morning"
    priority: 50
    trigger:
      - variable: dayPhase
        value: morning
      - variable: isAnyoneHome
        value: true
music:
  morning:
    participants:
      - player_name: "Pantry"
        base_volume: 9
        leave_muted_if: []
    playback_options:
      - uri: "https://tidal.com/browse/playlist/fade-test"
        media_type: tidal
        volume_multiplier: 1.0
`

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

// seamlessGuardTestConfig has sleep-prep and sleep zones that share both Pantry
// and the same rain URI. This means findSeamlessTransitions would normally
// consider sleep-prep→sleep a seamless transition. The guard at zone_manager.go
// must skip the seamless path when sleep-prep is retrying.
const seamlessGuardTestConfig = `
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
  - name: "sleep"
    priority: 100
    trigger:
      - variable: dayPhase
        value: night
      - variable: isAnyoneHome
        value: true
      - variable: isMasterAsleep
        value: true
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
  sleep:
    participants:
      - player_name: "Pantry"
        base_volume: 7
        leave_muted_if: []
    playback_options:
      - uri: ` + startRetryRainURI + `
        media_type: music
        volume_multiplier: 1.0
`

// fakeSonosSpeaker is the state the fake SoCo-CLI server keeps for one speaker.
type fakeSonosSpeaker struct {
	queue      []string
	transport  string
	volume     int
	currentURI string // what get_uri reports
}

// fakeSoCoServer imitates the SoCo-CLI HTTP API closely enough for the music
// plugin's start sequence. A speaker marked unreachable answers every request
// with a SoCo error, as Pantry did during the incident.
type fakeSoCoServer struct {
	mu                sync.Mutex
	speakers          map[string]*fakeSonosSpeaker
	unreachable       map[string]bool
	failedCalls       map[string]int // "speaker/action" -> count of refused requests
	calls             map[string]int // "speaker/action" -> count of answered requests
	groups            string         // what the groups action reports
	ignoreVolumeValue int            // acknowledge this setter without changing speaker state
}

func newFakeSoCoServer() *fakeSoCoServer {
	return &fakeSoCoServer{
		speakers:    make(map[string]*fakeSonosSpeaker),
		unreachable: make(map[string]bool),
		failedCalls: make(map[string]int),
		calls:       make(map[string]int),
	}
}

// setPlaying makes a speaker report that it is playing uri at the given volume.
func (f *fakeSoCoServer) setPlaying(speaker, uri string, volume int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.speakers[speaker] = &fakeSonosSpeaker{transport: "PLAYING", volume: volume, currentURI: uri}
}

// setGroups sets the text the groups action reports, one "Coordinator: member, member" line per group.
func (f *fakeSoCoServer) setGroups(groups string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups = groups
}

// callCount reports how many requests for an action the speaker answered.
func (f *fakeSoCoServer) callCount(speaker, action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[speaker+"/"+action]
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

	f.calls[speaker+"/"+action]++
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
		requested, _ := strconv.Atoi(args[0])
		if requested != f.ignoreVolumeValue {
			s.volume = requested
		} else {
			s.volume = 0
		}
	case "clear_queue":
		s.queue = nil
		s.transport = "STOPPED"
	case "add_uri_to_queue":
		uri := strings.Join(args, "/")
		s.queue = append(s.queue, uri)
		s.currentURI = uri
	case "sharelink":
		uri := strings.Join(args, "/")
		s.queue = append(s.queue, uri)
		s.currentURI = uri
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
	case "get_uri":
		reply(0, s.currentURI, "")
		return
	case "groups":
		reply(0, f.groups, "")
		return
	case "group":
		if len(args) > 0 {
			f.groups = args[0] + ": " + speaker
		}
	}
	reply(0, "", "")
}

// TestScenario_MusicFade_DoesNotReportCompletionUntilVolumeConverges reproduces
// the 2026-11-19 wake incident where SoCo acknowledged every setter but Sonos
// remained at volume zero.
//
// INVARIANT: A fade is complete only when a read-back confirms its final volume.
func TestScenario_MusicFade_DoesNotReportCompletionUntilVolumeConverges(t *testing.T) {
	t.Parallel()

	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	var musicConfig music.MusicConfig
	require.NoError(t, yaml.Unmarshal([]byte(fadeVerificationMusicConfig), &musicConfig))

	soco := newFakeSoCoServer()
	soco.ignoreVolumeValue = 9
	soco.setPlaying("Pantry", "", 0)
	socoServer := httptest.NewServer(soco)
	defer socoServer.Close()

	logger := testlogger.New()
	musicMgr := music.NewManager(context.Background(), client, stateManager, &musicConfig, logger, false, nil, nil)
	musicMgr.SetSleepFunc(func(time.Duration) {})
	musicMgr.SetSoCoClient(music.NewSoCoClient(socoServer.URL, logger, false))
	musicMgr.SetDebounceDelay(0)

	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	require.NoError(t, musicMgr.Start())
	defer musicMgr.Stop()
	waitForProcessing(t, stateManager)

	t.Log("GIVEN: SoCo acknowledges volume writes but the speaker remains at volume zero")
	t.Log("WHEN: Morning starts and Pantry fades toward its configured volume")
	server.SetState("input_text.day_phase", "morning", map[string]interface{}{})

	t.Log("THEN: The fade is recorded as state_not_converged, not completed")
	waitForCondition(t, func() bool {
		for _, fade := range musicMgr.GetShadowState().Outputs.FadeInProgress {
			if fade.SpeakerName == "Pantry" && !fade.IsActive && fade.Outcome == "state_not_converged" {
				return true
			}
		}
		return false
	}, "fade should retain its failed verification outcome")

	var fade shadowstate.SpeakerFadeIn
	for _, candidate := range musicMgr.GetShadowState().Outputs.FadeInProgress {
		if candidate.SpeakerName == "Pantry" {
			fade = candidate
		}
	}
	require.Equal(t, 9, fade.ExpectedVolume)
	require.Equal(t, 0, fade.ActualVolume)
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

// TestScenario_SleepZoneFullStartWhenSleepPrepRetrying checks that if sleep-prep
// is retrying when the sleep zone starts, the sleep zone runs a full start (not a
// seamless transition). A seamless transition inherits the stopping zone's
// playback; if the stopping zone was retrying, there is nothing to inherit, so the
// new zone would stay silent.
//
// INVARIANT: A zone transition that would normally be seamless falls back to a full
// start when the stopping zone's PlaybackStatus is ZonePlaybackRetrying.
func TestScenario_SleepZoneFullStartWhenSleepPrepRetrying(t *testing.T) {
	t.Parallel()

	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	var musicConfig music.MusicConfig
	require.NoError(t, yaml.Unmarshal([]byte(seamlessGuardTestConfig), &musicConfig))

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

	// GIVEN: sleep-prep cannot start because Pantry is unreachable when night begins.
	// sleep-prep and sleep share both Pantry and the rain URI, so without the
	// seamless-transition guard, findSeamlessTransitions would classify
	// sleep-prep→sleep as seamless. The seamless path keeps the stopping zone's
	// playback, but sleep-prep never loaded its playlist, so the speakers would
	// remain silent.
	t.Log("GIVEN: Pantry is unreachable when night begins — sleep-prep is retrying")
	soco.setUnreachable("Pantry", true)
	server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	waitForCondition(t, func() bool {
		return zonePlaybackStatus(musicMgr, "sleep-prep") == music.ZonePlaybackRetrying
	}, "sleep-prep should be retrying because Pantry is unreachable")

	// WHEN: Master falls asleep — sleep-prep stops, sleep starts.
	t.Log("WHEN: Master falls asleep; sleep-prep stops and sleep zone takes over")
	server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	waitForCondition(t, func() bool {
		return zonePlaybackStatus(musicMgr, "sleep-prep") == ""
	}, "sleep-prep should stop when master is asleep")

	// Pantry recovers — the sleep zone's full start (not a seamless handoff) can succeed.
	t.Log("AND: Pantry recovers")
	soco.setUnreachable("Pantry", false)

	// THEN: sleep zone runs a full start and rain plays on Pantry.
	// If the seamless guard had not fired, sleep zone would have inherited
	// sleep-prep's empty playback and Pantry would stay silent.
	t.Log("THEN: Rain sounds play on Pantry via the sleep zone's full start")
	waitForCondition(t, func() bool {
		return soco.isPlaying("Pantry", startRetryRainURI)
	}, "Pantry must play the rain playlist — sleep zone must do a full start, not a silent seamless handoff")
	waitForCondition(t, func() bool {
		return zonePlaybackStatus(musicMgr, "sleep") == music.ZonePlaybackPlaying
	}, "sleep zone must be marked as playing")
}
