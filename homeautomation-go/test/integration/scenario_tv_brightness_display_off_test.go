package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"homeautomation/internal/bravia"
	"homeautomation/internal/plugins/tv"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// displayOffBravia is a fake Bravia REST endpoint. While the display is off it answers a brightness
// write the way the real A80K does: error 40005 with the message "Display Is Turned off".
// Reads still work, as they do on the real TV.
type displayOffBravia struct {
	mu         sync.Mutex
	displayOn  bool
	current    int
	writes     []int
	failedSets int
}

func (f *displayOffBravia) setDisplay(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.displayOn = on
}

func (f *displayOffBravia) setCurrent(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = v
}

func (f *displayOffBravia) counts() (writes, failedSets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes), f.failedSets
}

func (f *displayOffBravia) brightness() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *displayOffBravia) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string                   `json:"method"`
		Params []map[string]interface{} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch req.Method {
	case "getPictureQualitySettings":
		_, _ = w.Write([]byte(`{"result":[[{"target":"brightness","currentValue":"` + strconv.Itoa(f.current) +
			`","isAvailable":true,"candidate":[{"max":50,"min":0,"step":1}]}]],"id":1}`))
	case "setPictureQualitySettings":
		if !f.displayOn {
			f.failedSets++
			_, _ = w.Write([]byte(`{"error":[40005,"Display Is Turned off"],"id":1}`))
			return
		}
		settings, _ := req.Params[0]["settings"].([]interface{})
		item, _ := settings[0].(map[string]interface{})
		v, _ := strconv.Atoi(item["value"].(string))
		f.current = v
		f.writes = append(f.writes, v)
		_, _ = w.Write([]byte(`{"result":[0],"id":1}`))
	default:
		http.Error(w, "unexpected method", http.StatusBadRequest)
	}
}

// TestScenario_UserStory_TVBrightnessWaitsQuietlyWhileTheDisplayIsOff covers a production log flood.
// The Bravia panel entity in Home Assistant said "on" for a day, while the TV display was off. The
// brightness feature wrote to the TV every minute and logged a warning each time: 376 in one day.
//
// Story: "When the TV display is off, the brightness feature waits quietly. When I turn the TV on,
// it sets the brightness."
//
// INVARIANTS:
//   - A display that is off is not an error. Nothing logs a warning.
//   - The write is tried again on the next lux change, so the brightness is set once the display is on.
func TestScenario_UserStory_TVBrightnessWaitsQuietlyWhileTheDisplayIsOff(t *testing.T) {
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	fake := &displayOffBravia{current: 25}
	tvServer := httptest.NewServer(fake)
	defer tvServer.Close()

	t.Log("GIVEN: Home Assistant says the panel is on, but the TV display is off")
	server.SetState(tv.TVRemoteEntity, "on", nil)
	server.SetState(tv.SyncBoxSoftwarePowerEntity, "off", nil)

	core, logs := observer.New(zap.DebugLevel)
	tvMgr := tv.NewManager(context.Background(), client, stateManager, zap.New(core), false, nil)
	tvMgr.SetPictureClient(bravia.NewClient(tvServer.URL, "test-psk"))
	tvMgr.SetBrightnessPowerOnDelay(0)
	require.NoError(t, tvMgr.Start())
	defer tvMgr.Stop()

	tvDecision := func() string {
		b := tvMgr.GetShadowState().Outputs.Brightness
		if b == nil {
			return ""
		}
		return b.TV.Decision
	}

	t.Log("WHEN: The lighting plugin publishes a new brightness target")
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 150))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 100))

	t.Log("THEN: The plugin tries the TV, records that the screen is not ready, and logs no warning")
	require.Eventually(t, func() bool {
		_, failed := fake.counts()
		return failed >= 1 && tvDecision() == "TV screen not ready"
	}, stateWaitTimeout, statePollInterval, "a display that is off should read as 'TV screen not ready'")
	require.Empty(t, logs.FilterLevelExact(zap.WarnLevel).FilterMessageSnippet("brightness").All(),
		"a display that is off must not log a brightness warning")

	t.Log("WHEN: The display turns on and the target changes again")
	fake.setDisplay(true)
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 100))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 80))

	t.Log("THEN: The brightness is written")
	require.Eventually(t, func() bool {
		writes, _ := fake.counts()
		return writes == 1
	}, stateWaitTimeout, statePollInterval, "brightness should be set once the display is on")
}

