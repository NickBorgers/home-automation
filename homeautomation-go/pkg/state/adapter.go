package state

import (
	"homeautomation/internal/state"
)

// ManagerAdapter wraps internal state.Manager to implement pkg state.Manager
type ManagerAdapter struct {
	internal *state.Manager
}

// WrapManager wraps an internal state.Manager to implement the pkg state.Manager interface
func WrapManager(m *state.Manager) Manager {
	return &ManagerAdapter{internal: m}
}

// UnwrapManager returns the underlying internal manager if available
func UnwrapManager(m Manager) *state.Manager {
	if adapter, ok := m.(*ManagerAdapter); ok {
		return adapter.internal
	}
	return nil
}

func (a *ManagerAdapter) SyncFromHA() error {
	return a.internal.SyncFromHA()
}

func (a *ManagerAdapter) GetBool(key string) (bool, error) {
	return a.internal.GetBool(key)
}

func (a *ManagerAdapter) SetBool(key string, value bool) error {
	return a.internal.SetBool(key, value)
}

func (a *ManagerAdapter) CompareAndSwapBool(key string, old, new bool) (bool, error) {
	return a.internal.CompareAndSwapBool(key, old, new)
}

func (a *ManagerAdapter) GetString(key string) (string, error) {
	return a.internal.GetString(key)
}

func (a *ManagerAdapter) SetString(key string, value string) error {
	return a.internal.SetString(key, value)
}

func (a *ManagerAdapter) GetNumber(key string) (float64, error) {
	return a.internal.GetNumber(key)
}

func (a *ManagerAdapter) SetNumber(key string, value float64) error {
	return a.internal.SetNumber(key, value)
}

func (a *ManagerAdapter) GetJSON(key string, target interface{}) error {
	return a.internal.GetJSON(key, target)
}

func (a *ManagerAdapter) SetJSON(key string, value interface{}) error {
	return a.internal.SetJSON(key, value)
}

func (a *ManagerAdapter) Subscribe(key string, handler StateChangeHandler) (Subscription, error) {
	// Create wrapper handler that converts between handler types
	internalHandler := func(k string, oldValue, newValue interface{}) {
		handler(k, oldValue, newValue)
	}
	return a.internal.Subscribe(key, internalHandler)
}

func (a *ManagerAdapter) GetAllValues() map[string]interface{} {
	return a.internal.GetAllValues()
}

func (a *ManagerAdapter) WaitForProcessing() {
	a.internal.WaitForProcessing()
}
