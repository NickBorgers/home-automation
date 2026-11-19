package integration

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/config"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/sleephygiene"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Primary Bath follows the bedroom from sleep into morning
//
// PRODUCTION BUG (2026-11-17):
// The sleep zone plays rain on Primary Bed and Primary Bath. At the Eight
// Sleep alarm the music plugin stops the sleep zone without a fade, because
// sleephygiene owns the slow bedroom fade. sleephygiene faded only Primary
// Bed. The morning zone did not include Primary Bath. Nothing stopped Primary
// Bath, so it played rain for hours into the morning.
//
// INVARIANTS:
// - At the alarm, every sleep-zone speaker fades out gradually to 0.
// - One speaker finishing its fade does not cut the other speaker's fade short.
// - When the master wakes, Primary Bath joins the morning group.
// ============================================================================

// perSpeakerVolumeClient records the volume of each Sonos speaker separately.
type perSpeakerVolumeClient struct {
	mu      sync.Mutex
	volumes map[string]int
	levels  map[string][]int
	readErr error
}

func newPerSpeakerVolumeClient(start map[string]int) *perSpeakerVolumeClient {
	return &perSpeakerVolumeClient{volumes: start, levels: map[string][]int{}}
}

func (c *perSpeakerVolumeClient) GetVolume(speaker string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return 0, c.readErr
	}
	return c.volumes[speaker], nil
}

func (c *perSpeakerVolumeClient) SetVolume(speaker string, level int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.volumes[speaker] = level
	c.levels[speaker] = append(c.levels[speaker], level)
	return nil
}

func TestScenario_WakeSequence_FailedVolumeReadDoesNotInventHighVolume(t *testing.T) {
	t.Parallel()
	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	logger := testlogger.New()
	speakers := newPerSpeakerVolumeClient(nil)
	speakers.readErr = errors.New("volume read failed")
	sleepHygiene := sleephygiene.NewManager(context.Background(), client, stateManager,
		config.NewLoader("../../configs", logger), logger, false, nil, nil)
	sleepHygiene.SetSpeakerClient(speakers)
	sleepHygiene.SetSleepFunc(func(time.Duration) {})

	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_boolean.anyone_home", "on", nil)
	server.SetState("input_text.music_playback_type", "sleep", nil)
	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	server.SetState("input_boolean.fade_out_in_progress", "off", nil)
	require.NoError(t, sleepHygiene.Start())
	defer sleepHygiene.Stop()
	waitForProcessing(t, stateManager)

	// GIVEN: Sleep music is playing, but SoCo-CLI cannot provide a valid starting volume.
	t.Log("GIVEN: sleep music is playing and the speaker volume read fails")
	// WHEN: The wake sequence begins and attempts its fade at T+0.
	t.Log("WHEN: the wake sequence begins")
	sleepHygiene.TriggerBeginWakeForTest()
	waitForCondition(t, func() bool {
		s := server.GetState("input_boolean.fade_out_in_progress")
		return s != nil && s.State == "off"
	}, "fade should safely finish after a failed volume read")

	// THEN: A failed read must never invent a high starting volume or write fade steps from it.
	t.Log("THEN: no manufactured volume is written to either sleep-zone speaker")
	assert.Empty(t, speakers.Levels("Primary Bed"))
	assert.Empty(t, speakers.Levels("Primary Bath"))
}

func (c *perSpeakerVolumeClient) Levels(speaker string) []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.levels[speaker]...)
}

