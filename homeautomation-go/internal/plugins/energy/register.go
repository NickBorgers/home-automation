package energy

import (
	"fmt"
	"path/filepath"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
	pkgstate "homeautomation/pkg/state"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "energy",
		Description: "Energy state - tracks battery, solar, and computes energy levels",
		Priority:    plugin.PriorityDefault,
		Order:       30, // After day phase (20), before music (40)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new Energy plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("energy plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("energy plugin requires internal state.Manager")
	}

	// Load energy configuration
	configPath := filepath.Join(ctx.ConfigDir, "energy_config.yaml")
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load energy config: %w", err)
	}

	manager := NewManager(haClient, stateManager, config, ctx.Logger, ctx.ReadOnly, ctx.Timezone, ctx.Registry)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "energy"
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
