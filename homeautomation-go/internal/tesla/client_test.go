package tesla

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVehicleSummaryOnline(t *testing.T) {
	assert.True(t, VehicleSummary{State: "online"}.Online())
	assert.False(t, VehicleSummary{State: "asleep"}.Online())
	assert.False(t, VehicleSummary{State: "offline"}.Online())
	assert.False(t, VehicleSummary{}.Online())
}

func TestChargeStatePlugged(t *testing.T) {
	tests := []struct {
		chargingState string
		want          bool
	}{
		{"Charging", true},
		{"Complete", true},
		{"Stopped", true},
		{"Disconnected", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.chargingState, func(t *testing.T) {
			assert.Equal(t, tc.want, ChargeState{ChargingState: tc.chargingState}.Plugged())
		})
	}
}

func TestClientRequiresAuthorization(t *testing.T) {
	store := NewTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	auth := NewAuthenticator(Config{ClientID: "id", AuthBase: DefaultAuthBase}, store)
	client := NewClient(auth)

	_, err := client.Vehicles(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not authorized")
	assert.Zero(t, client.RequestCount(), "a call that never reached Tesla must not be counted as billable")
}

func TestCommanderRejectsEmptyVIN(t *testing.T) {
	store := NewTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	auth := NewAuthenticator(Config{ClientID: "id", AuthBase: DefaultAuthBase}, store)
	commander := NewCommander(auth, "/nonexistent/private-key.pem")

	err := commander.ChargeStart(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no VIN configured")
}

func TestCommanderRejectsOutOfRangeChargeLimit(t *testing.T) {
	store := NewTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	auth := NewAuthenticator(Config{ClientID: "id", AuthBase: DefaultAuthBase}, store)
	commander := NewCommander(auth, "/nonexistent/private-key.pem")

	for _, percent := range []int32{0, 49, 101} {
		err := commander.SetChargeLimit(context.Background(), "5YJ3E1EA1JF000000", percent)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "charge limit")
	}
	assert.Zero(t, commander.CommandCount())
}