// TestScenario_UserStory_ZeroDuringDisplayTransitionDoesNotCreateAManualHold covers issue #1438.
//
// Story: "When the TV reports zero while its display is turning off, keep following the room once
// the display is ready again; do not mistake the transition for my manual choice."
//
// INVARIANTS:
//   - A zero read from a display that rejects picture writes is not a confirmed manual change.
//   - A confirmed manual change while the display remains on is held for that on-session.
//   - An unavailable/off state clears both device holds immediately, before recovery.
func TestScenario_UserStory_ZeroDuringDisplayTransitionDoesNotCreateAManualHold(t *testing.T) {
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	fake := &displayOffBravia{displayOn: true, current: 25}
	tvServer := httptest.NewServer(fake)
	defer tvServer.Close()

	t.Log("GIVEN: the on-session is established and the controller has set brightness 50")
	server.SetState(tv.TVRemoteEntity, "on", nil)
	server.SetState(tv.SyncBoxSoftwarePowerEntity, "off", nil)
	mgr := tv.NewManager(context.Background(), client, stateManager, zap.NewNop(), false, nil)
	mgr.SetPictureClient(bravia.NewClient(tvServer.URL, "test-psk"))
	mgr.SetBrightnessPowerOnDelay(0)
	require.NoError(t, mgr.Start())
	defer mgr.Stop()
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 150))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 100))
	require.Eventually(t, func() bool {
		writes, _ := fake.counts()
		return writes == 1
	}, stateWaitTimeout, statePollInterval)

	t.Log("WHEN: T+1 the transitioning display reports zero but rejects a no-op picture write")
	fake.setDisplay(false)
	fake.setCurrent(0)
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 56.8))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 65))
	require.Eventually(t, func() bool {
		_, failed := fake.counts()
		return failed >= 1
	}, stateWaitTimeout, statePollInterval, "the mismatch should be checked against the display")
	brightness := mgr.GetShadowState().Outputs.Brightness
	require.NotNil(t, brightness)
	require.Equal(t, "TV screen not ready", brightness.TV.Decision)
	require.False(t, brightness.TV.Held, "an unready display must not create a manual hold")

	t.Log("WHEN: T+2 the display is ready and the room target is published again")
	fake.setDisplay(true)
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 57))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 66))
	require.Eventually(t, func() bool {
		return fake.brightness() == 38
	}, stateWaitTimeout, statePollInterval, "normal calculated brightness should resume")
	server.SetState(tv.TVRemoteEntity, "standby", nil)
	client.WaitForHandlers()
	server.SetState(tv.TVRemoteEntity, "on", nil)
	client.WaitForHandlers()
	require.Eventually(t, func() bool {
		brightness := mgr.GetShadowState().Outputs.Brightness
		return brightness != nil && brightness.PowerSessionVerified
	}, stateWaitTimeout, statePollInterval, "the observed power cycle should verify the new session")

	t.Log("WHEN: T+3 a real remote change is confirmed while the display remains on")
	fake.setCurrent(40)
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 58))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 67))
	require.Eventually(t, func() bool {
		brightness := mgr.GetShadowState().Outputs.Brightness
		return brightness != nil && brightness.TV.Held
	}, stateWaitTimeout, statePollInterval, "a confirmed manual change must still hold")

	t.Log("WHEN: T+4 the TV entity becomes unavailable")
	server.SetState(tv.TVRemoteEntity, "unavailable", nil)
	client.WaitForHandlers()

	t.Log("THEN: the prior session's TV and sync-box holds are cleared immediately")
	brightness = mgr.GetShadowState().Outputs.Brightness
	require.NotNil(t, brightness)
	require.False(t, brightness.TV.Held)
	require.Empty(t, brightness.TV.HeldReason)
	require.False(t, brightness.SyncBox.Held)
}

// TestScenario_UserStory_StaleOnZeroHasUnknownCause covers issue #1488.
//
// Story: "When Home Assistant has missed the TV's power cycle, an unexplained zero reading must not
// be attributed to me or held indefinitely."
//
// INVARIANTS:
//   - A writable same-value probe proves only that the display is writable, not who caused a mismatch.
//   - A stale initial on state does not verify that remembered brightness belongs to the current panel session.
//   - A real setting change after an observed off-to-on edge is held for that verified session.
func TestScenario_UserStory_StaleOnZeroHasUnknownCause(t *testing.T) {
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	fake := &displayOffBravia{displayOn: true, current: 25}
	tvServer := httptest.NewServer(fake)
	defer tvServer.Close()

	t.Log("GIVEN: Home Assistant starts with a stale on state and the controller sets brightness 50")
	server.SetState(tv.TVRemoteEntity, "on", nil)
	server.SetState(tv.SyncBoxSoftwarePowerEntity, "off", nil)
	mgr := tv.NewManager(context.Background(), client, stateManager, zap.NewNop(), false, nil)
	mgr.SetPictureClient(bravia.NewClient(tvServer.URL, "test-psk"))
	mgr.SetBrightnessPowerOnDelay(0)
	require.NoError(t, mgr.Start())
	defer mgr.Stop()
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 150))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 100))
	require.Eventually(t, func() bool { return fake.brightness() == 50 }, stateWaitTimeout, statePollInterval)

	t.Log("WHEN: T+1 Bravia unexpectedly reports zero but accepts the same-value probe")
	fake.setCurrent(0)
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 149))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 99))
	require.Eventually(t, func() bool {
		brightness := mgr.GetShadowState().Outputs.Brightness
		return brightness != nil && brightness.TV.HoldEvidence == "brightness mismatch; cause unknown; power session unverified"
	}, stateWaitTimeout, statePollInterval)

	t.Log("THEN: the unknown mismatch is corrected, not labeled or held as a human change")
	brightness := mgr.GetShadowState().Outputs.Brightness
	require.False(t, brightness.PowerSessionVerified)
	require.False(t, brightness.TV.Held)
	require.Equal(t, "set 50", brightness.TV.Decision)
	require.Equal(t, 50, fake.brightness())

	t.Log("WHEN: T+2 an observed standby-to-on edge starts a verified session, then the setting changes")
	server.SetState(tv.TVRemoteEntity, "standby", nil)
	client.WaitForHandlers()
	server.SetState(tv.TVRemoteEntity, "on", nil)
	client.WaitForHandlers()
	require.Eventually(t, func() bool {
		brightness := mgr.GetShadowState().Outputs.Brightness
		return brightness != nil && brightness.PowerSessionVerified
	}, stateWaitTimeout, statePollInterval)
	fake.setCurrent(40)
	require.NoError(t, stateManager.SetNumber("tvRoomTargetLux", 148))
	require.NoError(t, stateManager.SetNumber("tvRoomBrightnessPct", 98))

	t.Log("THEN: a mismatch in the verified on-session is still held")
	require.Eventually(t, func() bool {
		brightness := mgr.GetShadowState().Outputs.Brightness
		return brightness != nil && brightness.TV.Held
	}, stateWaitTimeout, statePollInterval)
}
