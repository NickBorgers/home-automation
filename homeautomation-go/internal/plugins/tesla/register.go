package tesla

import (
	teslaapi "homeautomation/internal/tesla"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "tesla",
		Description: "Tesla Fleet API: polls vehicle and charge state, and signs vehicle commands",
		Priority:    plugin.PriorityDefault,
		Order:       96, // Independent: no other plugin reads anything this one produces
		Factory:     createPlugin,
	})
}

// createPlugin builds the Tesla plugin. Unlike most plugins it needs neither
// Home Assistant nor the state manager: it talks straight to Tesla and keeps
// what it learns in shadow state.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	manager := NewManager(ctx.ShutdownCtx, ctx.Logger)
	return &pluginAdapter{manager: manager}, nil
}

type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string { return "tesla" }
func (p *pluginAdapter) Start() error { return p.manager.Start() }
func (p *pluginAdapter) Stop()        { p.manager.Stop() }
func (p *pluginAdapter) Reset() error { return p.manager.Reset() }

func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

func (p *pluginAdapter) GetManager() *Manager { return p.manager }

// TeslaAuthenticator exposes the OAuth handler to cmd/app, which hands it to
// the API server so the login and callback endpoints work. It returns nil when
// Tesla credentials are absent.
func (p *pluginAdapter) TeslaAuthenticator() *teslaapi.Authenticator {
	return p.manager.Authenticator()
}
