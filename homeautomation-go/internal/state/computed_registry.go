package state

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"homeautomation/internal/clock"

	"go.uber.org/zap"
)

// AnyoneHomeDepartureDebounceDelay is how long isAnyoneHome must remain false
// before the computed output emits the departure.
const AnyoneHomeDepartureDebounceDelay = 5 * time.Minute

// UpdateMode defines how a computed state is updated
type UpdateMode int

const (
	// UpdateOnDependencyChange recomputes when any dependency changes
	UpdateOnDependencyChange UpdateMode = iota
	// UpdatePeriodically recomputes on a timer
	UpdatePeriodically
)

// ComputeContext provides access to state values during computation
type ComputeContext struct {
	manager *Manager
	logger  *zap.Logger
}

// GetBool retrieves a boolean state value
func (c *ComputeContext) GetBool(key string) (bool, error) {
	return c.manager.GetBool(key)
}

// GetString retrieves a string state value
func (c *ComputeContext) GetString(key string) (string, error) {
	return c.manager.GetString(key)
}

// GetNumber retrieves a number state value
func (c *ComputeContext) GetNumber(key string) (float64, error) {
	return c.manager.GetNumber(key)
}

// Logger returns the logger for the compute context
func (c *ComputeContext) Logger() *zap.Logger {
	return c.logger
}

// ComputedStateProvider defines a computed state
type ComputedStateProvider struct {
	// Name is the state variable name (e.g., "isAnyOwnerHome")
	Name string
	// Dependencies are state variable keys this computed state depends on
	Dependencies []string
	// ComputeFunc computes the new value from dependencies
	// Returns the new value and any error
	ComputeFunc func(ctx *ComputeContext) (interface{}, error)
	// UpdateMode determines when to recompute (dependency change or periodic)
	UpdateMode UpdateMode
	// PeriodicInterval is the interval for periodic updates (only for UpdatePeriodically mode)
	PeriodicInterval time.Duration
	// OnComputed is an optional callback invoked after successful computation
	// This is useful for shadow state updates
	OnComputed func(newValue interface{})
}

// providerState tracks runtime state for a registered provider
type providerState struct {
	provider      *ComputedStateProvider
	subscriptions []Subscription
	stopChan      chan struct{}
	stoppedChan   chan struct{} // Closed when the goroutine exits
}

// ComputedStateRegistry manages all computed state providers
type ComputedStateRegistry struct {
	manager   *Manager
	logger    *zap.Logger
	providers map[string]*providerState
	mu        sync.RWMutex
	started   bool

	clock                    clock.Clock
	anyoneHomeDepartureMu    sync.Mutex
	anyoneHomeDepartureTimer clock.Timer
}

// NewComputedStateRegistry creates a new computed state registry
func NewComputedStateRegistry(manager *Manager, logger *zap.Logger) *ComputedStateRegistry {
	return &ComputedStateRegistry{
		manager:   manager,
		logger:    logger.Named("computed-registry"),
		providers: make(map[string]*providerState),
		clock:     clock.NewRealClock(),
	}
}

// Register adds a computed state provider to the registry
// The provider will not be active until Start() is called
func (r *ComputedStateRegistry) Register(provider *ComputedStateProvider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return fmt.Errorf("cannot register provider after registry has started")
	}

	if provider.Name == "" {
		return fmt.Errorf("provider name is required")
	}

	if provider.ComputeFunc == nil {
		return fmt.Errorf("provider %s: ComputeFunc is required", provider.Name)
	}

	if _, exists := r.providers[provider.Name]; exists {
		return fmt.Errorf("provider %s is already registered", provider.Name)
	}

	// Validate dependencies exist
	for _, dep := range provider.Dependencies {
		if _, ok := r.manager.variables[dep]; !ok {
			return fmt.Errorf("provider %s: dependency %s is not a valid state variable", provider.Name, dep)
		}
	}

	// Validate periodic interval for periodic mode
	if provider.UpdateMode == UpdatePeriodically && provider.PeriodicInterval <= 0 {
		return fmt.Errorf("provider %s: PeriodicInterval is required for UpdatePeriodically mode", provider.Name)
	}

	r.providers[provider.Name] = &providerState{
		provider: provider,
	}

	r.logger.Info("Registered computed state provider",
		zap.String("name", provider.Name),
		zap.Strings("dependencies", provider.Dependencies),
		zap.Int("update_mode", int(provider.UpdateMode)))

	return nil
}