func TestScenario_WakeSequence_PrimaryBathFadesThenJoinsMorning(t *testing.T) {
	t.Parallel()
	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	logger := testlogger.New()
	speakers := newPerSpeakerVolumeClient(map[string]int{"Primary Bed": 20, "Primary Bath": 12})

	stateTracking := statetracking.NewManager(context.Background(), client, stateManager, logger, false, nil, "",
		&alert.MockAlerter{})
	// The production config decides which speakers each zone uses, so the test reads it.
	musicConfig, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)
	musicManager := music.NewManager(context.Background(), client, stateManager, musicConfig, logger, false, nil, nil)
	sleepHygiene := sleephygiene.NewManager(context.Background(), client, stateManager,
		config.NewLoader("../../configs", logger), logger, false, nil, nil)
	sleepHygiene.SetSpeakerClient(speakers)
	musicManager.SetSleepFunc(func(time.Duration) {})
	sleepHygiene.SetSleepFunc(func(time.Duration) {})

	server.SetState("sensor.nick_s_eight_sleep_side_bed_state_type", "none", nil)
	server.SetState("sensor.caroline_s_eight_sleep_side_bed_state_type", "none", nil)
	server.SetState("light.primary_suite", "off", nil)

	require.NoError(t, stateTracking.Start())
	require.NoError(t, musicManager.Start())
	require.NoError(t, sleepHygiene.Start())
	defer func() {
		sleepHygiene.Stop()
		musicManager.Stop()
		stateTracking.Stop()
	}()
	waitForProcessing(t, stateManager)

	// GIVEN: Morning, master asleep, rain on Primary Bed (20) and Primary Bath (12).
	// Production never writes currentlyPlayingMusic, so the test leaves it unset.
	t.Log("GIVEN: Master asleep with rain playing on Primary Bed and Primary Bath")
	server.SetState("input_text.day_phase", "morning", nil)
	server.SetState("input_boolean.anyone_home", "on", nil)
	server.SetState("input_boolean.nick_home", "on", nil)
	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_boolean.anyone_asleep", "on", nil)
	server.SetState("input_boolean.everyone_asleep", "on", nil)
	server.SetState("input_text.music_playback_type", "sleep", nil)
	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	server.SetState("input_boolean.fade_out_in_progress", "off", nil)
	waitForProcessing(t, stateManager)
	waitForServiceCallQuiescenceSince(t, server, 0, 200*time.Millisecond)

	// WHEN: The Eight Sleep alarm fires.
	t.Log("WHEN: The Eight Sleep alarm fires")
	server.SetState("sensor.nick_s_eight_sleep_side_bed_state_type", "alarm", nil)
	waitForCondition(t, func() bool {
		s := server.GetState("input_boolean.fade_out_in_progress")
		return s != nil && s.State == "on"
	}, "bedroom fade-out should start")
	waitForCondition(t, func() bool {
		s := server.GetState("input_boolean.fade_out_in_progress")
		return s != nil && s.State == "off"
	}, "bedroom fade-out should finish")

	// THEN: Both sleep-zone speakers fade gradually all the way to 0.
	t.Log("THEN: Primary Bath and Primary Bed both fade gradually to 0")
	for speaker, start := range map[string]int{"Primary Bath": 12, "Primary Bed": 20} {
		levels := speakers.Levels(speaker)
		t.Logf("%s fade: %v", speaker, levels)
		require.Len(t, levels, start, "%s must step down 1%% at a time from %d to 0", speaker, start)
		for i, level := range levels {
			assert.Equal(t, start-1-i, level, "%s fade step %d", speaker, i)
		}
	}

	// WHEN: The master gets up.
	snapshot := server.ServiceCallCount()
	t.Log("WHEN: The master wakes up (isMasterAsleep=false)")
	server.SetState("input_boolean.master_asleep", "off", nil)
	server.SetState("input_boolean.anyone_asleep", "off", nil)
	server.SetState("input_boolean.everyone_asleep", "off", nil)

	// THEN: Primary Bath joins the morning group led by Pantry.
	t.Log("THEN: Primary Bath joins the morning group")
	waitForCondition(t, func() bool {
		for _, call := range filterServiceCalls(server.GetServiceCallsSince(snapshot), "media_player", "join") {
			members, _ := call.ServiceData["group_members"].([]interface{})
			for _, member := range members {
				if member == "media_player.primary_bath" && call.ServiceData["entity_id"] == "media_player.pantry" {
					return true
				}
			}
		}
		return false
	}, "Primary Bath should join the morning group led by Pantry")
}

// TestScenario_PrimaryBathFollowsPrimaryBedAllDay checks that Primary Bath plays
// with Primary Bed in every household zone, under the same rules. The bedroom
// and bathroom are one suite, so they play the same music all day.
func TestScenario_PrimaryBathFollowsPrimaryBedAllDay(t *testing.T) {
	t.Log("GIVEN: the production music zones")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	t.Log("WHEN: Primary Bed and Primary Bath are compared in each household zone")
	t.Log("THEN: Primary Bath is in every zone that has Primary Bed, with the same rules")
	// Rain zones set their own volumes per room (Issue #1412), so only music zones compare volume.
	// sex and wakeup are manually triggered zones — excluded intentionally.
	zones := map[string]bool{"morning": true, "day": true, "evening": true, "winddown": true,
		"sleep-prep": false, "sleep": false}
	for zone, sameVolume := range zones {
		mode, ok := config.Music[zone]
		require.True(t, ok, "zone %s must exist", zone)
		var bed, bath *music.Participant
		for i := range mode.Participants {
			switch mode.Participants[i].PlayerName {
			case "Primary Bed":
				bed = &mode.Participants[i]
			case "Primary Bath":
				bath = &mode.Participants[i]
			}
		}
		require.NotNil(t, bed, "Primary Bed must be in %s", zone)
		require.NotNil(t, bath, "Primary Bath must follow Primary Bed into %s", zone)
		if sameVolume {
			assert.Equal(t, bed.BaseVolume, bath.BaseVolume, "base_volume in %s", zone)
		}
		assert.ElementsMatch(t, bed.LeaveMutedIf, bath.LeaveMutedIf, "leave_muted_if in %s", zone)
		assert.ElementsMatch(t, bed.ExcludeIf, bath.ExcludeIf, "exclude_if in %s", zone)
	}
}
