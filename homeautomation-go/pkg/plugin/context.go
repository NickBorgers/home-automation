package plugin

import (
	"time"

	"homeautomation/internal/shadowstate"
	pkgha "homeautomation/pkg/ha"
	pkgstate "homeautomation/pkg/state"

	"go.uber.org/zap"
)

// Context provides dependencies to plugins during initialization.
// It wraps the core services needed by all plugins in a single struct
// for cleaner constructor signatures.
//
// Note: HAClient and StateManager use interface types from pkg/ha and pkg/state
// respectively, which allows external packages to work with these types.
// The actual implementations from internal/ha and internal/state satisfy
// these interfaces.
type Context struct {
	// HAClient provides access to Home Assistant for service calls
	// and entity state subscriptions.
	HAClient pkgha.Client

	// StateManager provides access to the state variable system
	// for reading and writing state, and subscribing to changes.
	StateManager pkgstate.Manager

	// Logger is a structured logger for the plugin to use.
	// Plugins should use logger.Named("pluginname") for namespacing.
	Logger *zap.Logger

	// ReadOnly indicates whether the application is in read-only mode.
	// When true, plugins should log what they would do but not make
	// actual changes to Home Assistant entities.
	ReadOnly bool

	// ConfigDir is the path to the configuration directory.
	// Plugins that need configuration files can find them here.
	ConfigDir string

	// Timezone is the configured timezone for time-based calculations.
	// Plugins that deal with time windows or schedules should use this.
	Timezone *time.Location

	// Registry is the subscription registry for automatic shadow state input tracking.
	// Plugins should use this to register their HA and state subscriptions.
	Registry *shadowstate.SubscriptionRegistry

	// TimeProvider provides testable time for plugins.
	// When nil, plugins should default to RealTimeProvider.
	TimeProvider TimeProvider

	// Latitude is the geographic latitude for sun event calculations.
	Latitude float64

	// Longitude is the geographic longitude for sun event calculations.
	Longitude float64
}

// NewContext creates a new plugin context with all required dependencies.
func NewContext(
	haClient pkgha.Client,
	stateManager pkgstate.Manager,
	logger *zap.Logger,
	readOnly bool,
	configDir string,
	timezone *time.Location,
) *Context {
	return &Context{
		HAClient:     haClient,
		StateManager: stateManager,
		Logger:       logger,
		ReadOnly:     readOnly,
		ConfigDir:    configDir,
		Timezone:     timezone,
	}
}
