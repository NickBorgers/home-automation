package music

// =============================================================================
// WAKE-UP DETECTION SCENARIO TESTS
// =============================================================================
//
// PURPOSE:
// These tests validate that the Music Manager correctly detects wake-up events
// and triggers morning music. This is a critical behavior difference between
// Node-RED and the current Go implementation.
//
// CURRENT STATUS: FAILING (demonstrates the bug)
// The Go implementation does NOT correctly detect wake-up events because
// handleStateChange() does not pass trigger context to selectAppropriateMusicMode().
//
// NODE-RED REFERENCE:
// - Flow: Music (90f5fe8cb80ae6a7)
// - URL: https://node-red.featherback-mermaid.ts.net/#flow/90f5fe8cb80ae6a7
// - Function: "Set music type based on conditions" (node e461ac8aeac7cb0c)
//
// NODE-RED LOGIC (flows.json line 10167):
// ```javascript
// // Only play music if someone is home
// if (global.get("state").isAnyoneHome.value == false) {
//     msg.payload = ""
//     return msg
// }
// // If anyone is asleep, set to sleep
// if (global.get("state").isAnyoneAsleep.value) {
//     msg.payload = "sleep"
//     return msg
// }
//
// var dayPhase = global.get("state").dayPhase.value
//
// // If it's day time
// if (dayPhase == "day" || dayPhase == "morning") {
//     // If what changed was the last person waking up, kick off some music
//     if (msg.topic == "isAnyoneAsleep" && msg.payload == false) {
//         // Sunday override
//         var date = new Date();
//         var daynum = date.getDay();
//         // If day is not Sunday
//         if (daynum != 0) {
//             msg.payload = "morning"
//             return msg
//         }
//     }
//     // If noone is asleep then day starts
//     if (global.get("state").isAnyoneAsleep.value == false) {
//         msg.payload = "day"
//         return msg
//     }
// // If it's sunset
// } else if (dayPhase == "sunset" || dayPhase == "dusk") {
//     msg.payload = "evening"
//     return msg
// } else if (dayPhase == "winddown" || dayPhase == "night") {
//     // Override for when sleep sounds get started a little early
//     if (global.get("state").musicPlaybackType.value == "sleep") {
//         return null
//     }
//     msg.payload = "winddown"
//     return msg
// }
// ```
//
// KEY INSIGHT:
// Node-RED passes msg.topic and msg.payload to identify WHAT triggered the
// function. When isAnyoneAsleep changes to false (wake-up event), it triggers
// morning music. Without this context, the Go implementation always chooses
// "day" music during the morning phase.
//
// THE BUG:
// In manager.go, handleStateChange() calls selectAppropriateMusicMode() without
// passing the trigger key or detecting that it's a wake-up event:
//
// ```go
// func (m *Manager) handleStateChange(key string, oldValue, newValue interface{}) {
//     m.selectAppropriateMusicMode()  // Always calls with isWakeUpEvent=false!
// }
// ```
//
// THE FIX:
// handleStateChange() should detect wake-up events:
//
// ```go
// func (m *Manager) handleStateChange(key string, oldValue, newValue interface{}) {
//     // Detect wake-up event: isAnyoneAsleep changed from true to false
//     isWakeUpEvent := key == "isAnyoneAsleep" &&
//                      oldValue == true &&
//                      newValue == false
//     m.selectAppropriateMusicModeWithContext(key, isWakeUpEvent)
// }
// ```
//
// =============================================================================

