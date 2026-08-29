package tesla

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/account"
)

// ErrReadOnly reports that the process is running with READ_ONLY=true, so a
// command was logged instead of sent. Callers compare with errors.Is to tell
// a refusal apart from a real failure.
var ErrReadOnly = errors.New("read-only mode: command not sent")

// EnergySite is one Powerwall system on the account. A single property can
// have several, which the Tesla app presents as separate homes.
type EnergySite struct {
	ID           int64  `json:"energy_site_id"`
	Name         string `json:"site_name"`
	ResourceType string `json:"resource_type"`
}

// Battery reports whether this product is a Powerwall system rather than a
// vehicle or a solar-only site.
func (s EnergySite) Battery() bool { return s.ResourceType == "battery" }

// EnergySites lists the Powerwall systems on the account.
//
// This is one billed request for the whole account. It exists so an operator
// can look up a site id, not so anything can poll it: Powerwall telemetry
// comes from the gateways on the LAN, which is free.
func (c *Client) EnergySites(ctx context.Context) ([]EnergySite, error) {
	body, err := c.get(ctx, "api/1/products")
	if err != nil {
		return nil, err
	}

	return parseEnergySites(body)
}

// parseEnergySites pulls the Powerwall systems out of a product list. Vehicles
// come back on the same endpoint, without a site id.
func parseEnergySites(body []byte) ([]EnergySite, error) {
	var parsed struct {
		Response []EnergySite `json:"response"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse product list: %w", err)
	}

	sites := make([]EnergySite, 0, len(parsed.Response))
	for _, site := range parsed.Response {
		if site.ID != 0 && site.Battery() {
			sites = append(sites, site)
		}
	}
	return sites, nil
}

// BackupReserve reads the site's configured backup reserve percentage. This
// lives in site_info rather than live_status, so reading it costs its own
// request.
func (c *Client) BackupReserve(ctx context.Context, siteID int64) (int, error) {
	body, err := c.get(ctx, fmt.Sprintf("api/1/energy_sites/%d/site_info", siteID))
	if err != nil {
		return 0, err
	}

	return parseBackupReserve(body)
}

// parseBackupReserve rounds Tesla's fractional percentage to a whole one.
func parseBackupReserve(body []byte) (int, error) {
	var parsed struct {
		Response struct {
			BackupReservePercent float64 `json:"backup_reserve_percent"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("parse site info: %w", err)
	}
	return int(parsed.Response.BackupReservePercent + 0.5), nil
}

// SetBackupReserve sets how much charge the site holds back for an outage —
// the closest thing a Powerwall has to a target charge level.
//
// Unlike vehicle commands this needs no signing and no virtual key: energy
// commands are plain authenticated REST. It does need the energy_cmds scope,
// which is only present on tokens issued after that scope was requested.
func (c *Client) SetBackupReserve(ctx context.Context, siteID int64, percent int) error {
	if percent < 0 || percent > 100 {
		return fmt.Errorf("backup reserve %d%% is outside 0-100%%", percent)
	}

	payload, err := json.Marshal(map[string]int{"backup_reserve_percent": percent})
	if err != nil {
		return fmt.Errorf("encode backup reserve: %w", err)
	}

	body, err := c.post(ctx, fmt.Sprintf("api/1/energy_sites/%d/backup", siteID), payload)
	if err != nil {
		return err
	}

	return parseCommandResult(body)
}

// parseCommandResult reads Tesla's command envelope. Tesla answers 200 even
// when it declines the change, so the body decides whether this worked — but a
// bare {"response":{"result":true}} and an empty envelope both mean success,
// so only an explicit reason counts as failure.
func parseCommandResult(body []byte) error {
	var parsed struct {
		Response struct {
			Result bool   `json:"result"`
			Reason string `json:"reason"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("parse backup reserve response: %w", err)
	}
	if !parsed.Response.Result && parsed.Response.Reason != "" {
		return fmt.Errorf("tesla declined the backup reserve change: %s", parsed.Response.Reason)
	}
	return nil
}

// post issues one authenticated Fleet API POST, retrying only a request that
// never reached Tesla. Every command this package sends is idempotent — it
// states a target rather than nudging one — so repeating a dropped request
// cannot double-apply.
func (c *Client) post(ctx context.Context, endpoint string, payload []byte) ([]byte, error) {
	token, err := c.auth.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	acct, err := account.New(token, UserAgent)
	if err != nil {
		return nil, fmt.Errorf("build fleet api account: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay):
			}
		}

		body, err := acct.Post(ctx, endpoint, payload)
		if err == nil {
			c.requests.Add(1)
			return body, nil
		}
		lastErr = err
		if !transport(ctx, err) {
			c.requests.Add(1)
			break
		}
	}
	return nil, fmt.Errorf("fleet api POST %s: %w", endpoint, lastErr)
}
