// Package tesla provides access to the Tesla Fleet API: the third-party OAuth
// flow, on-disk token storage, vehicle data reads, and signed vehicle commands.
//
// Only one part of this integration is reachable from the internet: the EC
// public key that Tesla requires to be hosted at
// /.well-known/appspecific/com.tesla.3p.public-key.pem on the registered
// domain. That file is published by a separate container; see the
// host-config-as-code repository. Everything here is outbound HTTPS.
package tesla

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Scopes requested during the OAuth flow. These must also be selected on the
// application in the Tesla developer portal, or authorization fails.
const Scopes = "openid offline_access vehicle_device_data vehicle_location " +
	"vehicle_cmds vehicle_charging_cmds energy_device_data energy_cmds"

// Defaults for the North America / Asia-Pacific region.
const (
	DefaultAuthBase = "https://auth.tesla.com"
	DefaultAudience = "https://fleet-api.prd.na.vn.cloud.tesla.com"

	// DefaultPollInterval is deliberately slow. Tesla bills per request against
	// a small monthly credit, so polling is a cost, not a free read.
	DefaultPollInterval = 5 * time.Minute

	// UserAgent identifies this client to Tesla.
	UserAgent = "nickborgers-home-automation/1.0"
)

// Config holds everything the Fleet API integration needs. Secrets come from
// the environment; nothing here is read from the repository.
type Config struct {
	ClientID     string
	ClientSecret string

	// Domain is the partner domain registered with Tesla. It is the domain that
	// serves the public key, and it appears in the virtual key pairing link.
	Domain string

	// RedirectURI must match the redirect URI registered with Tesla exactly.
	// It is only ever loaded by a browser, so a tailnet URL is fine.
	RedirectURI string

	// PrivateKeyPath is the EC private key that signs vehicle commands.
	PrivateKeyPath string

	// TokenStorePath is where the OAuth tokens live. Tesla rotates the refresh
	// token on every refresh, so this file must be writable and must survive
	// restarts.
	TokenStorePath string

	// VIN of the vehicle to manage.
	VIN string

	AuthBase     string
	Audience     string
	PollInterval time.Duration
}

// ConfigFromEnv builds a Config from environment variables. It returns
// enabled=false when TESLA_CLIENT_ID is unset, which is the normal state for
// a deployment that has not gone through the Tesla setup.
func ConfigFromEnv() (cfg Config, enabled bool, err error) {
	cfg = Config{
		ClientID:       strings.TrimSpace(os.Getenv("TESLA_CLIENT_ID")),
		ClientSecret:   strings.TrimSpace(os.Getenv("TESLA_CLIENT_SECRET")),
		Domain:         strings.TrimSpace(os.Getenv("TESLA_DOMAIN")),
		RedirectURI:    strings.TrimSpace(os.Getenv("TESLA_REDIRECT_URI")),
		PrivateKeyPath: strings.TrimSpace(os.Getenv("TESLA_PRIVATE_KEY")),
		TokenStorePath: strings.TrimSpace(os.Getenv("TESLA_TOKEN_STORE")),
		VIN:            strings.TrimSpace(os.Getenv("TESLA_VIN")),
		AuthBase:       strings.TrimSpace(os.Getenv("TESLA_AUTH_BASE")),
		Audience:       strings.TrimSpace(os.Getenv("TESLA_AUDIENCE")),
		PollInterval:   DefaultPollInterval,
	}

	if cfg.ClientID == "" {
		return Config{}, false, nil
	}

	if cfg.AuthBase == "" {
		cfg.AuthBase = DefaultAuthBase
	}
	if cfg.Audience == "" {
		cfg.Audience = DefaultAudience
	}
	if cfg.PrivateKeyPath == "" {
		cfg.PrivateKeyPath = "/app/tesla/private-key.pem"
	}
	if cfg.TokenStorePath == "" {
		cfg.TokenStorePath = "/app/state/tesla-tokens.json"
	}
	if raw := strings.TrimSpace(os.Getenv("TESLA_POLL_MINUTES")); raw != "" {
		minutes, convErr := strconv.Atoi(raw)
		if convErr != nil || minutes < 1 {
			return Config{}, false, fmt.Errorf("TESLA_POLL_MINUTES must be a positive integer, got %q", raw)
		}
		cfg.PollInterval = time.Duration(minutes) * time.Minute
	}

	if err := cfg.validate(); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

func (c Config) validate() error {
	missing := make([]string, 0, 4)
	if c.ClientSecret == "" {
		missing = append(missing, "TESLA_CLIENT_SECRET")
	}
	if c.Domain == "" {
		missing = append(missing, "TESLA_DOMAIN")
	}
	if c.RedirectURI == "" {
		missing = append(missing, "TESLA_REDIRECT_URI")
	}
	if len(missing) > 0 {
		return fmt.Errorf("tesla config incomplete: %s not set", strings.Join(missing, ", "))
	}
	return nil
}
