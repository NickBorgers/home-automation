# Concurrency Lessons - WebSocket & State Management

## Overview

This document explains critical concurrency patterns used in the Go implementation. These patterns emerged from bugs discovered during integration testing and represent important lessons for working with WebSockets and concurrent state management.

**Core Philosophy**: Go encourages "share memory by communicating" rather than "communicate by sharing memory." When shared state is necessary, use proper synchronization primitives. Always test with `-race`.

---

## Lesson 1: WebSocket Writes Must Be Serialized

**Pattern**: All WebSocket writes must be protected by a mutex.

**Why**: The `gorilla/websocket` library is **NOT thread-safe for writes**. Multiple goroutines writing to the same connection will cause:
```
panic: concurrent write to websocket connection
```

**Implementation**:
```go
type Client struct {
    conn    *websocket.Conn
    writeMu sync.Mutex  // Protects all writes to conn
    // ...
}

func (c *Client) sendMessage(msg interface{}) error {
    c.writeMu.Lock()
    err := c.conn.WriteJSON(msg)
    c.writeMu.Unlock()
    return err
}
```

**Where to Apply**:
- `internal/ha/client.go` - All WebSocket write operations
- Any future WebSocket client implementations
- Test mock servers that broadcast to multiple connections

**Tests That Validate This**:
- `TestConcurrentWrites` - 20 goroutines writing simultaneously
- `TestConcurrentReadsAndWrites` - Mixed read/write workload

---

## Lesson 2: Subscription Tracking Requires Per-Handler IDs

**Pattern**: Track individual subscription handlers, not just by entity ID.

**Why**: Multiple handlers can subscribe to the same entity. Unsubscribing one must not affect others.

**Wrong Approach** (causes memory leak):
```go
// ❌ BAD: Deletes ALL handlers for entity
func (c *Client) unsubscribe(entityID string) {
    delete(c.subscribers, entityID)  // Removes all handlers!
}
```

**Correct Approach**:
```go
// ✅ GOOD: Track individual subscriptions
type subscription struct {
    id       string
    entityID string
    handler  func(state)
}

// Store by subscription ID, not entity ID
subscribers map[string]*subscription

func (c *Client) unsubscribe(subID string) {
    delete(c.subscribers, subID)  // Removes only this handler
}
```

**Where to Apply**:
- `internal/ha/client.go` - Subscription management
- `internal/state/manager.go` - State change notifications
- Any pub/sub or event handler system

**Test That Validates This**:
- `TestMultipleSubscribersOnSameEntity` - 3 handlers on same entity, unsubscribe one

---

## Lesson 3: Use RWMutex for Read-Heavy Workloads

**Pattern**: Use `sync.RWMutex` when reads vastly outnumber writes.

**Why**: Allows multiple concurrent readers while still protecting against concurrent writes.

**Implementation**:
```go
type Manager struct {
    cacheMu sync.RWMutex
    cache   map[string]interface{}
}

func (m *Manager) Get(key string) interface{} {
    m.cacheMu.RLock()         // Multiple readers OK
    defer m.cacheMu.RUnlock()
    return m.cache[key]
}

func (m *Manager) Set(key string, val interface{}) {
    m.cacheMu.Lock()          // Exclusive write lock
    defer m.cacheMu.Unlock()
    m.cache[key] = val
}
```

**Where to Apply**:
- `internal/state/manager.go` - State cache access
- Any shared cache or lookup table

---

## Lesson 4: Mock External Services for Concurrency Testing

**Pattern**: Use mock servers instead of real external services for integration tests.

**Why Mock HA Server vs Real Home Assistant**:

| Aspect | Mock Server | Real HA |
|--------|-------------|---------|
| **Isolation** | ✅ No external deps | ❌ Requires infrastructure |
| **Speed** | ✅ <30 seconds | ❌ Minutes + network latency |
| **Repeatability** | ✅ Exact same conditions | ❌ Variable state |
| **Concurrency Testing** | ✅ Can simulate 1000s of ops | ❌ Rate limited |
| **Race Detection** | ✅ `-race` flag works | ❌ Harder to reproduce |
| **CI/CD** | ✅ Runs in Docker | ❌ Needs HA instance |

**When to Use Real HA**:
- Final end-to-end validation
- Compatibility testing with specific HA versions
- Real-world performance benchmarking

**Implementation**:
- See `test/integration/mock_ha_server.go` for reference implementation
- Implements full WebSocket protocol with auth, state, subscriptions
- Can simulate disconnects, delays, and error conditions

---

## Lesson 5: Event Handlers Must Not Block Message Processing

**Pattern**: Call event handlers asynchronously in separate goroutines.

**Why**: Event handlers are invoked from the `receiveMessages()` goroutine. If a handler tries to send a message to HA and wait for a response, it will deadlock because `receiveMessages()` is blocked waiting for the handler to return.