// Start activates all registered providers
// This sets up subscriptions and runs initial computations
func (r *ComputedStateRegistry) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return fmt.Errorf("registry already started")
	}

	// Topologically sort providers based on dependencies
	// This ensures Level 1 computed states are initialized before Level 2
	order, err := r.topologicalSort()
	if err != nil {
		return fmt.Errorf("failed to sort providers: %w", err)
	}

	r.logger.Info("Starting computed state registry",
		zap.Int("provider_count", len(r.providers)),
		zap.Strings("init_order", order))

	// Initialize each provider in dependency order
	for _, name := range order {
		ps := r.providers[name]
		if err := r.startProvider(ps); err != nil {
			// Clean up any started providers on failure
			r.stopAllProviders()
			return fmt.Errorf("failed to start provider %s: %w", name, err)
		}
	}

	r.started = true
	r.logger.Info("Computed state registry started successfully")
	return nil
}

// startProvider initializes a single provider
func (r *ComputedStateRegistry) startProvider(ps *providerState) error {
	provider := ps.provider

	// Do initial computation
	if err := r.computeAndSet(provider); err != nil {
		return fmt.Errorf("initial computation failed: %w", err)
	}

	switch provider.UpdateMode {
	case UpdateOnDependencyChange:
		// Subscribe to all dependencies
		for _, dep := range provider.Dependencies {
			sub, err := r.manager.Subscribe(dep, func(key string, oldValue, newValue interface{}) {
				if err := r.computeAndSet(provider); err != nil {
					r.logger.Error("Failed to recompute on dependency change",
						zap.String("computed", provider.Name),
						zap.String("dependency", key),
						zap.Error(err))
				}
			})
			if err != nil {
				// Clean up any subscriptions we made
				for _, s := range ps.subscriptions {
					s.Unsubscribe()
				}
				return fmt.Errorf("failed to subscribe to %s: %w", dep, err)
			}
			ps.subscriptions = append(ps.subscriptions, sub)
		}

	case UpdatePeriodically:
		// Start periodic update goroutine
		ps.stopChan = make(chan struct{})
		ps.stoppedChan = make(chan struct{})
		go r.runPeriodic(ps)
	}

	r.logger.Debug("Provider started",
		zap.String("name", provider.Name),
		zap.Int("subscription_count", len(ps.subscriptions)))

	return nil
}

// runPeriodic runs periodic updates for a provider
func (r *ComputedStateRegistry) runPeriodic(ps *providerState) {
	defer close(ps.stoppedChan)

	ticker := time.NewTicker(ps.provider.PeriodicInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := r.computeAndSet(ps.provider); err != nil {
				r.logger.Error("Failed periodic computation",
					zap.String("computed", ps.provider.Name),
					zap.Error(err))
			}
		case <-ps.stopChan:
			return
		}
	}
}

