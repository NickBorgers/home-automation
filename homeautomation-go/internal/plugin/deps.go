// Package plugin provides standard dependency types for internal plugin constructors.
//
// This package defines StandardDeps and OptionalDeps structs that standardize
// how dependencies are passed to plugin NewManager() constructors. These types
// use internal types (ha.HAClient, *state.Manager) and are intended for use
// within internal/plugins/ packages only.
//
// For the public plugin system (registry, factory, lifecycle), see pkg/plugin/.
//
// # Background
//
// Historically, each plugin's NewManager() had a different parameter order and
// set of parameters, making it error-prone to add new dependencies or create
// new plugins. This package establishes a standard pattern for new plugins
// (2026+). Existing plugins are NOT migrated to avoid churn.
//
// # Usage
//
//	func NewManager(deps plugin.StandardDeps, opts plugin.OptionalDeps, config *MyConfig) *Manager {
//	    return &Manager{
//	        ctx:          deps.Ctx,
//	        haClient:     deps.HAClient,
//	        stateManager: deps.StateManager,
//	        logger:       deps.Logger.Named("myplugin"),
//	        readOnly:     deps.ReadOnly,
//	        registry:     deps.Registry,
//	        timeProvider: opts.TimeProviderOrDefault(),
//	        timezone:     opts.TimezoneOrDefault(),
//	    }
//	}
package plugin

import (
	"context"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	pkgplugin "homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

// StandardDeps contains the common dependencies required by all plugins.
// Every plugin needs these to function. Pass this as the first parameter
// to NewManager().
type StandardDeps struct {
	// Ctx is the shutdown context for graceful cancellation.
	// Cancelled during application shutdown to abort retry loops.
	Ctx context.Context

	// HAClient provides access to Home Assistant for service calls
	// and entity state subscriptions.
	HAClient ha.HAClient

	// StateManager provides access to the state variable system
	// for reading/writing state and subscribing to changes.
	StateManager *state.Manager

	// Logger is a structured logger. Plugins should call
	// Logger.Named("pluginname") for namespaced logging.
	Logger *zap.Logger

	// ReadOnly indicates whether the application is in read-only mode.
	// When true, plugins should log intended actions without executing them.
	ReadOnly bool

	// Registry is the subscription registry for automatic shadow state
	// input tracking. Plugins use this to register HA and state subscriptions.
	Registry *shadowstate.SubscriptionRegistry
}

// OptionalDeps contains optional or plugin-specific dependencies.
// Not all plugins need these. Pass this as the second parameter to
// NewManager(). Zero-value fields indicate the dependency is not needed;
// use the helper methods to get safe defaults.
type OptionalDeps struct {
	// TimeProvider provides testable time. When nil, defaults to
	// pkg/plugin.RealTimeProvider via TimeProviderOrDefault().
	TimeProvider pkgplugin.TimeProvider

	// Timezone is the configured timezone for time-based calculations.
	// When nil, defaults to time.Local via TimezoneOrDefault().
	Timezone *time.Location

	// NtfyClient provides push notifications via ntfy.sh.
	// May be nil if notifications are not configured or not needed.
	NtfyClient ntfy.Notifier

	// Latitude is the geographic latitude for sun event calculations.
	Latitude float64

	// Longitude is the geographic longitude for sun event calculations.
	Longitude float64
}

// TimeProviderOrDefault returns the TimeProvider if set, or
// pkg/plugin.RealTimeProvider{} as a safe default.
func (o OptionalDeps) TimeProviderOrDefault() pkgplugin.TimeProvider {
	if o.TimeProvider != nil {
		return o.TimeProvider
	}
	return pkgplugin.RealTimeProvider{}
}

// TimezoneOrDefault returns the Timezone if set, or time.Local as a safe default.
func (o OptionalDeps) TimezoneOrDefault() *time.Location {
	if o.Timezone != nil {
		return o.Timezone
	}
	return time.Local
}
