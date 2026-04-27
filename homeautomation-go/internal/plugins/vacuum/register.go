package vacuum

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
		Name:        "vacuum",
		Description: "Robot vacuum monitoring (today: error TTS announcements)",
		Priority:    plugin.PriorityDefault,
		Order:       60,
		Factory:     createPlugin,
	})
}

func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("vacuum plugin requires internal ha.HAClient")
	}
	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("vacuum plugin requires internal state.Manager")
	}

	configPath := filepath.Join(ctx.ConfigDir, "vacuum_config.yaml")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load vacuum config: %w", err)
	}

	manager := NewManager(ctx.ShutdownCtx, haClient, stateManager, cfg, ctx.Logger, ctx.ReadOnly, ctx.TimeProvider, ctx.Registry)
	return &pluginAdapter{manager: manager}, nil
}

type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string { return "vacuum" }
func (p *pluginAdapter) Start() error { return p.manager.Start() }
func (p *pluginAdapter) Stop()        { p.manager.Stop() }
func (p *pluginAdapter) Reset() error { return p.manager.Reset() }

func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

func (p *pluginAdapter) GetManager() *Manager { return p.manager }
