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

	"homeautomation/internal/config"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/sleephygiene"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// SoCo-CLI gets one request at a time
//
// PRODUCTION BUG (2026-11-19, issue #1431):
// soco-cli's HTTP server runs each request on a thread pool and captures the
// command's output by swapping the process-wide stdout without a lock. When
// two requests overlap, one of them still runs on the speaker but its HTTP
// response carries an empty result with exit_code 0. At the wake alarm the
// bedroom fade-out reads the volume of both sleep-zone speakers at the same
// moment, so one read came back empty and that speaker's fade went wrong.
//
// INVARIANTS:
// - The Go client never has two SoCo-CLI requests in flight at once.
// - At the alarm, each sleep-zone speaker fades from its real volume to 0.
// ============================================================================

// overlapSensitiveSoCo imitates soco-cli's lost-output bug. Every request
// takes a short time. A request that arrives while another is in flight still
// runs, but its reply has an empty result, as the real server's does.
type overlapSensitiveSoCo struct {
	hold time.Duration

	mu         sync.Mutex
	inFlight   int
	overlaps   int
	emptyReads int
	volumes    map[string]int
	writes     map[string][]int
}

func newOverlapSensitiveSoCo(hold time.Duration, volumes map[string]int) *overlapSensitiveSoCo {
	return &overlapSensitiveSoCo{hold: hold, volumes: volumes, writes: map[string][]int{}}
}

func (f *overlapSensitiveSoCo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	overlapped := f.inFlight > 0
	if overlapped {
		f.overlaps++
	}
	f.inFlight++
	f.mu.Unlock()

	time.Sleep(f.hold)

	f.mu.Lock()
	f.inFlight--
	result := ""
	if action == "volume" {
		if len(args) == 0 {
			result = strconv.Itoa(f.volumes[speaker])
			if overlapped {
				f.emptyReads++
			}
		} else if level, err := strconv.Atoi(args[0]); err == nil {
			f.volumes[speaker] = level
			f.writes[speaker] = append(f.writes[speaker], level)
		}
	}
	if overlapped {
		result = "" // The command ran, but its output went to another request.
	}
	f.mu.Unlock()

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"speaker": speaker, "action": action, "exit_code": 0, "result": result, "error_msg": "",
	})
}

func (f *overlapSensitiveSoCo) stats() (overlaps, emptyReads int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.overlaps, f.emptyReads
}

func (f *overlapSensitiveSoCo) writesFor(speaker string) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.writes[speaker]...)
}

func TestScenario_WakeFadeOut_SoCoRequestsDoNotOverlap(t *testing.T) {
	t.Parallel()
	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	start := map[string]int{"Primary Bed": 10, "Primary Bath": 6}
	soco := newOverlapSensitiveSoCo(5*time.Millisecond, map[string]int{"Primary Bed": 10, "Primary Bath": 6})
	socoServer := httptest.NewServer(soco)
	defer socoServer.Close()

	logger := testlogger.New()
	sleepHygiene := sleephygiene.NewManager(context.Background(), client, stateManager,
		config.NewLoader("../../configs", logger), logger, false, nil, nil)
	sleepHygiene.SetSpeakerClient(music.NewSoCoClient(socoServer.URL, logger, false))
	sleepHygiene.SetSleepFunc(func(time.Duration) {})

	server.SetState("sensor.nick_s_eight_sleep_side_bed_state_type", "none", nil)
	server.SetState("sensor.caroline_s_eight_sleep_side_bed_state_type", "none", nil)
	require.NoError(t, sleepHygiene.Start())
	defer sleepHygiene.Stop()
	waitForProcessing(t, stateManager)

	// GIVEN: Master asleep with rain on both sleep-zone speakers (Primary Bed 10, Primary Bath 6).
	t.Log("GIVEN: Master asleep with sleep music on both bedroom speakers")
	server.SetState("input_boolean.anyone_home", "on", nil)
	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_text.music_playback_type", "sleep", nil)
	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	server.SetState("input_boolean.fade_out_in_progress", "off", nil)
	waitForProcessing(t, stateManager)

	// WHEN: The alarm fires. Both speakers' fades start together and read their volumes at once.
	t.Log("WHEN: The Eight Sleep alarm fires and both bedroom fades start at the same time")
	server.SetState("sensor.nick_s_eight_sleep_side_bed_state_type", "alarm", nil)
	waitForCondition(t, func() bool {
		s := server.GetState("input_boolean.fade_out_in_progress")
		return s != nil && s.State == "on"
	}, "bedroom fade-out should start")
	waitForCondition(t, func() bool {
		s := server.GetState("input_boolean.fade_out_in_progress")
		return s != nil && s.State == "off"
	}, "bedroom fade-out should finish")

	// THEN: SoCo-CLI never saw two requests at once, so no read came back empty.
	overlaps, emptyReads := soco.stats()
	t.Logf("THEN: no overlapping SoCo-CLI requests (overlaps=%d, empty volume reads=%d)", overlaps, emptyReads)
	assert.Zero(t, overlaps, "the client must send one SoCo-CLI request at a time")
	assert.Zero(t, emptyReads, "no volume read may come back empty")

	// THEN: Each speaker fades 1% at a time from its real volume to 0.
	t.Log("THEN: each bedroom speaker fades from its real volume to 0")
	for speaker, from := range start {
		levels := soco.writesFor(speaker)
		t.Logf("%s fade: %v", speaker, levels)
		require.Len(t, levels, from, "%s must step down 1%% at a time from %d to 0", speaker, from)
		for i, level := range levels {
			assert.Equal(t, from-1-i, level, "%s fade step %d", speaker, i)
		}
	}
}