// computeAndSet runs the computation and updates the state
func (r *ComputedStateRegistry) computeAndSet(provider *ComputedStateProvider) error {
	ctx := &ComputeContext{
		manager: r.manager,
		logger:  r.logger.Named(provider.Name),
	}

	newValue, err := provider.ComputeFunc(ctx)
	if err != nil {
		return fmt.Errorf("computation failed: %w", err)
	}

	// Set the value based on type
	variable, ok := r.manager.variables[provider.Name]
	if !ok {
		return fmt.Errorf("state variable %s not found", provider.Name)
	}

	switch variable.Type {
	case TypeBool:
		boolVal, ok := newValue.(bool)
		if !ok {
			return fmt.Errorf("expected bool for %s, got %T", provider.Name, newValue)
		}
		if provider.Name == "isAnyoneHome" {
			return r.setAnyoneHomeWithDepartureDebounce(provider, boolVal)
		}
		if err := r.manager.SetBool(provider.Name, boolVal); err != nil {
			return fmt.Errorf("failed to set bool: %w", err)
		}
	case TypeString:
		strVal, ok := newValue.(string)
		if !ok {
			return fmt.Errorf("expected string for %s, got %T", provider.Name, newValue)
		}
		if err := r.manager.SetString(provider.Name, strVal); err != nil {
			return fmt.Errorf("failed to set string: %w", err)
		}
	case TypeNumber:
		numVal, ok := newValue.(float64)
		if !ok {
			return fmt.Errorf("expected float64 for %s, got %T", provider.Name, newValue)
		}
		if err := r.manager.SetNumber(provider.Name, numVal); err != nil {
			return fmt.Errorf("failed to set number: %w", err)
		}
	default:
		return fmt.Errorf("unsupported type %s for computed state %s", variable.Type, provider.Name)
	}

	// Invoke callback if set
	if provider.OnComputed != nil {
		provider.OnComputed(newValue)
	}

	return nil
}

func (r *ComputedStateRegistry) setAnyoneHomeWithDepartureDebounce(
	provider *ComputedStateProvider,
	newValue bool,
) error {
	if newValue {
		r.cancelAnyoneHomeDepartureDebounce()
		if err := r.manager.SetBool(provider.Name, true); err != nil {
			return fmt.Errorf("failed to set bool: %w", err)
		}
		if provider.OnComputed != nil {
			provider.OnComputed(true)
		}
		return nil
	}

	currentValue, err := r.manager.GetBool(provider.Name)
	if err != nil {
		return fmt.Errorf("failed to get current bool: %w", err)
	}
	if !currentValue {
		if err := r.manager.SetBool(provider.Name, false); err != nil {
			return fmt.Errorf("failed to set bool: %w", err)
		}
		if provider.OnComputed != nil {
			provider.OnComputed(false)
		}
		return nil
	}

	r.anyoneHomeDepartureMu.Lock()
	if r.anyoneHomeDepartureTimer != nil {
		r.anyoneHomeDepartureMu.Unlock()
		return nil
	}
	r.anyoneHomeDepartureTimer = r.clock.AfterFunc(AnyoneHomeDepartureDebounceDelay, func() {
		r.finishAnyoneHomeDepartureDebounce(provider)
	})
	r.anyoneHomeDepartureMu.Unlock()

	r.logger.Info("Started isAnyoneHome departure debounce",
		zap.Duration("delay", AnyoneHomeDepartureDebounceDelay))
	return nil
}

func (r *ComputedStateRegistry) cancelAnyoneHomeDepartureDebounce() {
	r.anyoneHomeDepartureMu.Lock()
	defer r.anyoneHomeDepartureMu.Unlock()

	if r.anyoneHomeDepartureTimer == nil {
		return
	}
	r.anyoneHomeDepartureTimer.Stop()
	r.anyoneHomeDepartureTimer = nil
	r.logger.Info("Canceled isAnyoneHome departure debounce")
}

func (r *ComputedStateRegistry) finishAnyoneHomeDepartureDebounce(provider *ComputedStateProvider) {
	r.anyoneHomeDepartureMu.Lock()
	r.anyoneHomeDepartureTimer = nil
	r.anyoneHomeDepartureMu.Unlock()

	ctx := &ComputeContext{
		manager: r.manager,
		logger:  r.logger.Named(provider.Name),
	}

	value, err := provider.ComputeFunc(ctx)
	if err != nil {
		r.logger.Error("Failed to recompute after isAnyoneHome departure debounce",
			zap.Error(err))
		return
	}
	boolVal, ok := value.(bool)
	if !ok {
		r.logger.Error("Unexpected isAnyoneHome computed type",
			zap.String("type", fmt.Sprintf("%T", value)))
		return
	}
	if boolVal {
		return
	}
	if err := r.manager.SetBool(provider.Name, false); err != nil {
		r.logger.Error("Failed to emit debounced isAnyoneHome departure",
			zap.Error(err))
		return
	}
	if provider.OnComputed != nil {
		provider.OnComputed(false)
	}
}

