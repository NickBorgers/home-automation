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

func (f *displayOffBravia) counts() (writes, failedSets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes), f.failedSets
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