import (
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// createWakeupTestConfig creates a minimal configuration with morning and day modes
// that mirrors the actual music_config.yaml structure.
func createWakeupTestConfig() *MusicConfig {
	return &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   9,
						LeaveMutedIf: []MuteCondition{},
					},
					{
						PlayerName:   "Bedroom",
						BaseVolume:   9,
						LeaveMutedIf: []MuteCondition{},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{
						URI:              "spotify:playlist:morning_instrumental",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
			"day": {
				Participants: []Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   9,
						LeaveMutedIf: []MuteCondition{},
					},
					{
						PlayerName:   "Soundbar",
						BaseVolume:   10,
						LeaveMutedIf: []MuteCondition{},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{
						URI:              "spotify:playlist:day_chill",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
			"evening": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:evening", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"winddown": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 10, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:winddown", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"sleep": {
				Participants: []Participant{
					{PlayerName: "Bedroom", BaseVolume: 16, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "http://rain-sounds.example.com/rain.m4a", MediaType: "music", VolumeMultiplier: 1.0},
				},
			},
			"sex": {
				Participants: []Participant{
					{PlayerName: "Bedroom", BaseVolume: 10, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:sex", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"wakeup": {
				Participants: []Participant{
					{PlayerName: "Bedroom", BaseVolume: 6, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:wakeup", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}
}

// =============================================================================
// TEST: Wake-Up During Morning Phase Should Trigger Morning Music
// =============================================================================
//
// SCENARIO:
// - Time: Monday 7:00 AM (morning dayPhase)
// - Initial state: Someone is asleep, sleep music playing
// - Event: Last person wakes up (isAnyoneAsleep: true → false)
//
// CORRECT BEHAVIOR (per Node-RED):
// → musicPlaybackType should change to "morning"
//
// The wake-up event during morning phase is special - it triggers energizing
// morning music (upbeat instrumental house/techno) rather than the calmer
// day music. This helps people start their day with energy.
//
// Node-RED detects this by checking the trigger source:
//
//	if (msg.topic == "isAnyoneAsleep" && msg.payload == false) {
//	    msg.payload = "morning"  // Wake-up triggers morning music!
//	}
//
// CURRENT BUG:
// The Go implementation returns "day" instead of "morning" because
// handleStateChange() doesn't pass trigger context to the music selection logic.
// It always calls selectAppropriateMusicMode() with isWakeUpEvent=false.
func TestScenario_WakeUpDuringMorning_TriggersMorningMusic(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createWakeupTestConfig()

	// Use fixed time: Monday 7:00 AM (not Sunday!)
	fixedTime := time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC) // Monday
	require.Equal(t, time.Monday, fixedTime.Weekday(), "Test requires Monday")
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// ==========================================================
	// INITIAL STATE: Morning phase, someone is asleep
	// ==========================================================
	_ = stateManager.SetString("dayPhase", "morning")
	_ = stateManager.SetString("musicPlaybackType", "sleep") // Sleep music was playing
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", true) // Someone IS asleep
	_ = stateManager.SetBool("isMasterAsleep", true)
	_ = stateManager.SetBool("isGuestAsleep", false)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	// Start the manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Allow initial processing
	time.Sleep(50 * time.Millisecond)

	// Clear any initial service calls
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Wake-up event - isAnyoneAsleep changes to false
	// ==========================================================
	// This simulates the last person waking up in the morning.
	// Node-RED would receive: msg.topic = "isAnyoneAsleep", msg.payload = false
	//
	// IMPORTANT: We use SimulateStateChange (not SetBool) to properly trigger
	// the subscription callback with correct old/new values. SetBool updates
	// the state manager's cache before the mock client callback fires, causing
	// the callback to see old=false, new=false instead of old=true, new=false.

	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")

	// Allow time for state change to propagate and trigger music mode selection
	time.Sleep(50 * time.Millisecond)

	// ==========================================================
	// VERIFICATION: Morning music should be selected
	// ==========================================================
	// Per Node-RED logic:
	// - dayPhase is "morning"
	// - The trigger was isAnyoneAsleep changing to false (wake-up event)
	// - It's not Sunday
	// - Therefore: morning music should play

	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)

	// THIS IS THE KEY ASSERTION
	// Current bug: Go returns "day" instead of "morning"
	assert.Equal(t, "morning", musicType,
		"Wake-up during morning phase should trigger MORNING music, not day music. "+
			"Node-RED checks: if (msg.topic == 'isAnyoneAsleep' && msg.payload == false) "+
			"and returns 'morning' when dayPhase is 'morning' and it's not Sunday.")
}

// =============================================================================
// TEST: Wake-Up On Sunday Should Trigger Day Music (Not Morning)
// =============================================================================
//
// SCENARIO:
// - Time: Sunday 8:00 AM (morning dayPhase)
// - Initial state: Someone is asleep, sleep music playing
// - Event: Last person wakes up (isAnyoneAsleep: true → false)
//
// CORRECT BEHAVIOR (per Node-RED):
// → musicPlaybackType should change to "day" (NOT "morning")
//
// Sundays are treated specially - no energizing morning music. The family
// gets a more relaxed start with calmer day music instead.
//
// Node-RED logic explicitly checks for Sunday:
//
//	if (msg.topic == "isAnyoneAsleep" && msg.payload == false) {
//	    var daynum = date.getDay();
//	    if (daynum != 0) {  // 0 = Sunday - skip morning music!
//	        msg.payload = "morning"
//	        return msg
//	    }
//	}
//	// Falls through to "day" music on Sundays
//
// NOTE: This test PASSES because both the buggy Go code and the correct
// behavior result in "day" music (Go never triggers morning music anyway).
func TestScenario_WakeUpOnSunday_TriggersDayMusic(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createWakeupTestConfig()

	// Use fixed time: Sunday 8:00 AM
	fixedTime := time.Date(2024, 1, 14, 8, 0, 0, 0, time.UTC) // Sunday
	require.Equal(t, time.Sunday, fixedTime.Weekday(), "Test requires Sunday")
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initial state: Morning phase, someone asleep
	_ = stateManager.SetString("dayPhase", "morning")
	_ = stateManager.SetString("musicPlaybackType", "sleep")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", true)
	_ = stateManager.SetBool("isMasterAsleep", true)
	_ = stateManager.SetBool("isGuestAsleep", false)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ACTION: Wake-up event on Sunday
	// Use SimulateStateChange to properly trigger the subscription callback
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")

	time.Sleep(50 * time.Millisecond)

	// VERIFICATION: Day music (Sunday override)
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)

	assert.Equal(t, "day", musicType,
		"Wake-up on SUNDAY should trigger DAY music (Sunday override). "+
			"Node-RED checks daynum != 0 before returning 'morning'.")
}

// =============================================================================
// TEST: Sunday Check Uses Configured Timezone (Issue #450)
// =============================================================================
//
// SCENARIO (BUG CASE):
// - UTC time: Sunday 00:30 AM (January 14, 2024)
// - CST time: Saturday 6:30 PM (January 13, 2024) - NOT Sunday locally!
// - Event: Wake-up during morning phase
//
// BUG BEHAVIOR (before fix):
// → Used UTC weekday, incorrectly detected Sunday, played DAY music
//
// CORRECT BEHAVIOR (after fix):
// → Uses configured timezone (CST), correctly detects Saturday, plays MORNING music
//
// This tests the fix for Issue #450: The music manager's Sunday check must use
// the configured local timezone instead of UTC to avoid edge cases at midnight.
func TestScenario_WakeUp_UsesLocalTimezoneForSundayCheck(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createWakeupTestConfig()

	// Load CST timezone (America/Chicago)
	cst, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err, "Failed to load America/Chicago timezone")

	// Create a time that is:
	// - Sunday 00:30 AM UTC (January 14, 2024)
	// - Saturday 6:30 PM CST (January 13, 2024)
	utcTime := time.Date(2024, 1, 14, 0, 30, 0, 0, time.UTC)
	require.Equal(t, time.Sunday, utcTime.Weekday(), "Test requires Sunday in UTC")
	require.Equal(t, time.Saturday, utcTime.In(cst).Weekday(), "Test requires Saturday in CST")

	timeProvider := plugin.FixedTimeProvider{FixedTime: utcTime}

	// Pass CST as the configured timezone
	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, cst)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initial state: Morning phase, someone asleep
	_ = stateManager.SetString("dayPhase", "morning")
	_ = stateManager.SetString("musicPlaybackType", "sleep")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", true)
	_ = stateManager.SetBool("isMasterAsleep", true)
	_ = stateManager.SetBool("isGuestAsleep", false)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	err = manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ACTION: Wake-up event
	// UTC: Sunday 00:30 AM (but CST: Saturday 6:30 PM)
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")

	time.Sleep(50 * time.Millisecond)

	// VERIFICATION: Should use local timezone (CST = Saturday)
	// NOT Sunday, so morning music should play
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)

	assert.Equal(t, "morning", musicType,
		"Wake-up when UTC=Sunday but CST=Saturday should trigger MORNING music "+
			"(Issue #450: Sunday check must use configured timezone, not UTC)")
}

// =============================================================================
// TEST: Day Phase Change (Not Wake-Up) Should Trigger Day Music
// =============================================================================
//
// SCENARIO:
// - Time: Monday 6:00 AM
// - Initial state: Night phase, no one asleep (they stayed up late)
// - Event: dayPhase changes from "night" to "morning" (sunrise)
//
// CORRECT BEHAVIOR (per Node-RED):
// → musicPlaybackType should change to "day" (NOT "morning")
//
// This is an important distinction: morning music is NOT triggered just because
// the dayPhase is "morning". It ONLY triggers when someone actually wakes up
// (isAnyoneAsleep changes from true to false).
//
// In this scenario, no one was asleep, so the sunrise is just a normal phase
// transition that should play calmer day music.
//
// NOTE: This test PASSES - both Go and Node-RED correctly return "day" here.
func TestScenario_DayPhaseChangesToMorning_TriggersDayMusic(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createWakeupTestConfig()

	// Monday 6:00 AM
	fixedTime := time.Date(2024, 1, 15, 6, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initial state: Night phase, no one asleep (maybe they stayed up late)
	_ = stateManager.SetString("dayPhase", "night")
	_ = stateManager.SetString("musicPlaybackType", "winddown")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false) // No one is asleep
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isGuestAsleep", false)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ACTION: Day phase changes to morning (sunrise)
	// This is NOT a wake-up event - no one was asleep
	_ = stateManager.SetString("dayPhase", "morning")

	time.Sleep(50 * time.Millisecond)

	// VERIFICATION: Day music (not morning)
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)

	assert.Equal(t, "day", musicType,
		"Day phase change to 'morning' (without wake-up event) should trigger DAY music. "+
			"Morning music only plays when triggered by someone waking up.")
}

// =============================================================================
// TEST: No One Home - No Music
// =============================================================================
//
// SCENARIO:
// - Time: Monday 10:00 AM
// - Initial state: Day music playing, someone is home
// - Event: Everyone leaves (isAnyoneHome: true → false)
//
// CORRECT BEHAVIOR (per Node-RED):
// → musicPlaybackType should change to "" (empty string = stop music)
//
// This is the highest priority check in Node-RED - if no one is home,
// immediately stop all music regardless of other conditions.
//
// NOTE: This test PASSES - Go implementation handles this correctly.
func TestScenario_NoOneHome_StopsMusic(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createWakeupTestConfig()

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initial state: Day music playing
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("musicPlaybackType", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isGuestAsleep", false)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ACTION: Everyone leaves
	_ = stateManager.SetBool("isAnyoneHome", false)

	time.Sleep(50 * time.Millisecond)

	// VERIFICATION: Music stops
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)

	assert.Equal(t, "", musicType,
		"When no one is home, music should stop (empty musicPlaybackType)")
}

// =============================================================================
// TEST: Someone Falls Asleep - Sleep Music Takes Priority
// =============================================================================
//
// SCENARIO:
// - Time: Monday 10:00 PM (winddown dayPhase)
// - Initial state: Winddown music playing, no one asleep
// - Event: Someone goes to bed (isAnyoneAsleep: false → true)
//
// CORRECT BEHAVIOR (per Node-RED):
// → musicPlaybackType should change to "sleep"
//
// Sleep state has the second-highest priority (after "no one home"). When
// anyone falls asleep, the system immediately switches to soothing rain sounds
// to help them sleep, regardless of the current dayPhase.
//
// NOTE: This test PASSES - Go implementation handles this correctly.
func TestScenario_SomeoneFallsAsleep_TriggersSleepMusic(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createWakeupTestConfig()

	fixedTime := time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC) // 10 PM
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initial state: Winddown music playing
	_ = stateManager.SetString("dayPhase", "winddown")
	_ = stateManager.SetString("musicPlaybackType", "winddown")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isGuestAsleep", false)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ACTION: Someone goes to sleep
	_ = stateManager.SetBool("isAnyoneAsleep", true)
	_ = stateManager.SetBool("isMasterAsleep", true)

	time.Sleep(50 * time.Millisecond)

	// VERIFICATION: Sleep music
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)

	assert.Equal(t, "sleep", musicType,
		"When someone falls asleep, sleep music should take priority")
}

