package ha

import (
	"errors"
	"testing"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeInternalClient implements only SubscribeMQTT. Other methods panic.
type fakeInternalClient struct {
	ha.HAClient
	topic   string
	handler ha.MQTTHandler
	err     error
}

func (f *fakeInternalClient) SubscribeMQTT(topic string, handler ha.MQTTHandler) (ha.Subscription, error) {
	f.topic = topic
	f.handler = handler
	if f.err != nil {
		return nil, f.err
	}
	return fakeSubscription{}, nil
}

type fakeSubscription struct{}

func (fakeSubscription) Unsubscribe() error { return nil }

func TestClientAdapter_SubscribeMQTT_ForwardsMessages(t *testing.T) {
	inner := &fakeInternalClient{}
	client := WrapClient(inner)
	subscriber, ok := client.(MQTTSubscriber)
	require.True(t, ok)

	var got []MQTTMessage
	sub, err := subscriber.SubscribeMQTT("sensors/#", func(msg MQTTMessage) { got = append(got, msg) })
	require.NoError(t, err)
	require.NotNil(t, sub)
	assert.Equal(t, "sensors/#", inner.topic)

	inner.handler(ha.MQTTMessage{Topic: "sensors/porch", Payload: "on", QoS: 1, Retain: true})
	inner.handler(ha.MQTTMessage{Topic: "sensors/#", Resubscribed: true})
	assert.Equal(t, []MQTTMessage{
		{Topic: "sensors/porch", Payload: "on", QoS: 1, Retain: true},
		{Topic: "sensors/#", Resubscribed: true},
	}, got)
}

func TestClientAdapter_SubscribeMQTT_ReturnsError(t *testing.T) {
	inner := &fakeInternalClient{err: errors.New("not connected")}
	_, err := WrapClient(inner).(MQTTSubscriber).SubscribeMQTT("sensors/#", func(MQTTMessage) {})
	require.EqualError(t, err, "not connected")
}
