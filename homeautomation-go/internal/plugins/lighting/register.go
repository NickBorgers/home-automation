package lighting

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
		Name:        "lighting",
		Description: "Lighting control - manages room scenes based on day phase and presence",
		Priority:    plugin.PriorityDefault,
		Order:       50, // After music (40), before security (60)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new Lighting plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("lighting plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("lighting plugin requires internal state.Manager")
	}

	// Load lighting configuration
	configPath := filepath.Join(ctx.ConfigDir, "hue_config.yaml")
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load lighting config: %w", err)
	}

	manager := NewManager(ctx.ShutdownCtx, haClient, stateManager, config, ctx.Logger, ctx.ReadOnly, ctx.Registry)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "lighting"
}

func (p *pluginAdapter) Start() error {
	if err := p.manager.Start(); err != nil {
		return err
	}
	// Enable production debouncing of per-room evaluations. Arrival flips
	// several presence variables in a sub-second burst; without coalescing,
	// the lighting plugin fires the same scene recall several times and
	// destabilizes the Hue Bridge's dynamic palette.
	p.manager.SetDebounceDelay(defaultLightingDebounceDelay)
	return nil
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
