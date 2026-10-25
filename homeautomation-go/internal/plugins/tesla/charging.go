package tesla

// Energy-level charging makes the car's charging follow currentEnergyLevel
// while it is plugged in at home: stop at red and yellow, charge slowly at
// green, and charge at full speed at white. Below a floor the car always
// charges, so it is never left without a usable range.
//
// A charge limit of 100% is the override. Setting it in the Tesla app says "I
// need a full car", so the car charges at full speed whatever the energy level.
//
// It is the one place this plugin sends commands on its own. Every command is
// billed, so commands go out only when the car's state differs from what the
// energy level asks for.

import (
	"context"
	"fmt"
	"math"
	"time"

	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

const (
	// energyChargingFloorPercent is the charge the car always gets, whatever
	// the energy level says.
	energyChargingFloorPercent = 50

	// overrideLimitPercent is the charge limit that overrides the energy level.
	overrideLimitPercent = 100

	// greenChargeAmps is the slow charging speed used at green.
	greenChargeAmps = 8

	// maxWakesPer24h caps how often a sleeping car is woken to resume
	// charging. Tesla bills wakes, and a car that keeps refusing to charge
	// would otherwise be woken on every poll.
	maxWakesPer24h = 3

	// homeRadiusMeters is how close to the house the car must be to count as
	// home. It covers GPS drift across a driveway, not the neighbor's house.
	homeRadiusMeters = 250

	// wakeSettleDelay is how long to wait after a wake before polling again. A
	// car takes several seconds to come online after a wake.
	wakeSettleDelay = 45 * time.Second
)

// chargeCommander is the part of the vehicle Commander that energy-level
// charging uses. It exists so tests can record commands instead of sending
// them.
type chargeCommander interface {
	Wake(ctx context.Context, vin string) error
	ChargeStart(ctx context.Context, vin string) error
	ChargeStop(ctx context.Context, vin string) error
	SetChargingAmps(ctx context.Context, vin string, amps int32) error
}

// levelReader reads state variables. The state manager satisfies it.
type levelReader interface {
	GetString(key string) (string, error)
}

type chargeAction int

const (
	leaveAlone chargeAction = iota
	chargeNow
	holdCharge
)

// chargePlan is what the energy level asks of the car. amps is zero when the
// charging speed should stay as it is.
type chargePlan struct {
	action chargeAction
	amps   int
	reason string
}

// planCharging maps the energy level, battery charge, and charge limit to a
// plan. maxAmps is the most the charger allows. It is zero when unknown, which
// leaves the speed alone.
func planCharging(level string, battery, limit, maxAmps int) chargePlan {
	if limit >= overrideLimitPercent {
		return chargePlan{
			action: chargeNow,
			amps:   maxAmps,
			reason: "charge limit is 100%: charge at full speed, energy level ignored",
		}
	}
	if battery < energyChargingFloorPercent {
		return chargePlan{
			action: chargeNow,
			amps:   maxAmps,
			reason: fmt.Sprintf("battery %d%% is below the %d%% floor: charge at full speed",
				battery, energyChargingFloorPercent),
		}
	}
	switch level {
	case "white":
		return chargePlan{action: chargeNow, amps: maxAmps, reason: "energy level white: charge at full speed"}
	case "green":
		amps := greenChargeAmps
		if maxAmps > 0 && maxAmps < amps {
			amps = maxAmps
		}
		return chargePlan{action: chargeNow, amps: amps, reason: "energy level green: charge slowly"}
	case "yellow", "red":
		return chargePlan{action: holdCharge, reason: "energy level " + level + ": hold charging"}
	default:
		return chargePlan{action: leaveAlone, reason: fmt.Sprintf("energy level %q is unknown: leave charging alone", level)}
	}
}

// chargeMemory is what energy-level charging remembers between polls. A
// sleeping car answers no data requests, so the wake decision runs on what the
// last online poll saw.
type chargeMemory struct {
	seen    bool
	atHome  bool
	plugged bool
	battery int
	limit   int

	wakes  []time.Time
	record shadowstate.TeslaEnergyCharging
}

// distanceMeters returns the great-circle distance between two coordinates.
func distanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371000.0
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadius * math.Asin(math.Sqrt(a))
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// energyLevel returns currentEnergyLevel, or "" when it cannot be read.
func (m *Manager) energyLevel() string {
	level, err := m.levels.GetString("currentEnergyLevel")
	if err != nil {
		return ""
	}
	return level
}

// handleEnergyLevelChange re-polls when the energy level changes while the car
// was last seen plugged in at home. The poll applies the new plan, or wakes the
// car when it is asleep and should now charge.
func (m *Manager) handleEnergyLevelChange(_ string, _, newValue interface{}) {
	go func() {
		m.chargeMu.Lock()
		relevant := m.charge.seen && m.charge.atHome && m.charge.plugged
		m.chargeMu.Unlock()
		if !relevant {
			return
		}
		m.logger.Info("Energy level changed with the car plugged in at home, re-checking charging",
			zap.Any("energyLevel", newValue))
		m.Poll()
	}()
}

// controlCharging compares a fresh vehicle_data read with the energy level and
// sends whatever commands close the gap.
func (m *Manager) controlCharging(ctx context.Context, vin string, atHome, hasLocation bool, cs chargeSnapshot) {
	if m.charger == nil {
		return
	}
	level := m.energyLevel()

	m.chargeMu.Lock()
	defer m.chargeMu.Unlock()
	defer m.publishCharging()

	mem := &m.charge
	mem.seen = true
	mem.atHome = atHome
	mem.plugged = cs.plugged
	mem.battery = cs.battery
	mem.limit = cs.limit
	mem.record.EnergyLevel = level

	switch {
	case !hasLocation:
		mem.record.Decision = "location unavailable (the token may lack vehicle_location): leave charging alone"
		return
	case !atHome:
		mem.record.Decision = "away from home: leave charging alone"
	case !cs.plugged:
		mem.record.Decision = "not plugged in"
	case cs.chargingState == "Complete":
		mem.record.Decision = "charge limit reached"
	}
	if !atHome || !cs.plugged || cs.chargingState == "Complete" {
		return
	}

	plan := planCharging(level, cs.battery, cs.limit, cs.maxAmps)
	charging := cs.chargingState == "Charging" || cs.chargingState == "Starting"

	mem.record.Decision = plan.reason
	switch plan.action {
	case holdCharge:
		if charging {
			m.sendChargeCommand(ctx, "stop charging", func(ctx context.Context) error {
				return m.charger.ChargeStop(ctx, vin)
			})
		}
	case chargeNow:
		if plan.amps > 0 && cs.amps != plan.amps {
			amps := plan.amps
			m.sendChargeCommand(ctx, fmt.Sprintf("set charging speed to %dA", amps), func(ctx context.Context) error {
				return m.charger.SetChargingAmps(ctx, vin, int32(amps))
			})
		}
		if cs.chargingState == "Stopped" {
			m.sendChargeCommand(ctx, "start charging", func(ctx context.Context) error {
				return m.charger.ChargeStart(ctx, vin)
			})
		}
	}
}

// chargeSnapshot is the slice of one vehicle_data read that charging control
// needs.
type chargeSnapshot struct {
	plugged       bool
	chargingState string
	battery       int
	limit         int
	amps          int
	maxAmps       int
}

// maybeWakeToCharge wakes a sleeping car when the last online poll saw it
// plugged in at home, below its limit, and the energy level now says charge.
// Wakes are billed and capped per day.
func (m *Manager) maybeWakeToCharge(ctx context.Context, vin, vehicleState string) {
	if m.charger == nil || vehicleState != "asleep" {
		return
	}
	level := m.energyLevel()

	m.chargeMu.Lock()
	defer m.chargeMu.Unlock()

	mem := &m.charge
	if !mem.seen || !mem.atHome || !mem.plugged || mem.battery >= mem.limit {
		return
	}
	plan := planCharging(level, mem.battery, mem.limit, 0)
	if plan.action != chargeNow {
		return
	}
	defer m.publishCharging()
	mem.record.EnergyLevel = level

	now := m.clock()
	m.pruneWakes(now)
	if len(mem.wakes) >= maxWakesPer24h {
		mem.record.Decision = "car is asleep and should charge, but the daily wake limit is spent"
		return
	}
	mem.wakes = append(mem.wakes, now)
	mem.record.Decision = "car is asleep: waking it to charge (" + plan.reason + ")"

	if !m.sendChargeCommand(ctx, "wake the car to charge", func(ctx context.Context) error {
		return m.charger.Wake(ctx, vin)
	}) {
		return
	}
	m.schedulePollAfter(wakeSettleDelay)
}

// pruneWakes drops wakes older than 24 hours. The caller holds chargeMu.
func (m *Manager) pruneWakes(now time.Time) {
	kept := m.charge.wakes[:0]
	for _, at := range m.charge.wakes {
		if now.Sub(at) < 24*time.Hour {
			kept = append(kept, at)
		}
	}
	m.charge.wakes = kept
}

func (m *Manager) schedulePollAfter(delay time.Duration) {
	if m.schedulePoll != nil {
		m.schedulePoll(delay)
		return
	}
	time.AfterFunc(delay, func() {
		if m.ctx.Err() == nil {
			m.Poll()
		}
	})
}

// sendChargeCommand sends one command and records the outcome. It reports
// whether the command went through. Read-only mode logs the command and
// reports false. The caller holds chargeMu.
func (m *Manager) sendChargeCommand(ctx context.Context, what string, send func(context.Context) error) bool {
	rec := &m.charge.record
	rec.LastActionAt = m.clock()

	if m.readOnly {
		m.logger.Info("Read-only mode: skipping Tesla charging command",
			zap.String("command", what), zap.String("reason", rec.Decision))
		rec.LastAction = "read-only: would " + what
		return false
	}

	err := send(ctx)
	m.recordUsage()
	if err != nil {
		m.logger.Warn("Tesla charging command failed", zap.String("command", what), zap.Error(err))
		rec.LastAction = what + " failed: " + err.Error()
		m.shadowTracker.UpdateLastError(err.Error())
		return false
	}

	m.logger.Info("Tesla charging command sent", zap.String("command", what), zap.String("reason", rec.Decision))
	rec.LastAction = what
	return true
}

// publishCharging copies the charging record into shadow state. The caller
// holds chargeMu.
func (m *Manager) publishCharging() {
	m.pruneWakes(m.clock())
	rec := m.charge.record
	rec.Enabled = true
	rec.WakesLast24h = len(m.charge.wakes)
	m.shadowTracker.UpdateEnergyCharging(rec)
}
