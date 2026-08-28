package tesla

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/teslamotors/vehicle-command/pkg/account"
	"github.com/teslamotors/vehicle-command/pkg/protocol"
	"github.com/teslamotors/vehicle-command/pkg/vehicle"
)

// Commander sends signed commands to a vehicle.
//
// Cars built since 2021 reject unsigned commands, so every command is signed
// in-process with the EC private key whose public half Tesla fetched from the
// registered domain. The car must also have the virtual key paired, which is a
// one-time step done from the Tesla phone app.
type Commander struct {
	auth     *Authenticator
	keyPath  string
	commands atomic.Int64

	keyMu  sync.Mutex
	keyPEM protocol.ECDHPrivateKey
}

// NewCommander returns a Commander that signs with the key at keyPath.
func NewCommander(auth *Authenticator, keyPath string) *Commander {
	return &Commander{auth: auth, keyPath: keyPath}
}

// CommandCount returns how many commands have been attempted.
func (c *Commander) CommandCount() int64 { return c.commands.Load() }

// privateKey loads the signing key on first use and keeps it. The key does not
// change while the process runs, and re-reading it per command would turn a
// misconfigured path into an intermittent command failure.
func (c *Commander) privateKey() (protocol.ECDHPrivateKey, error) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()

	if c.keyPEM != nil {
		return c.keyPEM, nil
	}
	key, err := protocol.LoadPrivateKey(c.keyPath)
	if err != nil {
		return nil, fmt.Errorf("load tesla private key %s: %w", c.keyPath, err)
	}
	c.keyPEM = key
	return key, nil
}

// Wake wakes a sleeping car. Tesla bills wakes separately from commands, so
// call this only in response to something a person did.
func (c *Commander) Wake(ctx context.Context, vin string) error {
	return c.withVehicle(ctx, vin, false, func(car *vehicle.Vehicle) error {
		return car.Wakeup(ctx)
	})
}

// ChargeStart begins charging.
func (c *Commander) ChargeStart(ctx context.Context, vin string) error {
	return c.withVehicle(ctx, vin, true, func(car *vehicle.Vehicle) error {
		return car.ChargeStart(ctx)
	})
}

// ChargeStop stops charging.
func (c *Commander) ChargeStop(ctx context.Context, vin string) error {
	return c.withVehicle(ctx, vin, true, func(car *vehicle.Vehicle) error {
		return car.ChargeStop(ctx)
	})
}

// SetChargeLimit sets the charge limit as a percentage.
func (c *Commander) SetChargeLimit(ctx context.Context, vin string, percent int32) error {
	if percent < 50 || percent > 100 {
		return fmt.Errorf("charge limit %d%% is outside the supported 50-100%% range", percent)
	}
	return c.withVehicle(ctx, vin, true, func(car *vehicle.Vehicle) error {
		return car.ChangeChargeLimit(ctx, percent)
	})
}

// FlashLights flashes the headlights. It is the cheapest way to prove that
// command signing and virtual key pairing both work.
func (c *Commander) FlashLights(ctx context.Context, vin string) error {
	return c.withVehicle(ctx, vin, true, func(car *vehicle.Vehicle) error {
		return car.FlashLights(ctx)
	})
}

// withVehicle opens a connection to the car, optionally starts a signed
// session, runs fn, and always disconnects.
//
// Wakeup is the one command that does not need a session: it goes through
// Tesla's servers rather than to the car, which is asleep by definition.
func (c *Commander) withVehicle(ctx context.Context, vin string, needSession bool, fn func(*vehicle.Vehicle) error) error {
	if vin == "" {
		return fmt.Errorf("no VIN configured")
	}

	token, err := c.auth.AccessToken(ctx)
	if err != nil {
		return err
	}
	acct, err := account.New(token, UserAgent)
	if err != nil {
		return fmt.Errorf("build fleet api account: %w", err)
	}

	privateKey, err := c.privateKey()
	if err != nil {
		return err
	}

	car, err := acct.GetVehicle(ctx, vin, privateKey, nil)
	if err != nil {
		return fmt.Errorf("open vehicle %s: %w", vin, err)
	}
	if err := car.Connect(ctx); err != nil {
		return fmt.Errorf("connect to vehicle %s: %w", vin, err)
	}
	defer car.Disconnect()

	if needSession {
		if err := car.StartSession(ctx, nil); err != nil {
			return fmt.Errorf("start signed session with %s (is the virtual key paired?): %w", vin, err)
		}
	}

	c.commands.Add(1)
	if err := fn(car); err != nil {
		return fmt.Errorf("tesla command failed: %w", err)
	}
	return nil
}
