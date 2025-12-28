package dayphase

import (
	"fmt"

	"homeautomation/internal/config"
	dayphaselib "homeautomation/internal/dayphase"
	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	pkgstate "homeautomation/pkg/state"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "dayphase",
		Description: "Day phase - tracks sun events and calculates day phase",
		Priority:    plugin.PriorityDefault,
		Order:       20, // After state tracking (10), before energy (30)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new DayPhase plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("dayphase plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("dayphase plugin requires internal state.Manager")
	}

	// Load schedule configuration (needed for day phase calculation)
	configLoader := config.NewLoader(ctx.ConfigDir, ctx.Logger)
	if err := configLoader.LoadScheduleConfig(); err != nil {
		return nil, fmt.Errorf("failed to load schedule config: %w", err)
	}

	// Create day phase calculator with coordinates from context
	calculator := dayphaselib.NewCalculator(ctx.Latitude, ctx.Longitude, ctx.Logger)

	manager := NewManager(haClient, stateManager, configLoader, calculator, ctx.Logger, ctx.ReadOnly)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "dayphase"
}

func (p *pluginAdapter) Start() error {
	return p.manager.Start()
}

func (p *pluginAdapter) Stop() {
	p.manager.Stop()
}

// Implement plugin.Resettable
func (p *pluginAdapter) Reset() error {
	return p.manager.Reset()
}

// Implement plugin.ShadowStateProvider
func (p *pluginAdapter) GetShadowState() interface{} {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager instance.
func (p *pluginAdapter) GetManager() *Manager {
	return p.manager
}