**Deadlock Scenario** (CRITICAL BUG - Fixed in PR #XX):
```go
// ❌ BAD: Synchronous handler call causes deadlock
func (c *Client) handleEvent(msg *Message) {
    for _, entry := range entries {
        entry.handler(...)  // BLOCKS receiveMessages goroutine
        // Handler tries to send message → waits for response
        // But response can never be received because receiveMessages is blocked!
        // → 10 second timeout → error
    }
}
```

**Error Symptoms**:
```
Failed to set solarProductionEnergyLevel: failed to set HA value: timeout waiting for response
```

**Call Chain Leading to Deadlock**:
```
receiveMessages() goroutine:
  → ReadJSON() - reads state change event
  → handleEvent() - processes event
  → handler() - energy manager handler called SYNCHRONOUSLY
    → recalculateSolarProductionLevel()
      → stateManager.SetString()
        → haClient.SetInputText()
          → haClient.CallService()
            → haClient.sendMessage()
              → Waits for response with 10 second timeout
              → BUT receiveMessages() is BLOCKED waiting for handler to return
              → Response can never be received
              → DEADLOCK → timeout → error
```

**Correct Approach**:
```go
// ✅ GOOD: Async handler calls prevent deadlock
func (c *Client) handleEvent(msg *Message) {
    for _, entry := range entries {
        // Run each handler in its own goroutine
        // This allows receiveMessages to continue processing
        go entry.handler(entityID, oldState, newState)
    }
}
```

**Benefits**:
- No blocking of message processing
- Handlers can safely send messages back to HA
- Better performance (parallel handler execution)
- Prevents cascading delays when handlers are slow

**Where to Apply**:
- `internal/ha/client.go` - Event handler invocation
- Any callback/handler system invoked from a message processing loop
- Plugin systems where plugins might need to communicate back

**Test That Validates This**:
- `TestClient_HandleEventBackpressuresHandlers` - Verifies handlers don't block message processing
- Production logs showing timeout errors when handlers were synchronous

**Production Impact**:
- **Before Fix**: Frequent 10-second timeout errors in energy manager
- **After Fix**: No timeouts, all HA updates succeed immediately

---

## Lesson 6: Always Test with Race Detector

**Command**: `go test -race ./...`

**Why**: Race conditions are timing-dependent and may not manifest in normal runs.

**What It Catches**:
- Concurrent map access without locks
- Concurrent writes to shared variables
- Channel races and deadlocks

**Cost**: ~10x slower test execution, but catches critical bugs.

**CI Requirement**: All tests must pass with `-race` flag before merging.

---

## Lesson 7: Message ID Allocation and WebSocket Write Must Be Atomic

**Pattern**: When a protocol requires strictly increasing message IDs, allocate the ID while holding the write lock.

**Why**: Home Assistant's WebSocket API requires message IDs to strictly increase. If ID allocation and writing are protected by different locks, concurrent goroutines can allocate IDs in order but write them out of order.

**Race Condition Scenario** (Fixed in [PR #165](https://github.com/NickBorgersOnLowSecurityNode/home-automation/pull/165)):
```go
// ❌ BAD: ID allocation and write protected by different locks
func (c *Client) CallService(...) error {
    msgID := c.nextMsgID()  // Gets ID 10, releases msgIDMu
    // Context switch! Another goroutine gets ID 11 and writes first
    req := &CallServiceRequest{ID: msgID, ...}
    _, err := c.sendMessage(req)  // Writes ID 10 AFTER ID 11 was sent
    return err
    // HA rejects ID 10: "id_reuse - Identifier values have to increase"
}
```

**Error Symptoms**:
```
Failed to set volume during fade-in: service call failed: HA error: id_reuse - Identifier values have to increase.
```

**Interleaving That Causes the Bug**:
```
Goroutine A: nextMsgID() → gets ID 10, releases msgIDMu
Goroutine B: nextMsgID() → gets ID 11, releases msgIDMu
Goroutine B: sendMessage() → acquires writeMu, writes ID 11
Goroutine A: sendMessage() → acquires writeMu, writes ID 10
HA: Rejects ID 10 (already saw ID 11)
```

**Correct Approach**:
```go
// ✅ GOOD: ID allocation inside write lock ensures ordering
func (c *Client) sendMessage(msg interface{}) (*Message, error) {
    c.writeMu.Lock()

    // Allocate message ID while holding write lock
    msgID := c.nextMsgID()

    // Set the ID on the request
    switch m := msg.(type) {
    case *CallServiceRequest:
        m.ID = msgID
    // ... other request types
    }

    // Write the message
    err := conn.WriteJSON(msg)
    c.writeMu.Unlock()

    // ... handle response
}
```

**Key Insight**: The write lock now protects two operations atomically:
1. Message ID allocation
2. WebSocket write

This guarantees that if goroutine A allocates ID 10, no other goroutine can allocate ID 11 until A has written its message.

**Where to Apply**:
- Any WebSocket or TCP protocol with ordered message IDs
- Request-response protocols where IDs must be unique
- Any system where multiple goroutines share a monotonic counter for network messages

**Test That Validates This**:
- `TestClient_ConcurrentCallService` - 50 concurrent calls, verifies strict ID ordering

**Production Impact**:
- **Before Fix**: Intermittent "id_reuse" errors during speaker fade-in (5+ speakers)
- **After Fix**: All concurrent service calls succeed with strictly ordered IDs

---

## Lesson 8: Configure TCP Keepalive with Syscalls for Dead Connection Detection

**Pattern**: Use syscalls to configure TCP keepalive parameters (`TCP_KEEPIDLE`, `TCP_KEEPINTVL`, `TCP_KEEPCNT`) directly, because Go's `net.Dialer.KeepAlive` is insufficient.

**Why**: Go's `net.Dialer.KeepAlive` only sets the keepalive probe *interval*, but does **NOT** configure `TCP_KEEPIDLE` (time before the first probe). On Linux, `TCP_KEEPIDLE` defaults to **7200 seconds (2 hours)**, meaning dead connections aren't detected for hours even with keepalive "enabled."

**Symptoms**:
- Write timeouts detected at variable intervals (11-56 seconds)
- Connection appears alive but writes block indefinitely
- `net.Dialer.KeepAlive` is set but doesn't help
- OS reports connection is still established even when remote end is dead

**Root Cause**:
```
Go's net.Dialer.KeepAlive:
  - Sets SO_KEEPALIVE=1 (enables keepalive)
  - Sets probe interval (e.g., 30s)
  - Does NOT set TCP_KEEPIDLE (defaults to 7200s = 2 hours!)

Result: First keepalive probe sent after 2 HOURS, not 30 seconds!
```

**Wrong Approach** (partially works):
```go
// ❌ INSUFFICIENT: KeepAlive only sets the interval, not the idle time
d := net.Dialer{
    KeepAlive: 30 * time.Second,  // OS still waits 2 hours before first probe!
}
```

**Correct Approach** (uses syscalls):
```go
// ✅ CORRECT: Configure all TCP keepalive parameters with syscalls
import (
    "syscall"
    "golang.org/x/sys/unix"
)

func setTCPKeepAlive(conn net.Conn, idle, interval time.Duration, count int) error {
    tcpConn := conn.(*net.TCPConn)
    rawConn, _ := tcpConn.SyscallConn()

    rawConn.Control(func(fd uintptr) {
        // Enable TCP keepalive
        syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)

        // Time before first probe (was 7200s, now 10s)
        syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, unix.TCP_KEEPIDLE, int(idle.Seconds()))

        // Interval between probes (was 75s, now 5s)
        syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, unix.TCP_KEEPINTVL, int(interval.Seconds()))

        // Number of failed probes before giving up (was 9, now 3)
        syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, unix.TCP_KEEPCNT, count)
    })
    return nil
}

// In WebSocket dialer:
dialer := websocket.Dialer{
    NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
        conn, err := net.Dial(network, addr)
        if err != nil {
            return nil, err
        }
        // 10s idle + 5s interval × 3 probes = dead connection detected in ~25 seconds
        setTCPKeepAlive(conn, 10*time.Second, 5*time.Second, 3)
        return conn, nil
    },
}
```

**How It Works**:
| Parameter | OS Default | Our Setting | Purpose |
|-----------|------------|-------------|---------|
| `TCP_KEEPIDLE` | 7200s (2h) | 10s | Time before first probe |
| `TCP_KEEPINTVL` | 75s | 5s | Interval between probes |
| `TCP_KEEPCNT` | 9 | 3 | Failed probes before giving up |

**Detection Time**: 10s + (5s × 3) = **25 seconds** to detect dead connection (vs 2+ hours with defaults).

**Layered Defense**:
This project uses multiple mechanisms:
- **TCP keepalive (syscalls)**: Detects dead connections at OS level in ~25 seconds
- **Application pings (15s)**: Keeps Home Assistant's WebSocket session alive
- **Write timeout detection (10s)**: Catches blocked writes and triggers reconnection

**Where to Apply**:
- Any WebSocket or TCP client that needs fast dead connection detection
- Long-running connections where network outages are possible
- Connections through NAT gateways or firewalls with aggressive timeouts
- MQTT, gRPC, or other persistent connections

**Platform Notes**:
- Linux-only: Uses `golang.org/x/sys/unix` for `TCP_KEEPIDLE`, `TCP_KEEPINTVL`, `TCP_KEEPCNT`
- On macOS/Windows, different syscall constants are needed (not implemented here)

**References**:
- [golang/go#62254](https://github.com/golang/go/issues/62254) - Discussion of KeepAlive limitations
- [golang/go#73386](https://github.com/golang/go/issues/73386) - KeepAlive documentation is misleading
- Issue #281, #282, #289 - Investigation and fixes for this project

**Production Impact**:
- **Before Syscall Fix**: Write timeouts every 11-56 seconds, dead connections undetected for hours
- **After Syscall Fix**: Dead connections detected in ~25 seconds, stable reconnection

---

## Lesson 9: Cancel Concurrent Goroutines Before Starting New Operations

**Pattern**: Use `context.Context` to cancel active goroutines before starting new operations that would conflict.

**Why**: When multiple goroutines operate on the same resource, a new operation may conflict with an active one, causing:
- Resource contention (e.g., volume jumping when multiple fade-ins run)
- False state detection (e.g., old goroutine sees new operation's state as "unexpected")
- Wasted work (old operation continues when its result is no longer needed)

**Problem Scenario** (Fixed in [PR #458](https://github.com/NickBorgersOnLowSecurityNode/home-automation/pull/458)):
```go
// ❌ BAD: No cancellation - concurrent fade-ins cause chaos
func (m *Manager) executePlayback(...) {
    for _, speaker := range speakers {
        go m.fadeInSpeaker(speaker, targetVolume)  // No way to stop old fade-ins!
    }
}

// When user changes music, old fade-ins are still running:
// - Old fade-in sees volume at 0 (set by new fade-in) → "human override detected!"
// - Old and new fade-ins fight for volume → volume jumps wildly
```

**Error Symptoms**:
```
Aborting fade-in: apparent human override - volume changed unexpectedly
```

**Correct Approach**:
```go
// ✅ GOOD: Track and cancel goroutines with context
type Manager struct {
    fadeInContexts   map[string]context.CancelFunc
    fadeInContextsMu sync.Mutex
}

func (m *Manager) cancelAllFadeIns() {
    m.fadeInContextsMu.Lock()
    defer m.fadeInContextsMu.Unlock()
    for _, cancel := range m.fadeInContexts {
        cancel()
    }
    m.fadeInContexts = make(map[string]context.CancelFunc)
}

func (m *Manager) startFadeInWithContext(entityID string) context.Context {
    m.fadeInContextsMu.Lock()
    defer m.fadeInContextsMu.Unlock()

    // Cancel existing fade-in for this speaker
    if cancel, exists := m.fadeInContexts[entityID]; exists {
        cancel()
    }

    ctx, cancel := context.WithCancel(context.Background())
    m.fadeInContexts[entityID] = cancel
    return ctx
}

func (m *Manager) executePlayback(...) {
    m.cancelAllFadeIns()  // Stop all old fade-ins first!
    for _, speaker := range speakers {
        ctx := m.startFadeInWithContext(speaker)
        go m.fadeInSpeaker(ctx, speaker, targetVolume)
    }
}

func (m *Manager) fadeInSpeaker(ctx context.Context, speaker string, target int) {
    defer m.unregisterFadeIn(speaker)
    for volume := 0; volume <= target; volume++ {
        select {
        case <-ctx.Done():
            return  // Cancelled - exit gracefully
        default:
        }
        // ... do fade-in step
    }
}
```

**Key Elements**:
1. **Map of cancel functions**: Track active goroutines by resource identifier
2. **Mutex protection**: Context map is shared state, needs synchronization
3. **Cancel before start**: Always cancel old operations before starting new ones
4. **Graceful exit**: Goroutines check `ctx.Done()` and return cleanly
5. **Cleanup on completion**: Unregister context when goroutine finishes

**Where to Apply**:
- Any long-running goroutine that operates on a shared resource
- Operations that can be superseded by newer requests
- Retry/polling loops that should stop when conditions change
- Background tasks that should abort when parent operation completes

**Test That Validates This**:
- `TestFadeInCancellation_NewPlaybackCancelsActiveFadeIns`
- `TestFadeInCancellation_CancelledBeforeStart`

**Production Impact**:
- **Before Fix**: Volume jumping during music changes, false "human override" detection
- **After Fix**: Clean handoff between playback sequences, no spurious aborts

---

## Lesson 10: Use Buffered Channels to Prevent Goroutine Leaks

**Pattern**: When a goroutine sends to a channel that might not be received (e.g., timeout scenarios), use a buffered channel.

**Why**: If a receiver times out and stops waiting, an unbuffered send will block forever, leaking the goroutine and its resources.

**Leak Scenario**:
```go
// ❌ BAD: Goroutine leaks if timeout occurs
func fetchWithTimeout(ctx context.Context) (Result, error) {
    ch := make(chan Result)  // Unbuffered!
    go func() {
        result := slowOperation()
        ch <- result  // BLOCKS FOREVER if ctx times out
    }()

    select {
    case result := <-ch:
        return result, nil
    case <-ctx.Done():
        return Result{}, ctx.Err()  // Goroutine is now leaked!
    }
}
```

**Correct Approach**:
```go
// ✅ GOOD: Buffered channel allows goroutine to complete
func fetchWithTimeout(ctx context.Context) (Result, error) {
    ch := make(chan Result, 1)  // Buffer of 1
    go func() {
        result := slowOperation()
        ch <- result  // Never blocks - buffer absorbs the send
    }()

    select {
    case result := <-ch:
        return result, nil
    case <-ctx.Done():
        return Result{}, ctx.Err()  // Goroutine will finish and exit cleanly
    }
}
```

**Key Insight**: A buffer of 1 ensures the goroutine can always complete its send, even if no one is listening. The channel and its contents will be garbage collected when no references remain.

**Where to Apply**:
- Any pattern where a goroutine sends a result and the receiver might time out
- Response channels in request/response patterns
- Fire-and-forget notification channels

**Real-World Example in This Project**:
```go
// internal/ha/client.go - Response channel for WebSocket messages
respChan := make(chan Message, 1)  // Buffered to prevent leak
```

---

## Lesson 11: Maintain Consistent Lock Ordering to Prevent Deadlocks

**Pattern**: When multiple locks must be held simultaneously, always acquire them in the same order across all goroutines.

**Why**: Inconsistent lock ordering is the most common cause of deadlocks. If goroutine A holds lock X and waits for lock Y, while goroutine B holds lock Y and waits for lock X, neither can proceed.

**General Deadlock Scenario**:
```go
// ❌ BAD: Inconsistent lock ordering causes deadlock
// Goroutine A
func updateUserProfile() {
    userMu.Lock()
    defer userMu.Unlock()
    profileMu.Lock()      // Waits for profileMu
    defer profileMu.Unlock()
    // ...
}

// Goroutine B
func updateProfileSettings() {
    profileMu.Lock()
    defer profileMu.Unlock()
    userMu.Lock()         // Waits for userMu - DEADLOCK!
    defer userMu.Unlock()
    // ...
}
```

**Real-World Example** (Fixed in [Issue #552](https://github.com/NickBorgersOnLowSecurityNode/home-automation/issues/552)):

The `MockClock` implementation had a lock ordering violation between `mockTimer.Reset()` and `MockClock.Advance()`:

```go
// ❌ BAD: Inconsistent lock ordering causes deadlock
// Reset acquires: timer.mu → clock.mu
func (t *mockTimer) Reset(d time.Duration) bool {
    t.mu.Lock()           // 1. timer.mu FIRST
    defer t.mu.Unlock()

    t.clock.mu.Lock()     // 2. clock.mu SECOND
    t.deadline = t.clock.current.Add(d)
    t.clock.mu.Unlock()
    return wasActive
}

// Advance acquires: clock.mu → timer.mu
func (c *MockClock) Advance(d time.Duration) {
    c.mu.Lock()           // 1. clock.mu FIRST
    for _, timer := range c.timers {
        timer.mu.Lock()   // 2. timer.mu SECOND
        // ...
        timer.mu.Unlock()
    }
    c.mu.Unlock()
}
```

**Interleaving That Causes Deadlock**:
```
Goroutine A (Reset):     Goroutine B (Advance):
─────────────────────    ────────────────────────
timer.mu.Lock() ✓        clock.mu.Lock() ✓
clock.mu.Lock() ← BLOCKED timer.mu.Lock() ← BLOCKED

                 DEADLOCK!
```

**Correct Approach**:
```go
// ✅ GOOD: Consistent lock ordering (clock.mu always before timer.mu)
func (t *mockTimer) Reset(d time.Duration) bool {
    // Acquire clock.mu first (consistent with Advance)
    t.clock.mu.Lock()
    defer t.clock.mu.Unlock()

    t.mu.Lock()
    defer t.mu.Unlock()

    wasActive := !t.stopped
    t.stopped = false
    t.deadline = t.clock.current.Add(d)

    if !wasActive {
        t.clock.timers = append(t.clock.timers, t)
    }
    return wasActive
}
```

**How to Identify Lock Ordering Issues**:
1. Document which locks each method acquires
2. Draw an "acquisition order" diagram for each method
3. Check that all methods follow the same order
4. Look for methods that acquire locks on "child" objects (like timers owned by a clock)

**Choosing a Lock Order**:
- **Hierarchical**: Parent before child (e.g., `clock.mu` before `timer.mu`)
- **Alphabetical**: When no hierarchy exists, use alphabetical as a tiebreaker
- **Document it**: Add comments like `// Lock ordering: clock.mu before timer.mu`

**Where to Apply**:
- Any struct that owns objects with their own mutexes
- Bi-directional references between objects (parent ↔ child)
- Nested data structures with concurrent access
- Test mocks that mirror production lock patterns

**Best Practices**:
1. **Document lock ordering** in struct comments or package docs
2. **Use hierarchical ordering**: Higher-level locks before lower-level
3. **Prefer single locks**: If possible, use one lock instead of multiple
4. **Consider lock-free patterns**: Channels, atomic operations, or immutable data
5. **Use go-deadlock for debugging**: Drop-in replacement that detects ordering violations

**Detecting Lock Order Violations**:
```go
// For debugging, replace sync.Mutex with go-deadlock
import "github.com/sasha-s/go-deadlock"
var mu deadlock.Mutex  // Reports when lock ordering is violated
```

**Test That Validates This**:
- `TestMockClock_ConcurrentResetAndAdvance` - Concurrent Reset/Advance operations with timeout-based deadlock detection

**Risk Assessment**:
- **Severity**: Medium (affected test code, not production)
- **Impact**: Test flakiness or hangs with concurrent timer operations
- **Detection**: Race detector may not catch this; requires concurrent stress tests

---

## Common Pitfalls to Avoid

### 1. Forgetting to Lock Before Map Access
```go
// ❌ BAD: Race condition
m.cache[key] = value

// ✅ GOOD: Protected access
m.mu.Lock()
m.cache[key] = value
m.mu.Unlock()
```

### 2. Holding Locks Across Network Calls
```go
// ❌ BAD: Lock held during slow I/O
m.mu.Lock()
result := callAPI()  // May take seconds!
m.cache[key] = result
m.mu.Unlock()

// ✅ GOOD: Release lock before I/O
result := callAPI()
m.mu.Lock()
m.cache[key] = result
m.mu.Unlock()
```

### 3. Not Closing Channels in Cleanup
```go
// ❌ BAD: Goroutine leak
func subscribe() chan Event {
    ch := make(chan Event)
    go sendEvents(ch)  // Never stops!
    return ch
}

// ✅ GOOD: Proper cleanup
type Subscription struct {
    events chan Event
    done   chan struct{}
}

func (s *Subscription) Close() {
    close(s.done)  // Signals goroutine to stop
}
```

### 4. Using Defer in Loops
```go
// ❌ BAD: Defer only runs when function returns, not per iteration
func processItems(items []Item) {
    for _, item := range items {
        mu.Lock()
        defer mu.Unlock()  // WRONG: Defers stack up, unlock happens at function end
        process(item)
    }
}

// ✅ GOOD: Explicit unlock or wrap in function
func processItems(items []Item) {
    for _, item := range items {
        func() {
            mu.Lock()
            defer mu.Unlock()  // Runs at end of anonymous func
            process(item)
        }()
    }
}
```

### 5. Copying Mutexes and Sync Primitives
```go
// ❌ BAD: Mutex is copied - each copy has independent state
func processCopy(m Manager) {  // Value receiver copies the mutex!
    m.mu.Lock()  // Locks a COPY, not the original
    // ...
}

// ✅ GOOD: Always pass sync primitives by pointer
func processPtr(m *Manager) {
    m.mu.Lock()  // Locks the original
    // ...
}
```

### 6. Forgetting to Check ctx.Done() in Long Loops
```go
// ❌ BAD: Loop ignores cancellation
func pollForever(ctx context.Context) {
    for {
        doWork()
        time.Sleep(time.Second)  // Doesn't respect context!
    }
}

// ✅ GOOD: Check context and use timer
func pollForever(ctx context.Context) {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return  // Exit on cancellation
        case <-ticker.C:
            doWork()
        }
    }
}
```

### 7. Reading and Writing Shared Variables Without Synchronization
```go
// ❌ BAD: Data race on 'running' flag
type Worker struct {
    running bool
}

func (w *Worker) Start() { w.running = true }
func (w *Worker) IsRunning() bool { return w.running }

// ✅ GOOD: Use atomic or mutex
type Worker struct {
    running atomic.Bool
}

func (w *Worker) Start() { w.running.Store(true) }
func (w *Worker) IsRunning() bool { return w.running.Load() }
```

---

## Debugging Deadlocks and Race Conditions

When concurrency bugs occur, use these techniques to diagnose them:

### 1. Race Detector
```bash
go test -race ./...
go build -race ./cmd/main.go && ./main
```
The race detector instruments memory accesses and reports concurrent access without synchronization. It catches ~95% of data races but has ~10x performance overhead.

### 2. Stack Traces with SIGQUIT
Send SIGQUIT to a running process to dump all goroutine stacks:
```bash
kill -QUIT <pid>
# Or press Ctrl+\ in the terminal
```
Look for multiple goroutines blocked on the same mutex or channel.

### 3. pprof Goroutine Dumps
Add pprof to your application:
```go
import _ "net/http/pprof"
go http.ListenAndServe("localhost:6060", nil)
```
Then access `http://localhost:6060/debug/pprof/goroutine?debug=1` to see all goroutine stacks with blocking reasons.

### 4. go-deadlock Library
For lock ordering violations, use [go-deadlock](https://github.com/sasha-s/go-deadlock) as a drop-in replacement during development:
```go
import "github.com/sasha-s/go-deadlock"
var mu deadlock.Mutex  // Reports potential deadlocks
```
It detects when lock ordering is violated and logs warnings before actual deadlocks occur.

### 5. Runtime Detection Limitations
Go's runtime only detects deadlocks when **all** goroutines are blocked. If even one goroutine is running (like an HTTP server), partial deadlocks go undetected. The techniques above help find these "silent" deadlocks.

---

## Key Takeaways

1. **WebSocket writes need mutex protection** - gorilla/websocket is not thread-safe
2. **Track subscriptions individually** - Multiple handlers per entity must be supported
3. **Use RWMutex for caches** - Better concurrency for read-heavy workloads
4. **Mock external services** - Faster, more reliable concurrency testing
5. **Event handlers must be async** - Prevents deadlocks when handlers send messages
6. **Always test with -race** - Catches bugs you can't see in normal runs
7. **Message ID allocation and write must be atomic** - Prevents out-of-order IDs
8. **Configure TCP keepalive with syscalls** - Go's net.Dialer.KeepAlive is insufficient for fast dead connection detection
9. **Cancel concurrent goroutines before new operations** - Use context.Context to stop conflicting goroutines
10. **Buffer channels in timeout patterns** - Prevents goroutine leaks when receivers time out
11. **Maintain consistent lock ordering** - Prevents deadlocks when multiple locks are needed
12. **Use errgroup for structured concurrency** - Provides bounded parallelism, error propagation, and cleaner code than manual WaitGroup

---

## Lesson 12: Use errgroup for Structured Concurrency with Bounded Parallelism

**Pattern**: Use `golang.org/x/sync/errgroup` for coordinating groups of goroutines that should complete together.

**Why**: errgroup provides several advantages over manual `sync.WaitGroup` management:
- **Automatic error propagation** - First error is captured and returned by `Wait()`
- **Bounded concurrency** - `SetLimit(n)` controls parallelism (useful for network operations)
- **Cleaner code** - No manual `Add(1)` / `Done()` bookkeeping
- **Context integration** - `errgroup.WithContext()` cancels remaining goroutines on first error

**Implementation**:
```go
import "golang.org/x/sync/errgroup"

func (m *Manager) buildSpeakerGroupAsync(participants []ParticipantWithVolume, leadEntityID string) {
    // Use errgroup for structured concurrency with bounded parallelism
    g := new(errgroup.Group)
    g.SetLimit(3) // Limit concurrent speaker joins to reduce IGMP congestion

    for i := 1; i < len(participants); i++ {
        p := participants[i]
        staggerDelay := time.Duration(i-1) * asyncJoinStaggerDelay

        g.Go(func() error {
            if staggerDelay > 0 {
                time.Sleep(staggerDelay)
            }
            return m.joinSpeakerWithRetry(p, leadEntityID)
        })
    }

    // Wait for all goroutines to complete
    if err := g.Wait(); err != nil {
        m.logger.Debug("Some speakers failed to join", zap.Error(err))
    }
}
```

**When to Use errgroup vs WaitGroup**:

| Scenario | Use |
|----------|-----|
| Batch of operations that should complete together | `errgroup` |
| Need bounded parallelism (rate limiting) | `errgroup.SetLimit(n)` |
| Long-running goroutines signaling startup completion | `sync.WaitGroup` |
| Simple synchronization with no error handling | `sync.WaitGroup` |
| Cancel remaining work on first error | `errgroup.WithContext()` |

**Where Applied**:
- `internal/plugins/music/fadein.go` - Async speaker group building with concurrency limit

**Where WaitGroup is Still Appropriate**:
- `internal/plugins/energy/manager.go` - Startup synchronization for long-running goroutines that signal initial work completion but continue running
- `internal/plugins/music/manager.go` - Test synchronization for rotation syncs

**References**:
- [errgroup package documentation](https://pkg.go.dev/golang.org/x/sync/errgroup)
- Issue #553 - errgroup adoption

---

## External Resources

- [Effective Go - Concurrency](https://go.dev/doc/effective_go#concurrency) - Official Go concurrency guide
- [Go Concurrency Patterns](https://go.dev/blog/pipelines) - Pipelines and cancellation
- [go-deadlock](https://github.com/sasha-s/go-deadlock) - Runtime deadlock detection
- [errgroup package](https://pkg.go.dev/golang.org/x/sync/errgroup) - Structured concurrency
- [Common Concurrent Programming Mistakes](https://go101.org/article/concurrent-common-mistakes.html) - Comprehensive pitfall list
- [Goroutine Leaks - The Forgotten Sender](https://www.ardanlabs.com/blog/2018/11/goroutine-leaks-the-forgotten-sender.html) - Ardan Labs deep dive

---

## References

- Integration tests: `test/integration/integration_test.go`
- HA client: `internal/ha/client.go`
- State manager: `internal/state/manager.go`
- Mock server: `test/integration/mock_ha_server.go`

**Last Updated**: 2026-02-01
**Test Status**: All 11/11 integration tests passing with `-race` flag

---

## Change Log

### 2026-02-01
- **Added Lesson 12**: Use errgroup for Structured Concurrency with Bounded Parallelism
  - Adopted `golang.org/x/sync/errgroup` for managing groups of goroutines (Issue #553)
  - Provides bounded parallelism via `SetLimit(n)`, error propagation, and cleaner code
  - Applied to `internal/plugins/music/fadein.go` for async speaker group building
  - Documented when to prefer errgroup vs WaitGroup
- **Added Lesson 10**: Use Buffered Channels to Prevent Goroutine Leaks
  - Documents pattern for using buffered channels in timeout scenarios
  - Prevents goroutine leaks when receivers time out or cancel
- **Enhanced Lesson 11**: Maintain Consistent Lock Ordering to Prevent Deadlocks
  - Added real-world example: lock ordering violation in `MockClock` (Issue #552)
  - Documents how `mockTimer.Reset()` and `MockClock.Advance()` caused deadlock
  - Pattern: Always acquire parent locks (clock.mu) before child locks (timer.mu)
  - Added comprehensive tests for MockClock timer operations
  - References go-deadlock library for detection
- **Added new Common Pitfalls**:
  - Using defer in loops (defers stack up)
  - Copying mutexes and sync primitives
  - Forgetting to check ctx.Done() in long loops
  - Reading/writing shared variables without synchronization
- **Added Debugging Deadlocks section**: Tools and techniques (pprof, SIGQUIT, go-deadlock)
- **Added External Resources section**: Links to authoritative Go concurrency resources
- **Removed arbitrary performance numbers** from Lesson 3 (were misleading without benchmarks)

### 2026-01-10
- **Added Lesson 9**: Cancel Concurrent Goroutines Before Starting New Operations
  - Documents pattern for using `context.Context` to cancel conflicting goroutines (Issue #457, PR #458)
  - Pattern: Track cancel functions in map, cancel before starting new operations, check `ctx.Done()` in goroutines
  - Fixes volume jumping and false human-override detection in music fade-ins

### 2026-01-05
- **Simplification**: WebSocket Connection Management (Issue #405)
  - Removed `sendWebSocketPings()` - redundant with application-level JSON pings
  - Added `managedConn` wrapper to encapsulate nil checks and connection lifecycle
  - Ping goroutines now receive explicit context and connection, eliminating stale reference races
  - Consolidated `metricsMu` into `connMu`, reducing mutex count in Client struct from 10 to 9
  - Layered keepalive is now 2 mechanisms (TCP keepalive + application pings) instead of 3

### 2026-01-02
- **Updated Lesson 8**: Configure TCP Keepalive with Syscalls for Dead Connection Detection
  - Go's `net.Dialer.KeepAlive` is insufficient - doesn't set `TCP_KEEPIDLE` (defaults to 2 hours)
  - New pattern: Use syscalls to set `TCP_KEEPIDLE`, `TCP_KEEPINTVL`, `TCP_KEEPCNT` directly
  - Dead connections now detected in ~25 seconds instead of hours (Issue #289)
  - Added connection health metrics: disconnect count, write timeouts, connection duration

### 2026-01-01
- **Added Lesson 8**: Enable TCP Keepalive for WebSocket Connections Through Proxies
  - Fixes 60-second WebSocket timeout caused by reverse proxy idle detection (Issue #281, #282)
  - Pattern: Use custom `websocket.Dialer` with `net.Dialer{KeepAlive: 30s}`
  - TCP keepalive probes prevent proxy idle timeouts at the network layer

### 2025-12-28
- **Added Lesson 7**: Message ID Allocation and WebSocket Write Must Be Atomic
  - Documents fix for race condition causing "id_reuse" errors (PR #165)
  - Pattern: allocate message IDs while holding write lock

### 2025-11-16
- **Added Lesson 5**: Event Handlers Must Not Block Message Processing
  - Fixed critical deadlock bug causing 10-second timeouts in production
  - Changed handlers from synchronous to asynchronous execution
  - Updated `TestClient_HandleEventBackpressuresHandlers` to verify async behavior
  - See `internal/ha/client.go:356-360` for implementation
