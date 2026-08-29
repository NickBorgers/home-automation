package tesla

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnergySiteBattery(t *testing.T) {
	assert.True(t, EnergySite{ResourceType: "battery"}.Battery())
	assert.False(t, EnergySite{ResourceType: "solar"}.Battery())
	assert.False(t, EnergySite{}.Battery())
}

func TestSetBackupReserveRejectsOutOfRange(t *testing.T) {
	client := NewClient(NewAuthenticator(Config{ClientID: "id", AuthBase: DefaultAuthBase},
		NewTokenStore(t.TempDir()+"/tokens.json")))

	for _, percent := range []int{-1, 101, 1000} {
		err := client.SetBackupReserve(t.Context(), 123, percent)
		assert.ErrorContains(t, err, "outside 0-100")
	}
	assert.Zero(t, client.RequestCount(), "a rejected value must not reach Tesla or the bill")
}

func TestEnergyCallsRequireAuthorization(t *testing.T) {
	client := NewClient(NewAuthenticator(Config{ClientID: "id", AuthBase: DefaultAuthBase},
		NewTokenStore(t.TempDir()+"/tokens.json")))

	_, err := client.EnergySites(t.Context())
	assert.ErrorContains(t, err, "not authorized")

	_, err = client.BackupReserve(t.Context(), 123)
	assert.ErrorContains(t, err, "not authorized")

	assert.ErrorContains(t, client.SetBackupReserve(t.Context(), 123, 40), "not authorized")
	assert.Zero(t, client.RequestCount())
}

func TestParseEnergySitesKeepsOnlyBatteries(t *testing.T) {
	// A real product list: one vehicle (no site id) and two Powerwall systems.
	body := []byte(`{"response":[
		{"vin":"5YJ3E1EA1JF000000","display_name":"car"},
		{"energy_site_id":1234567890123456,"site_name":"Left Powerwall","resource_type":"battery"},
		{"energy_site_id":1234567890123457,"site_name":"Right Powerwall","resource_type":"battery"},
		{"energy_site_id":999,"site_name":"solar only","resource_type":"solar"}
	],"count":4}`)

	sites, err := parseEnergySites(body)

	require.NoError(t, err)
	require.Len(t, sites, 2)
	assert.Equal(t, int64(1234567890123456), sites[0].ID)
	assert.Equal(t, "Left Powerwall", sites[0].Name)
	assert.Equal(t, "Right Powerwall", sites[1].Name)
}

func TestParseEnergySitesRejectsGarbage(t *testing.T) {
	_, err := parseEnergySites([]byte("not json"))
	assert.ErrorContains(t, err, "parse product list")
}

func TestParseBackupReserveRounds(t *testing.T) {
	tests := map[string]int{
		`{"response":{"backup_reserve_percent":20}}`:   20,
		`{"response":{"backup_reserve_percent":20.4}}`: 20,
		`{"response":{"backup_reserve_percent":20.6}}`: 21,
		`{"response":{"backup_reserve_percent":0}}`:    0,
		`{"response":{"backup_reserve_percent":99.5}}`: 100,
	}

	for body, want := range tests {
		got, err := parseBackupReserve([]byte(body))
		require.NoError(t, err)
		assert.Equal(t, want, got, body)
	}
}

func TestParseCommandResult(t *testing.T) {
	assert.NoError(t, parseCommandResult([]byte(`{"response":{"code":201,"message":"","result":true}}`)))
	assert.NoError(t, parseCommandResult([]byte(`{"response":{}}`)), "an empty envelope is not a failure")

	err := parseCommandResult([]byte(`{"response":{"result":false,"reason":"site is offline"}}`))
	assert.ErrorContains(t, err, "site is offline")

	assert.Error(t, parseCommandResult([]byte("not json")))
}
