package music

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
		Name:        "music",
		Description: "Music control - selects music modes based on day phase and presence",
		Priority:    plugin.PriorityDefault,
		Order:       40, // After energy (30), before sleep hygiene (45)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new Music plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("music plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("music plugin requires internal state.Manager")
	}

	// Load music configuration
	configPath := filepath.Join(ctx.ConfigDir, "music_config.yaml")
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load music config: %w", err)
	}

	// Pass the TimeProvider and Timezone directly (NewManager handles nil by defaulting to RealTimeProvider and time.Local)
	manager := NewManager(haClient, stateManager, config, ctx.Logger, ctx.ReadOnly, ctx.TimeProvider, ctx.Timezone)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "music"
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
