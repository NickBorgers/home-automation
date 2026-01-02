package plugin

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPlugin implements the Plugin interface for testing
type mockPlugin struct {
	name    string
	started bool
	stopped bool
}

func (m *mockPlugin) Name() string { return m.name }
func (m *mockPlugin) Start() error { m.started = true; return nil }
func (m *mockPlugin) Stop()        { m.stopped = true }

func TestRegistry_Register(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		info        PluginInfo
		wantErr     bool
		errContains string
	}{
		{
			name: "valid registration",
			info: PluginInfo{
				Name:        "test-plugin",
				Description: "A test plugin",
				Priority:    PriorityDefault,
				Factory:     func(ctx *Context) (Plugin, error) { return &mockPlugin{name: "test"}, nil },
			},
			wantErr: false,
		},
		{
			name: "empty name",
			info: PluginInfo{
				Name:    "",
				Factory: func(ctx *Context) (Plugin, error) { return nil, nil },
			},
			wantErr:     true,
			errContains: "name cannot be empty",
		},
		{
			name: "nil factory",
			info: PluginInfo{
				Name:    "test-plugin",
				Factory: nil,
			},
			wantErr:     true,
			errContains: "factory cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			registry := NewRegistry()
			err := registry.Register(tt.info)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRegistry_PriorityOverride(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()

	// Register default priority plugin
	defaultFactory := func(ctx *Context) (Plugin, error) {
		return &mockPlugin{name: "default"}, nil
	}
	err := registry.Register(PluginInfo{
		Name:        "security",
		Description: "Default security plugin",
		Priority:    PriorityDefault,
		Factory:     defaultFactory,
	})
	require.NoError(t, err)

	// Verify default is registered
	info := registry.Get("security")
	require.NotNil(t, info)
	assert.Equal(t, PriorityDefault, info.Priority)
	assert.Equal(t, "Default security plugin", info.Description)

	// Register override priority plugin
	overrideFactory := func(ctx *Context) (Plugin, error) {
		return &mockPlugin{name: "override"}, nil
	}
	err = registry.Register(PluginInfo{
		Name:        "security",
		Description: "Private security plugin",
		Priority:    PriorityOverride,
		Factory:     overrideFactory,
	})
	require.NoError(t, err)

	// Verify override took precedence
	info = registry.Get("security")
	require.NotNil(t, info)
	assert.Equal(t, PriorityOverride, info.Priority)
	assert.Equal(t, "Private security plugin", info.Description)

	// Verify we can create the override plugin
	plugin, err := info.Factory(nil)
	require.NoError(t, err)
	assert.Equal(t, "override", plugin.Name())
}

func TestRegistry_LowerPrioritySkipped(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()

	// Register high priority first
	err := registry.Register(PluginInfo{
		Name:        "security",
		Description: "High priority",
		Priority:    PriorityOverride,
		Factory:     func(ctx *Context) (Plugin, error) { return &mockPlugin{name: "high"}, nil },
	})
	require.NoError(t, err)

	// Try to register lower priority - should be skipped
	err = registry.Register(PluginInfo{
		Name:        "security",
		Description: "Low priority",
		Priority:    PriorityDefault,
		Factory:     func(ctx *Context) (Plugin, error) { return &mockPlugin{name: "low"}, nil },
	})
	require.NoError(t, err) // No error, just skipped

	// Verify high priority is still there
	info := registry.Get("security")
	require.NotNil(t, info)
	assert.Equal(t, "High priority", info.Description)
}

func TestRegistry_List(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()

	// Register plugins with different orders
	registry.Register(PluginInfo{
		Name:    "reset",
		Order:   90,
		Factory: func(ctx *Context) (Plugin, error) { return &mockPlugin{name: "reset"}, nil },
	})
	registry.Register(PluginInfo{
		Name:    "statetracking",
		Order:   10,
		Factory: func(ctx *Context) (Plugin, error) { return &mockPlugin{name: "statetracking"}, nil },
	})
	registry.Register(PluginInfo{
		Name:    "security",
		Order:   50,
		Factory: func(ctx *Context) (Plugin, error) { return &mockPlugin{name: "security"}, nil },
	})
	registry.Register(PluginInfo{
		Name:    "lighting",
		Order:   50,
		Factory: func(ctx *Context) (Plugin, error) { return &mockPlugin{name: "lighting"}, nil },
	})

	// List should be ordered by Order, then by name
	list := registry.List()
	require.Len(t, list, 4)

	assert.Equal(t, "statetracking", list[0].Name) // Order 10
	assert.Equal(t, "lighting", list[1].Name)      // Order 50, "l" < "s"
	assert.Equal(t, "security", list[2].Name)      // Order 50, "s"
	assert.Equal(t, "reset", list[3].Name)         // Order 90
}

func TestRegistry_CreateAll(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()

	created := make([]string, 0)

	registry.Register(PluginInfo{
		Name:  "first",
		Order: 10,
		Factory: func(ctx *Context) (Plugin, error) {
			created = append(created, "first")
			return &mockPlugin{name: "first"}, nil
		},
	})
	registry.Register(PluginInfo{
		Name:  "second",
		Order: 20,
		Factory: func(ctx *Context) (Plugin, error) {
			created = append(created, "second")
			return &mockPlugin{name: "second"}, nil
		},
	})

	plugins, err := registry.CreateAll(nil)
	require.NoError(t, err)
	require.Len(t, plugins, 2)

	// Verify creation order
	assert.Equal(t, []string{"first", "second"}, created)
	assert.Equal(t, "first", plugins[0].Name())
	assert.Equal(t, "second", plugins[1].Name())
}

func TestRegistry_CreateAll_ErrorCleanup(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()

	plugin1 := &mockPlugin{name: "first"}
	registry.Register(PluginInfo{
		Name:  "first",
		Order: 10,
		Factory: func(ctx *Context) (Plugin, error) {
			return plugin1, nil
		},
	})
	registry.Register(PluginInfo{
		Name:  "second",
		Order: 20,
		Factory: func(ctx *Context) (Plugin, error) {
			return nil, errors.New("creation failed")
		},
	})

	plugins, err := registry.CreateAll(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create plugin second")
	assert.Nil(t, plugins)

	// Verify cleanup happened - first plugin should be stopped
	assert.True(t, plugin1.stopped, "first plugin should have been stopped on cleanup")
}

func TestRegistry_Get_NotFound(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	info := registry.Get("nonexistent")
	assert.Nil(t, info)
}

func TestRegistry_Names(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()

	registry.Register(PluginInfo{
		Name:    "alpha",
		Factory: func(ctx *Context) (Plugin, error) { return &mockPlugin{}, nil },
	})
	registry.Register(PluginInfo{
		Name:    "beta",
		Factory: func(ctx *Context) (Plugin, error) { return &mockPlugin{}, nil },
	})

	names := registry.Names()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "alpha")
	assert.Contains(t, names, "beta")
}

func TestRegistry_Clear(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()

	registry.Register(PluginInfo{
		Name:    "test",
		Factory: func(ctx *Context) (Plugin, error) { return &mockPlugin{}, nil },
	})

	assert.Len(t, registry.Names(), 1)

	registry.Clear()

	assert.Len(t, registry.Names(), 0)
	assert.Nil(t, registry.Get("test"))
}

func TestRegistry_DefaultOrder(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()

	// Register without specifying Order
	err := registry.Register(PluginInfo{
		Name:    "test",
		Factory: func(ctx *Context) (Plugin, error) { return &mockPlugin{}, nil },
	})
	require.NoError(t, err)

	info := registry.Get("test")
	require.NotNil(t, info)
	assert.Equal(t, 50, info.Order, "default order should be 50")
}

func TestGlobalRegistry(t *testing.T) {
	t.Parallel(
	// Clear global registry for clean test
	)

	ClearGlobal()
	defer ClearGlobal()

	err := Register(PluginInfo{
		Name:        "global-test",
		Description: "Testing global registry",
		Factory:     func(ctx *Context) (Plugin, error) { return &mockPlugin{name: "global"}, nil },
	})
	require.NoError(t, err)

	// Test Get
	info := Get("global-test")
	require.NotNil(t, info)
	assert.Equal(t, "Testing global registry", info.Description)

	// Test List
	list := List()
	assert.Len(t, list, 1)

	// Test Names
	names := Names()
	assert.Contains(t, names, "global-test")

	// Test CreateAll
	plugins, err := CreateAll(nil)
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "global", plugins[0].Name())
}
