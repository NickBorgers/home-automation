package music

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/pkg/plugin"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProductionConfig_EarlyWakeBeforeDawn checks the early wake zones in the shipped music config.
//
// User story: "I get up at 5 a.m. and turn on a living room light. The house wakes up with
// morning music, but the primary bedroom keeps its rain sounds because my partner is asleep."
//
// Before 06:00 (or before dawn) dayPhase is "night", so the morning zone must not depend on
// dayPhase while isEarlyWakeActive is true.
//
// INVARIANTS:
//   - Primary Bed stays in the sleep zone while isMasterAsleep is true.
//   - Primary Bed is never part of the morning zone while isMasterAsleep is true.
//   - The morning zone plays on the other speakers.
func TestProductionConfig_EarlyWakeBeforeDawn(t *testing.T) {
	t.Parallel()

	config, err := LoadConfig(filepath.Join(findRepoRoot(t), "configs", "music_config.yaml"))
	require.NoError(t, err, "Failed to load production music config")

	env := testutil.NewEnv(t)
	timeProvider := plugin.FixedTimeProvider{FixedTime: time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC)}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, timeProvider, nil)
	manager.SetSleepFunc(func(time.Duration) {})

	// GIVEN: 5 a.m., the bedroom is asleep with rain sounds, someone just turned on a living light.
	_ = env.StateMgr.SetString("dayPhase", "night")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isMasterAsleep", true)
	_ = env.StateMgr.SetBool("isAnyoneAsleep", true)
	_ = env.StateMgr.SetBool("isWakeSequenceActive", false)
	_ = env.StateMgr.SetBool("isAnyoneHomeAndAwake", true)
	_ = env.StateMgr.SetString("musicPlaybackType", "sleep")
	_ = env.StateMgr.SetBool("isTVPlaying", false)
	require.NoError(t, env.StateMgr.SetBool("isEarlyWakeActive", true))

	require.NoError(t, manager.Start())
	defer manager.Stop()
	time.Sleep(100 * time.Millisecond)
	manager.WaitForSync()

	// THEN
	zoneToSpeakers, _ := manager.zoneManager.assignSpeakersToZones()

	sleepSpeakers, sleepActive := zoneToSpeakers["sleep"]
	require.True(t, sleepActive, "sleep zone must stay active for the sleeping bedroom")
	assert.Contains(t, sleepSpeakers, "Primary Bed", "Primary Bed must keep its rain sounds")

	morningSpeakers, morningActive := zoneToSpeakers["morning"]
	require.True(t, morningActive, "morning zone must start before dawn during an early wake, got zones: %v",
		zoneToSpeakers)
	assert.NotEmpty(t, morningSpeakers, "morning zone must have speakers")
	assert.NotContains(t, morningSpeakers, "Primary Bed", "Primary Bed must not join morning music while asleep")
}

// TestProductionConfig_NoEarlyWakeNoMorningAtNight checks that the night stays quiet without an early wake.
//
// INVARIANT: a night trip (a feeding, the bathroom) never starts morning music by itself.
func TestProductionConfig_NoEarlyWakeNoMorningAtNight(t *testing.T) {
	t.Parallel()

	config, err := LoadConfig(filepath.Join(findRepoRoot(t), "configs", "music_config.yaml"))
	require.NoError(t, err, "Failed to load production music config")

	env := testutil.NewEnv(t)
	timeProvider := plugin.FixedTimeProvider{FixedTime: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, timeProvider, nil)
	manager.SetSleepFunc(func(time.Duration) {})

	_ = env.StateMgr.SetString("dayPhase", "night")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isMasterAsleep", true)
	_ = env.StateMgr.SetBool("isAnyoneAsleep", true)
	_ = env.StateMgr.SetBool("isWakeSequenceActive", false)
	_ = env.StateMgr.SetBool("isAnyoneHomeAndAwake", false)
	_ = env.StateMgr.SetString("musicPlaybackType", "sleep")
	_ = env.StateMgr.SetBool("isTVPlaying", false)
	require.NoError(t, env.StateMgr.SetBool("isEarlyWakeActive", false))

	require.NoError(t, manager.Start())
	defer manager.Stop()
	time.Sleep(100 * time.Millisecond)
	manager.WaitForSync()

	zoneToSpeakers, _ := manager.zoneManager.assignSpeakersToZones()
	_, morningActive := zoneToSpeakers["morning"]
	assert.False(t, morningActive, "morning zone must not start at night without an early wake")
	_, sleepActive := zoneToSpeakers["sleep"]
	assert.True(t, sleepActive, "sleep zone must stay active")
}
