package tv

import (
	"fmt"
	"os"

	"homeautomation/internal/bravia"
	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
	pkgstate "homeautomation/pkg/state"

	"go.uber.org/zap"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "tv",
		Description: "TV monitoring - tracks Apple TV, sync box, and HDMI input states",
		Priority:    plugin.PriorityDefault,
		Order:       70, // After security (60)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new TV plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("tv plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("tv plugin requires internal state.Manager")
	}

	manager := NewManager(ctx.ShutdownCtx, haClient, stateManager, ctx.Logger, ctx.ReadOnly, ctx.Registry)

	// TV picture brightness goes straight to the TV's REST API. Home Assistant does not expose it.
	ip, psk := os.Getenv("BRAVIA_IP"), os.Getenv("BRAVIA_PSK")
	logger := ctx.Logger.Named("tv")
	switch {
	case ip != "" && psk != "":
		manager.SetPictureClient(bravia.NewClient(ip, psk))
		// The PSK length (never the value) shows whether quoting in .env mangled special characters.
		logger.Info("TV brightness control enabled", zap.String("bravia_ip", ip), zap.Int("psk_length", len(psk)))
	case ip != "" || psk != "":
		logger.Warn("TV brightness control disabled: set both BRAVIA_IP and BRAVIA_PSK")
	default:
		logger.Info("TV brightness control disabled: BRAVIA_IP unset; only the sync box follows the room")
	}
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "tv"
}

func (p *pluginAdapter) Start() error {
	return p.manager.Start()
}

func (p *pluginAdapter) Stop() {
	p.manager.Stop()
}

// Implement plugin.ShadowStateProvider
func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager instance.
func (p *pluginAdapter) GetManager() *Manager {
	return p.manager
}
