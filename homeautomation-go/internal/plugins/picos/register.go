package picos

import (
	"fmt"
	"path/filepath"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "picos",
		Description: "Lutron Pico remotes step the brightness of light groups",
		Priority:    plugin.PriorityDefault,
		Order:       55, // After lighting (50). It needs no other plugin.
		Factory:     createPlugin,
	})
}

// createPlugin builds the picos plugin from a plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("picos plugin requires internal ha.HAClient")
	}

	cfg, err := LoadConfig(filepath.Join(ctx.ConfigDir, "pico_config.yaml"))
	if err != nil {
		return nil, fmt.Errorf("load picos config: %w", err)
	}

	return &pluginAdapter{manager: NewManager(ctx.ShutdownCtx, haClient, cfg, ctx.Logger, ctx.ReadOnly)}, nil
}

// pluginAdapter wraps Manager to satisfy plugin.Plugin.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string { return "picos" }

func (p *pluginAdapter) Start() error { return p.manager.Start() }

func (p *pluginAdapter) Stop() { p.manager.Stop() }

// GetShadowState implements plugin.ShadowStateProvider.
func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager (used by tests).
func (p *pluginAdapter) GetManager() *Manager { return p.manager }
