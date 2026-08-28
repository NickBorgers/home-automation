package tesla

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setMinimalEnv sets the variables every valid configuration needs.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TESLA_CLIENT_ID", "client-id")
	t.Setenv("TESLA_CLIENT_SECRET", "client-secret")
	t.Setenv("TESLA_DOMAIN", "tesla.example.ts.net")
	t.Setenv("TESLA_REDIRECT_URI", "https://home-automation.example.ts.net/api/tesla/callback")
}

func TestConfigFromEnvDisabledWithoutClientID(t *testing.T) {
	t.Setenv("TESLA_CLIENT_ID", "")

	cfg, enabled, err := ConfigFromEnv()

	require.NoError(t, err)
	assert.False(t, enabled, "no client ID means the plugin stays idle")
	assert.Equal(t, Config{}, cfg)
}

func TestConfigFromEnvAppliesDefaults(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("TESLA_PRIVATE_KEY", "")
	t.Setenv("TESLA_TOKEN_STORE", "")
	t.Setenv("TESLA_AUTH_BASE", "")
	t.Setenv("TESLA_AUDIENCE", "")
	t.Setenv("TESLA_POLL_MINUTES", "")

	cfg, enabled, err := ConfigFromEnv()

	require.NoError(t, err)
	require.True(t, enabled)
	assert.Equal(t, DefaultAuthBase, cfg.AuthBase)
	assert.Equal(t, DefaultAudience, cfg.Audience)
	assert.Equal(t, "/app/tesla/private-key.pem", cfg.PrivateKeyPath)
	assert.Equal(t, "/app/state/tesla-tokens.json", cfg.TokenStorePath)
	assert.Equal(t, DefaultPollInterval, cfg.PollInterval)
}

func TestConfigFromEnvOverrides(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("TESLA_PRIVATE_KEY", "/keys/private.pem")
	t.Setenv("TESLA_TOKEN_STORE", "/state/tokens.json")
	t.Setenv("TESLA_VIN", "5YJ3E1EA1JF000000")
	t.Setenv("TESLA_POLL_MINUTES", "15")

	cfg, enabled, err := ConfigFromEnv()

	require.NoError(t, err)
	require.True(t, enabled)
	assert.Equal(t, "/keys/private.pem", cfg.PrivateKeyPath)
	assert.Equal(t, "/state/tokens.json", cfg.TokenStorePath)
	assert.Equal(t, "5YJ3E1EA1JF000000", cfg.VIN)
	assert.Equal(t, 15*time.Minute, cfg.PollInterval)
}

func TestConfigFromEnvRejectsBadPollInterval(t *testing.T) {
	setMinimalEnv(t)

	for _, raw := range []string{"0", "-5", "soon"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("TESLA_POLL_MINUTES", raw)

			_, enabled, err := ConfigFromEnv()

			require.Error(t, err)
			assert.False(t, enabled)
			assert.Contains(t, err.Error(), "TESLA_POLL_MINUTES")
		})
	}
}

func TestConfigFromEnvReportsMissingFields(t *testing.T) {
	t.Setenv("TESLA_CLIENT_ID", "client-id")
	t.Setenv("TESLA_CLIENT_SECRET", "")
	t.Setenv("TESLA_DOMAIN", "")
	t.Setenv("TESLA_REDIRECT_URI", "")

	_, enabled, err := ConfigFromEnv()

	require.Error(t, err)
	assert.False(t, enabled)
	assert.Contains(t, err.Error(), "TESLA_CLIENT_SECRET")
	assert.Contains(t, err.Error(), "TESLA_DOMAIN")
	assert.Contains(t, err.Error(), "TESLA_REDIRECT_URI")
}
