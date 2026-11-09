// Package roompresence decides which areas of the house have people in them, from Frigate person events.
//
// It keeps anonymous person tokens on a map of areas and moves them as Frigate tracks people through
// zones. See docs/architecture/ROOM_PRESENCE.md for the rules. The map of a real house is private
// deployment data. This package holds only the mechanism.
package roompresence

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigFileName is the config file the plugin reads from the config directory.
const ConfigFileName = "room_presence_config.yaml"

// Modes.
const (
	ModeLogOnly = "log_only"
	ModeActive  = "active"
)

// ignoreZone is the zone_overrides value that tells the plugin to skip a zone.
const ignoreZone = "ignore"

// Defaults.
const (
	defaultHandoffWindow          = 5 * time.Second
	defaultCameraHeartbeatTimeout = 30 * time.Second
	defaultNobodyHomeDelay        = 5 * time.Minute
	defaultExitWindow             = 30 * time.Second
)

// fileConfig is the YAML file.
type fileConfig struct {
	RoomPresence *Config `yaml:"room_presence"`
}

// Config is the room presence configuration.
type Config struct {
	// Mode is log_only (compute and report only) or active (also write outputs).
	Mode string `yaml:"mode"`

	// HandoffWindow is how long after one track ends a new track can still be the same person.
	HandoffWindow time.Duration `yaml:"handoff_window"`

	// CameraHeartbeatTimeout is how long a camera may stay silent before its areas become unknown.
	CameraHeartbeatTimeout time.Duration `yaml:"camera_heartbeat_timeout"`

	// NobodyHomeDelay is how long isAnyoneHome must stay false before every token is removed.
	NobodyHomeDelay time.Duration `yaml:"nobody_home_delay"`

	// ExitWindow is how long before or after a token arrives in an area with exit sensors one of them may
	// turn on and still mean that person went outside. The sensor may fire before the token arrives because
	// Frigate ends a track a few seconds after the person leaves view.
	ExitWindow time.Duration `yaml:"exit_window"`

	// Areas are the units of state, by name.
	Areas map[string]*AreaConfig `yaml:"areas"`

	// Cameras optionally lists the areas each camera sees. The plugin also learns this from the zones
	// each camera reports. It decides which areas become unknown when a camera stops.
	Cameras map[string][]string `yaml:"cameras"`

	// Outputs maps a boolean state variable to the areas that make it true. Written only in active mode.
	Outputs map[string][]string `yaml:"outputs"`

	// ZoneOverrides maps a Frigate zone name to an area, to a boundary written as <area>_to_<area>, or to
	// "ignore". Use it for zone names that break the naming convention.
	ZoneOverrides map[string]string `yaml:"zone_overrides"`
}

// AreaConfig is one area.
type AreaConfig struct {
	// Seen is true when a camera looks at the area's floor.
	Seen bool `yaml:"seen"`

	// IdleLimit is how long a token may stay without evidence. A seen area counts from the last time a
	// camera saw a person there. An unseen area counts from when the token arrived. Absent: never expires.
	IdleLimit *time.Duration `yaml:"idle_limit"`

	// LeadsOutside marks an area with a door to the outside. It is reported in shadow state.
	LeadsOutside bool `yaml:"leads_outside"`

	// Neighbors are areas a person can walk to directly without a boundary zone, such as the parts of
	// one open space. A hand-off between neighbors keeps the token.
	Neighbors []string `yaml:"neighbors"`

	// BedSensors are binary sensors that, when on, mean someone is in a bed in this area.
	BedSensors []string `yaml:"bed_sensors"`

	// ExitSensors are binary sensors outside the area's outside door, such as an outdoor camera's person
	// sensor. One that turns on just after a token arrives means that person went outside. Only an unseen
	// area that leads outside may have them.
	ExitSensors []string `yaml:"exit_sensors"`
}

// LoadConfig reads the config file. A missing file returns (nil, nil): the plugin is unconfigured.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return ParseConfig(data)
}

// ParseConfig parses and validates config YAML, and fills defaults.
func ParseConfig(data []byte) (*Config, error) {
	var f fileConfig
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse room presence config: %w", err)
	}
	if f.RoomPresence == nil {
		return nil, fmt.Errorf("room presence config has no room_presence section")
	}
	cfg := f.RoomPresence
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Mode == "" {
		c.Mode = ModeLogOnly
	}
	if c.HandoffWindow == 0 {
		c.HandoffWindow = defaultHandoffWindow
	}
	if c.CameraHeartbeatTimeout == 0 {
		c.CameraHeartbeatTimeout = defaultCameraHeartbeatTimeout
	}
	if c.NobodyHomeDelay == 0 {
		c.NobodyHomeDelay = defaultNobodyHomeDelay
	}
	if c.ExitWindow == 0 {
		c.ExitWindow = defaultExitWindow
	}
}

