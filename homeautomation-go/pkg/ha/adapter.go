package ha

import (
	"homeautomation/internal/ha"
)

// internalToState converts internal ha.State to pkg ha.State
func internalToState(s *ha.State) *State {
	if s == nil {
		return nil
	}
	return &State{
		EntityID:    s.EntityID,
		State:       s.State,
		Attributes:  s.Attributes,
		LastChanged: s.LastChanged,
		LastUpdated: s.LastUpdated,
	}
}

// ClientAdapter wraps internal ha.HAClient to implement pkg ha.Client
type ClientAdapter struct {
	internal ha.HAClient
}

// WrapClient wraps an internal ha.HAClient to implement the pkg ha.Client interface
func WrapClient(c ha.HAClient) Client {
	return &ClientAdapter{internal: c}
}

// UnwrapClient returns the underlying internal client if available
func UnwrapClient(c Client) ha.HAClient {
	if adapter, ok := c.(*ClientAdapter); ok {
		return adapter.internal
	}
	return nil
}

func (a *ClientAdapter) Connect() error {
	return a.internal.Connect()
}

func (a *ClientAdapter) Disconnect() error {
	return a.internal.Disconnect()
}

func (a *ClientAdapter) IsConnected() bool {
	return a.internal.IsConnected()
}

func (a *ClientAdapter) GetState(entityID string) (*State, error) {
	s, err := a.internal.GetState(entityID)
	if err != nil {
		return nil, err
	}
	return internalToState(s), nil
}

func (a *ClientAdapter) GetAllStates() ([]*State, error) {
	states, err := a.internal.GetAllStates()
	if err != nil {
		return nil, err
	}
	result := make([]*State, len(states))
	for i, s := range states {
		result[i] = internalToState(s)
	}
	return result, nil
}

func (a *ClientAdapter) CallService(domain, service string, data map[string]interface{}) error {
	return a.internal.CallService(domain, service, data)
}

func (a *ClientAdapter) CallServiceWithTarget(domain, service string, target *ServiceTarget, data map[string]interface{}) error {
	// Convert pkg ServiceTarget to internal ServiceTarget
	var internalTarget *ha.ServiceTarget
	if target != nil {
		internalTarget = &ha.ServiceTarget{
			EntityID: target.EntityID,
			LabelID:  target.LabelID,
			AreaID:   target.AreaID,
		}
	}
	return a.internal.CallServiceWithTarget(domain, service, internalTarget, data)
}

func (a *ClientAdapter) SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error) {
	// Create a wrapper handler that converts internal states to pkg states
	internalHandler := func(entity string, oldState, newState *ha.State) {
		handler(entity, internalToState(oldState), internalToState(newState))
	}
	return a.internal.SubscribeStateChanges(entityID, internalHandler)
}

func (a *ClientAdapter) SetInputBoolean(name string, value bool) error {
	return a.internal.SetInputBoolean(name, value)
}

func (a *ClientAdapter) SetInputNumber(name string, value float64) error {
	return a.internal.SetInputNumber(name, value)
}

func (a *ClientAdapter) SetInputText(name string, value string) error {
	return a.internal.SetInputText(name, value)
}
