package lighting

import (
	"context"
	"testing"
	"time"

	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
)

// TestLightingReset_TriggerClearsBooleanAndReappliesScenes verifies the false → true edge on
// lightingReset clears the boolean (loop prevention) and re-applies scenes.
func TestLightingReset_TriggerClearsBooleanAndReappliesScenes(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	env.StateMgr.SetString("dayPhase", "morning")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, createTestConfig(), env.Logger, false, nil)
	assert.NoError(t, manager.Start())
	defer manager.Stop()

	before := len(env.MockHA.GetServiceCalls())
	assert.NoError(t, env.StateMgr.SetBool("lightingReset", true))
	manager.handleLightingResetChange("lightingReset", false, true)

	val, err := env.StateMgr.GetBool("lightingReset")
	assert.NoError(t, err)
	assert.False(t, val, "lightingReset must be cleared after the handler runs")

	assert.Eventually(t, func() bool {
		return len(env.MockHA.GetServiceCalls()) > before
	}, 3*time.Second, 20*time.Millisecond, "reset should re-apply scenes")
}

// TestLightingReset_IgnoresFalseAndNonBool verifies that false and non-bool values do nothing.
func TestLightingReset_IgnoresFalseAndNonBool(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	env.StateMgr.SetString("dayPhase", "morning")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, createTestConfig(), env.Logger, false, nil)
	assert.NoError(t, manager.Start())
	defer manager.Stop()

	before := len(env.MockHA.GetServiceCalls())
	manager.handleLightingResetChange("lightingReset", true, false)
	manager.handleLightingResetChange("lightingReset", false, "on")

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, before, len(env.MockHA.GetServiceCalls()), "no scenes should be applied")
}

// TestLightingReset_ReadOnlyDoesNotClearBoolean verifies read-only mode writes nothing back.
func TestLightingReset_ReadOnlyDoesNotClearBoolean(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	env.StateMgr.SetString("dayPhase", "morning")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, createTestConfig(), env.Logger, true, nil)
	assert.NoError(t, manager.Start())
	defer manager.Stop()

	assert.NoError(t, env.StateMgr.SetBool("lightingReset", true))
	manager.handleLightingResetChange("lightingReset", false, true)

	val, err := env.StateMgr.GetBool("lightingReset")
	assert.NoError(t, err)
	assert.True(t, val, "read-only mode must not write the boolean")
}