// =============================================================================
// TEST: Sleep Music Persists During Winddown Phase
// =============================================================================
//
// SCENARIO:
// - Time: Monday 9:00 PM
// - Initial state: Dusk phase, user manually started sleep music early
// - Event: dayPhase changes from "dusk" to "winddown"
//
// CORRECT BEHAVIOR (per Node-RED):
// → musicPlaybackType should REMAIN "sleep" (not change to winddown)
//
// This handles the case where someone manually starts sleep sounds before
// the winddown phase. The system should NOT interrupt their relaxation by
// switching to different music just because the dayPhase changed.
//
// Node-RED explicitly checks for this:
//
//	if (dayPhase == "winddown" || dayPhase == "night") {
//	    if (musicPlaybackType.value == "sleep") {
//	        return null  // Don't change anything!
//	    }
//	    msg.payload = "winddown"
//	}
//
// NOTE: This test PASSES - Go implementation handles this correctly.
//
// TEST SETUP:
// 1. Start manager with dusk phase (triggers evening music initially)
// 2. Manually set musicPlaybackType to "sleep" (simulating user action)
// 3. Trigger dayPhase change to "winddown"
// 4. Verify sleep music persists
func TestScenario_SleepMusicPersistsDuringWinddown(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createWakeupTestConfig()

	fixedTime := time.Date(2024, 1, 15, 21, 0, 0, 0, time.UTC) // 9 PM
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initial state: Dusk phase (valid dayPhase), no one asleep but sleep music manually started
	_ = stateManager.SetString("dayPhase", "dusk")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false) // Not actually asleep yet
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isGuestAsleep", false)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)
	// Don't set musicPlaybackType yet - let Start() set it initially

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)

	// Manually set sleep music (simulating user manually started sleep sounds early)
	// This happens AFTER the manager's initial evaluation
	_ = stateManager.SetString("musicPlaybackType", "sleep")

	time.Sleep(100 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ACTION: Day phase changes to winddown
	_ = stateManager.SetString("dayPhase", "winddown")

	time.Sleep(50 * time.Millisecond)

	// VERIFICATION: Sleep music persists
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)

	assert.Equal(t, "sleep", musicType,
		"Sleep music should persist during winddown phase (Node-RED returns null to keep current)")
}

