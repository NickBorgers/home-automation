package tesla

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/account"
)

// VehicleSummary is one entry from the vehicle list. State is "online",
// "asleep", or "offline".
type VehicleSummary struct {
	ID          int64  `json:"id"`
	VIN         string `json:"vin"`
	DisplayName string `json:"display_name"`
	State       string `json:"state"`
}

// Online reports whether the car is awake and will answer a data request.
func (v VehicleSummary) Online() bool { return v.State == "online" }

// ChargeState is the subset of Tesla's charge_state that the automations use.
type ChargeState struct {
	BatteryLevel        int     `json:"battery_level"`
	ChargeLimitSOC      int     `json:"charge_limit_soc"`
	ChargingState       string  `json:"charging_state"`
	ChargePortDoorOpen  bool    `json:"charge_port_door_open"`
	MinutesToFullCharge int     `json:"minutes_to_full_charge"`
	ChargerPower        float64 `json:"charger_power"`
	BatteryRange        float64 `json:"battery_range"`
}

// Plugged reports whether a cable is connected. Tesla reports "Disconnected"
// when nothing is plugged in.
func (c ChargeState) Plugged() bool {
	return c.ChargingState != "" && c.ChargingState != "Disconnected"
}

// ClimateState is the subset of Tesla's climate_state that is useful here.
type ClimateState struct {
	InsideTemp  float64 `json:"inside_temp"`
	OutsideTemp float64 `json:"outside_temp"`
	IsClimateOn bool    `json:"is_climate_on"`
}

// VehicleState is the subset of Tesla's vehicle_state that is useful here.
type VehicleState struct {
	Locked   bool    `json:"locked"`
	Odometer float64 `json:"odometer"`
}

// VehicleData is one vehicle_data response.
type VehicleData struct {
	VIN          string       `json:"vin"`
	State        string       `json:"state"`
	ChargeState  ChargeState  `json:"charge_state"`
	ClimateState ClimateState `json:"climate_state"`
	VehicleState VehicleState `json:"vehicle_state"`
}

// Client reads vehicle data from the Fleet API.
//
// Every call is billed by Tesla, so the client counts requests. Callers should
// check the state from Vehicles (one cheap request) and only ask for
// VehicleData when the car is online.
type Client struct {
	auth     *Authenticator
	requests atomic.Int64
}

// NewClient returns a Fleet API client that authenticates through auth.
func NewClient(auth *Authenticator) *Client {
	return &Client{auth: auth}
}

// RequestCount returns how many Fleet API requests this client has made.
func (c *Client) RequestCount() int64 { return c.requests.Load() }

// Vehicles lists the vehicles on the account, including their sleep state.
func (c *Client) Vehicles(ctx context.Context) ([]VehicleSummary, error) {
	body, err := c.get(ctx, "api/1/vehicles")
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Response []VehicleSummary `json:"response"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse vehicle list: %w", err)
	}
	return parsed.Response, nil
}

// VehicleData fetches live data for one vehicle. It fails when the car is
// asleep; Tesla answers 408 rather than waking it, which is the behaviour we
// want — waking a car is a separate, billable action.
func (c *Client) VehicleData(ctx context.Context, vin string) (*VehicleData, error) {
	endpoint := fmt.Sprintf(
		"api/1/vehicles/%s/vehicle_data?endpoints=charge_state%%3Bclimate_state%%3Bvehicle_state",
		vin,
	)
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Response VehicleData `json:"response"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse vehicle data: %w", err)
	}
	return &parsed.Response, nil
}

// get issues one authenticated Fleet API GET. The account package picks the
// regional Fleet API host out of the access token, so no base URL is hardcoded
// on this path.
//
// A dropped connection gets one more attempt. An answer from Tesla does not,
// even a 500: Tesla has already billed that request, and paying twice for the
// same read is worse than waiting for the next poll.
func (c *Client) get(ctx context.Context, endpoint string) ([]byte, error) {
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

		body, err := acct.Get(ctx, endpoint)
		if err == nil {
			c.requests.Add(1)
			return body, nil
		}
		lastErr = err
		if !transport(ctx, err) {
			// Tesla answered, just not with a 200. That request was billed.
			c.requests.Add(1)
			break
		}
	}
	return nil, fmt.Errorf("fleet api GET %s: %w", endpoint, lastErr)
}