// Stop stops all providers and cleans up subscriptions
func (r *ComputedStateRegistry) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return
	}

	r.stopAllProviders()
	r.started = false

	r.logger.Info("Computed state registry stopped")
}

// stopAllProviders stops all providers without locking
func (r *ComputedStateRegistry) stopAllProviders() {
	r.cancelAnyoneHomeDepartureDebounce()

	// First, signal all periodic goroutines to stop
	for _, ps := range r.providers {
		if ps.stopChan != nil {
			close(ps.stopChan)
		}
	}

	// Wait for all periodic goroutines to exit
	for _, ps := range r.providers {
		if ps.stoppedChan != nil {
			<-ps.stoppedChan
		}
	}

	// Clean up
	for _, ps := range r.providers {
		ps.stopChan = nil
		ps.stoppedChan = nil

		// Unsubscribe from all dependencies
		for _, sub := range ps.subscriptions {
			sub.Unsubscribe()
		}
		ps.subscriptions = nil
	}
}

// Recalculate forces recomputation of a specific computed state
func (r *ComputedStateRegistry) Recalculate(name string) error {
	r.mu.RLock()
	ps, ok := r.providers[name]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("provider %s not found", name)
	}

	return r.computeAndSet(ps.provider)
}

// RecalculateAll forces recomputation of all computed states
// Respects dependency order
func (r *ComputedStateRegistry) RecalculateAll() error {
	r.mu.RLock()
	order, err := r.topologicalSort()
	r.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to sort providers: %w", err)
	}

	for _, name := range order {
		r.mu.RLock()
		ps := r.providers[name]
		r.mu.RUnlock()

		if err := r.computeAndSet(ps.provider); err != nil {
			return fmt.Errorf("failed to recalculate %s: %w", name, err)
		}
	}

	return nil
}

// GetDependencyGraph returns a map of computed state names to their dependencies
func (r *ComputedStateRegistry) GetDependencyGraph() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	graph := make(map[string][]string)
	for name, ps := range r.providers {
		deps := make([]string, len(ps.provider.Dependencies))
		copy(deps, ps.provider.Dependencies)
		graph[name] = deps
	}
	return graph
}

// GetProviderNames returns the names of all registered providers
func (r *ComputedStateRegistry) GetProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// topologicalSort returns provider names in dependency order
// Providers with no dependencies on other computed states come first
func (r *ComputedStateRegistry) topologicalSort() ([]string, error) {
	// Build adjacency list and in-degree count
	// Only consider dependencies that are also computed states
	inDegree := make(map[string]int)
	dependents := make(map[string][]string)

	// Initialize
	for name := range r.providers {
		inDegree[name] = 0
		dependents[name] = nil
	}

	// Count dependencies on other computed states
	for name, ps := range r.providers {
		for _, dep := range ps.provider.Dependencies {
			if _, isComputed := r.providers[dep]; isComputed {
				inDegree[name]++
				dependents[dep] = append(dependents[dep], name)
			}
		}
	}

	// Kahn's algorithm
	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}

	// Sort initial queue for deterministic order
	sort.Strings(queue)

	var result []string
	for len(queue) > 0 {
		// Pop first element
		name := queue[0]
		queue = queue[1:]
		result = append(result, name)

		// Process dependents
		for _, dependent := range dependents[name] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				// Insert in sorted position for deterministic order
				queue = insertSorted(queue, dependent)
			}
		}
	}

	// Check for cycles
	if len(result) != len(r.providers) {
		return nil, fmt.Errorf("circular dependency detected in computed states")
	}

	return result, nil
}

// insertSorted inserts a string into a sorted slice
func insertSorted(slice []string, s string) []string {
	i := sort.SearchStrings(slice, s)
	slice = append(slice, "")
	copy(slice[i+1:], slice[i:])
	slice[i] = s
	return slice
}
