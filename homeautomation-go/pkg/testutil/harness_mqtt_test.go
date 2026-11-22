package testutil

import (
	"sync"
	"testing"
	"time"

	pkgha "homeautomation/pkg/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScenario_ExternalPlugin_ReceivesMQTTThroughPublicClient covers a plugin
// built outside this module. It sees only the public pkg/ha client, as an
// external plugin does through plugin.Context. It must still be able to read
// MQTT topics that Home Assistant forwards.
func TestScenario_ExternalPlugin_ReceivesMQTTThroughPublicClient(t *testing.T) {
	t.Log("GIVEN: an external plugin with only the public Home Assistant client")
	env, err := NewTestEnv("localhost:18391", "test_token")
	require.NoError(t, err)
	defer env.Cleanup()

	subscriber, ok := env.HAClient.(pkgha.MQTTSubscriber)
	require.True(t, ok, "the public client must offer MQTT subscriptions")

	var mu sync.Mutex
	var got []pkgha.MQTTMessage
	sub, err := subscriber.SubscribeMQTT("sensors/+/state", func(msg pkgha.MQTTMessage) {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return env.Server.MQTTSubscriptionCount() == 1 }, 5*time.Second, 10*time.Millisecond)

	t.Log("WHEN: Home Assistant forwards a message on a matching topic")
	require.Equal(t, 1, env.Server.PublishMQTT("sensors/porch/state", `{"value":1}`))

	t.Log("THEN: the plugin receives the topic and payload")
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	}, 5*time.Second, 10*time.Millisecond)
	mu.Lock()
	assert.Equal(t, "sensors/porch/state", got[0].Topic)
	assert.Equal(t, `{"value":1}`, got[0].Payload)
	assert.False(t, got[0].Resubscribed)
	mu.Unlock()

	t.Log("WHEN: the plugin unsubscribes")
	require.NoError(t, sub.Unsubscribe())
	env.Server.PublishMQTT("sensors/porch/state", `{"value":2}`)

	t.Log("THEN: it receives nothing more")
	time.Sleep(100 * time.Millisecond) // Delivery is asynchronous; give a stray message time to arrive.
	mu.Lock()
	assert.Len(t, got, 1)
	mu.Unlock()
}