func (c *Config) validate() error {
	var errs []string
	if c.Mode != ModeLogOnly && c.Mode != ModeActive {
		errs = append(errs, fmt.Sprintf("mode %q must be %s or %s", c.Mode, ModeLogOnly, ModeActive))
	}
	if len(c.Areas) == 0 {
		errs = append(errs, "no areas")
	}
	for _, name := range sortedKeys(c.Areas) {
		a := c.Areas[name]
		if a == nil {
			c.Areas[name] = &AreaConfig{}
			a = c.Areas[name]
		}
		if strings.Contains(name, "_to_") {
			errs = append(errs, fmt.Sprintf("area %q: a name with _to_ would read as a boundary zone", name))
		}
		if a.IdleLimit != nil && *a.IdleLimit <= 0 {
			errs = append(errs, fmt.Sprintf("area %q: idle_limit must be positive", name))
		}
		for _, n := range a.Neighbors {
			if _, ok := c.Areas[n]; !ok {
				errs = append(errs, fmt.Sprintf("area %q: unknown neighbor %q", name, n))
			}
		}
		if len(a.ExitSensors) > 0 && (a.Seen || !a.LeadsOutside) {
			// In a seen area a person who stands still has an unheld token too, so an exit sensor could
			// remove the token of someone still inside.
			errs = append(errs, fmt.Sprintf("area %q: exit_sensors needs an unseen area with leads_outside", name))
		}
	}
	if c.ExitWindow < 0 {
		errs = append(errs, "exit_window must be positive")
	}
	for cam, areas := range c.Cameras {
		for _, a := range areas {
			if _, ok := c.Areas[a]; !ok {
				errs = append(errs, fmt.Sprintf("camera %q: unknown area %q", cam, a))
			}
		}
	}
	for v, areas := range c.Outputs {
		if inputVar(v) {
			errs = append(errs, fmt.Sprintf("output %q: the plugin reads this variable, so it cannot write it", v))
		}
		if len(areas) == 0 {
			errs = append(errs, fmt.Sprintf("output %q: no areas", v))
		}
		for _, a := range areas {
			if _, ok := c.Areas[a]; !ok {
				errs = append(errs, fmt.Sprintf("output %q: unknown area %q", v, a))
			}
		}
	}
	for zone, target := range c.ZoneOverrides {
		if target == ignoreZone {
			continue
		}
		if _, ok := c.Areas[target]; ok {
			continue
		}
		if from, to, ok := strings.Cut(target, "_to_"); ok {
			_, okFrom := c.Areas[from]
			_, okTo := c.Areas[to]
			if okFrom && okTo {
				continue
			}
		}
		errs = append(errs, fmt.Sprintf("zone override %q: %q is not an area, a boundary or %q", zone, target, ignoreZone))
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("invalid room presence config: %s", strings.Join(errs, "; "))
	}
	return nil
}

// zoneKind is what a Frigate zone means.
type zoneKind int

const (
	zoneUnknown zoneKind = iota
	zoneIgnored
	zoneRoom
	zoneBoundary
)

// zoneInfo is a resolved Frigate zone. For a room zone, Area is the area. For a boundary zone, Area is the
// area the camera stands in and Beyond is the area past the doorway.
type zoneInfo struct {
	Kind   zoneKind
	Area   string
	Beyond string
}

// resolveZone reads a zone name by the naming convention and the overrides.
func (c *Config) resolveZone(zone string) zoneInfo {
	name := zone
	if target, ok := c.ZoneOverrides[zone]; ok {
		if target == ignoreZone {
			return zoneInfo{Kind: zoneIgnored}
		}
		name = target
	}
	if _, ok := c.Areas[name]; ok {
		return zoneInfo{Kind: zoneRoom, Area: name}
	}
	if from, to, ok := strings.Cut(name, "_to_"); ok {
		_, okFrom := c.Areas[from]
		_, okTo := c.Areas[to]
		if okFrom && okTo {
			return zoneInfo{Kind: zoneBoundary, Area: from, Beyond: to}
		}
	}
	return zoneInfo{Kind: zoneUnknown}
}

// BoundaryAreas reads a Frigate zone name as a boundary zone, by the naming convention and the overrides. It
// returns the area the camera stands in and the area beyond the doorway. ok is false for any other zone.
func (c *Config) BoundaryAreas(zone string) (area, beyond string, ok bool) {
	z := c.resolveZone(zone)
	return z.Area, z.Beyond, z.Kind == zoneBoundary
}

// neighbors reports whether a person can walk from area a to area b without a boundary zone.
func (c *Config) neighbors(a, b string) bool {
	if a == b {
		return true
	}
	for _, n := range c.Areas[a].Neighbors {
		if n == b {
			return true
		}
	}
	for _, n := range c.Areas[b].Neighbors {
		if n == a {
			return true
		}
	}
	return false
}

// sensorAreas lists the areas that use an entity as a bed sensor and as an exit sensor.
func (c *Config) sensorAreas(entity string) (beds, exits []string) {
	for _, name := range sortedKeys(c.Areas) {
		a := c.Areas[name]
		for _, e := range a.BedSensors {
			if e == entity {
				beds = append(beds, name)
			}
		}
		for _, e := range a.ExitSensors {
			if e == entity {
				exits = append(exits, name)
			}
		}
	}
	return beds, exits
}

// sensorEntities lists every bed and exit sensor, once each.
func (c *Config) sensorEntities() []string {
	set := map[string]bool{}
	for _, a := range c.Areas {
		for _, e := range a.BedSensors {
			set[e] = true
		}
		for _, e := range a.ExitSensors {
			set[e] = true
		}
	}
	return sortedKeys(set)
}

// inputVar reports whether the plugin reads a state variable.
func inputVar(key string) bool {
	if key == "isAnyoneHome" || key == "isHaveGuests" {
		return true
	}
	for _, v := range peopleVars {
		if v == key {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