// =============================================================================
// TEST: Full Wake-Up Cycle Simulation
// =============================================================================
//
// This test simulates a complete night-to-day cycle to validate the full
// music mode selection logic in a realistic sequence.
//
// TIMELINE:
// - 5:30 AM (night): Someone asleep, sleep music playing
// - 6:30 AM (morning): Sunrise, still asleep → sleep continues
// - 7:00 AM (morning): Person wakes up → MORNING music starts
// - 9:00 AM (day): Day phase → day music
//
// CORRECT BEHAVIOR (per Node-RED) at each phase:
// - Phase 1: "sleep" (someone is asleep - sleep has priority)
// - Phase 2: "sleep" (still asleep, dayPhase change doesn't override)
// - Phase 3: "morning" ← THIS IS THE KEY TEST (wake-up event triggers morning)
// - Phase 4: "day" (normal day music)
//
// CURRENT BUG:
// Phase 3 returns "day" instead of "morning" because the Go implementation
// doesn't detect that isAnyoneAsleep changing from true→false is a wake-up event.
func TestScenario_FullWakeUpCycle(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createWakeupTestConfig()

	// Start at 5:30 AM Monday (before sunrise)
	fixedTime := time.Date(2024, 1, 15, 5, 30, 0, 0, time.UTC)
	require.Equal(t, time.Monday, fixedTime.Weekday())
	timeProvider := &MutableTimeProvider{currentTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// ==========================================================
	// PHASE 1: Night - someone asleep with sleep music
	// ==========================================================
	_ = stateManager.SetString("dayPhase", "night")
	_ = stateManager.SetString("musicPlaybackType", "sleep")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", true)
	_ = stateManager.SetBool("isMasterAsleep", true)
	_ = stateManager.SetBool("isGuestAsleep", false)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)

	// Verify sleep music
	musicType, _ := stateManager.GetString("musicPlaybackType")
	assert.Equal(t, "sleep", musicType, "Phase 1: Sleep music should play while asleep")

	// ==========================================================
	// PHASE 2: Sunrise - day phase changes to morning, still asleep
	// ==========================================================
	timeProvider.SetTime(time.Date(2024, 1, 15, 6, 30, 0, 0, time.UTC))
	mockClient.ClearServiceCalls()

	_ = stateManager.SetString("dayPhase", "morning")

	time.Sleep(50 * time.Millisecond)

	// Sleep music should continue (someone is still asleep)
	musicType, _ = stateManager.GetString("musicPlaybackType")
	assert.Equal(t, "sleep", musicType, "Phase 2: Sleep music should continue during morning while asleep")

	// ==========================================================
	// PHASE 3: Wake-up event - person wakes up during morning
	// ==========================================================
	timeProvider.SetTime(time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC))
	mockClient.ClearServiceCalls()

	// Use SimulateStateChange to properly trigger the subscription callback with correct old/new values
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	mockClient.SimulateStateChange("input_boolean.master_asleep", "off")

	time.Sleep(50 * time.Millisecond)

	// THIS IS THE KEY TEST - morning music should start
	musicType, _ = stateManager.GetString("musicPlaybackType")
	assert.Equal(t, "morning", musicType,
		"Phase 3: MORNING music should start after wake-up event during morning phase. "+
			"This is the core bug - Go currently returns 'day' instead of 'morning'.")

	// ==========================================================
	// PHASE 4: Day phase change
	// ==========================================================
	timeProvider.SetTime(time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC))
	mockClient.ClearServiceCalls()

	_ = stateManager.SetString("dayPhase", "day")

	time.Sleep(50 * time.Millisecond)

	musicType, _ = stateManager.GetString("musicPlaybackType")
	assert.Equal(t, "day", musicType, "Phase 4: Day music should play during day phase")
}

// =============================================================================
// HELPER: Mutable Time Provider for multi-phase tests
// =============================================================================

type MutableTimeProvider struct {
	mu          sync.RWMutex
	currentTime time.Time
}

func (p *MutableTimeProvider) Now() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.currentTime
}

func (p *MutableTimeProvider) SetTime(t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentTime = t
}
