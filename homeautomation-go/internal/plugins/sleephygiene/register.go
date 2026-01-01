package sleephygiene

import (
	"fmt"

	"homeautomation/internal/config"
	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
	pkgstate "homeautomation/pkg/state"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "sleephygiene",
		Description: "Sleep hygiene - manages wake-up sequences and alarm integration",
		Priority:    plugin.PriorityDefault,
		Order:       45, // After music (40), before lighting (50)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new SleepHygiene plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("sleephygiene plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("sleephygiene plugin requires internal state.Manager")
	}

	// Load schedule configuration with timezone for schedule parsing
	configLoader := config.NewLoader(ctx.ConfigDir, ctx.Logger)
	configLoader.SetTimezone(ctx.Timezone)
	if err := configLoader.LoadScheduleConfig(); err != nil {
		return nil, fmt.Errorf("failed to load schedule config: %w", err)
	}

	// Pass the TimeProvider directly (NewManager handles nil by defaulting to RealTimeProvider)
	manager := NewManager(haClient, stateManager, configLoader, ctx.Logger, ctx.ReadOnly, ctx.TimeProvider, ctx.Timezone)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "sleephygiene"
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
func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager instance.
func (p *pluginAdapter) GetManager() *Manager {
	return p.manager
}
