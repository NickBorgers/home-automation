package integration

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testToken = "test_token_12345"
	// Use port 0 to let OS assign a free port, avoiding port conflicts between tests
	testAddr = "localhost:0"
)

func setupTest(t *testing.T) (*MockHAServer, *ha.Client, *state.Manager, func()) {
	// Create logger
	logger := testlogger.New()

	// Start mock HA server with dynamic port allocation (port 0)
	server := NewMockHAServer(testAddr, testToken)
	server.InitializeStates()

	err := server.Start()
	require.NoError(t, err)

	// Get the actual address after dynamic port allocation
	actualAddr := server.Addr()

	// Create and connect client using the actual address
	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", actualAddr), testToken, logger)
	err = client.Connect()
	require.NoError(t, err)

	// Create state manager
	manager := state.NewManager(client, logger, false)
	err = manager.SyncFromHA()
	require.NoError(t, err)

	// Cleanup function
	cleanup := func() {
		client.Disconnect()
		server.Stop()
	}

	return server, client, manager, cleanup
}

// TestBasicConnection tests basic connection and sync
func TestBasicConnection(t *testing.T) {
	t.Parallel()
	server, client, manager, cleanup := setupTest(t)
	defer cleanup()

	t.Run("connection status", func(t *testing.T) {
		assert.True(t, client.IsConnected())
	})

	t.Run("initial sync", func(t *testing.T) {
		// Check boolean
		isHome, err := manager.GetBool("isNickHome")
		assert.NoError(t, err)
		assert.False(t, isHome)

		// Check string
		phase, err := manager.GetString("dayPhase")
		assert.NoError(t, err)
		assert.Equal(t, "morning", phase)

		// Check number
		alarmTime, err := manager.GetNumber("alarmTime")
		assert.NoError(t, err)
		assert.Equal(t, 0.0, alarmTime)
	})

	t.Run("state update from manager", func(t *testing.T) {
		err := manager.SetBool("isExpectingSomeone", true)
		assert.NoError(t, err)

		// Verify in manager
		value, err := manager.GetBool("isExpectingSomeone")
		assert.NoError(t, err)
		assert.True(t, value)

		// Wait for propagation to server
		waitForServerState(t, server, "input_boolean.expecting_someone", "on", "state should propagate to server")
	})
}

// TestStateChangeSubscription tests subscription to state changes
func TestStateChangeSubscription(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupTest(t)
	defer cleanup()

	changeCount := 0
	var lastOld, lastNew interface{}
	var mu sync.Mutex

	sub, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
		mu.Lock()
		defer mu.Unlock()
		changeCount++
		lastOld = oldValue
		lastNew = newValue
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Trigger change from server
	server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})

	// Wait for state to propagate
	waitForBoolState(t, manager, "isNickHome", true, "isNickHome should become true")

	mu.Lock()
	assert.Equal(t, 1, changeCount)
	assert.False(t, lastOld.(bool))
	assert.True(t, lastNew.(bool))
	mu.Unlock()
}

// TestConcurrentReads tests concurrent read operations
func TestConcurrentReads(t *testing.T) {
	t.Parallel()
	_, _, manager, cleanup := setupTest(t)
	defer cleanup()

	const numGoroutines = 50
	const readsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < readsPerGoroutine; j++ {
				manager.GetBool("isNickHome")
				manager.GetString("dayPhase")
				manager.GetNumber("alarmTime")
			}
		}()
	}

	wg.Wait()
	// If we get here without hanging, no deadlock
}

// TestConcurrentWrites tests concurrent write operations
func TestConcurrentWrites(t *testing.T) {
	t.Parallel()
	_, _, manager, cleanup := setupTest(t)
	defer cleanup()

	const numGoroutines = 10
	const writesPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				manager.SetBool("isExpectingSomeone", j%2 == 0)
				manager.SetString("dayPhase", fmt.Sprintf("phase_%d", j%3))
			}
		}(i)
	}

	wg.Wait()
	// If we get here without hanging, no deadlock
}

// TestConcurrentReadsAndWrites tests mixed read/write operations
func TestConcurrentReadsAndWrites(t *testing.T) {
	t.Parallel()
	_, _, manager, cleanup := setupTest(t)
	defer cleanup()

	const duration = 1 * time.Second
	done := make(chan bool)

	// Readers
	for i := 0; i < 10; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					manager.GetBool("isNickHome")
					manager.GetString("dayPhase")
					time.Sleep(1 * time.Millisecond)
				}
			}
		}()
	}

	// Writers
	for i := 0; i < 5; i++ {
		go func(id int) {
			count := 0
			for {
				select {
				case <-done:
					return
				default:
					manager.SetBool("isExpectingSomeone", count%2 == 0)
					count++
					time.Sleep(5 * time.Millisecond)
				}
			}
		}(i)
	}

	// Run for duration
	time.Sleep(duration)
	close(done)

	// Wait a bit for goroutines to exit
	time.Sleep(100 * time.Millisecond)
}

// TestSubscriptionWithConcurrentWrites tests the deadlock scenario
func TestSubscriptionWithConcurrentWrites(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupTest(t)
	defer cleanup()

	const duration = 1 * time.Second
	done := make(chan bool)

	changeCount := 0
	var countMu sync.Mutex

	// Subscribe to state changes and trigger more writes in callback
	sub, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
		countMu.Lock()
		changeCount++
		countMu.Unlock()

		// This could trigger deadlock if manager doesn't release locks properly
		manager.GetBool("isCarolineHome")
		manager.SetBool("isExpectingSomeone", newValue.(bool))
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Trigger rapid state changes from server
	go func() {
		count := 0
		for {
			select {
			case <-done:
				return
			default:
				state := "off"
				if count%2 == 0 {
					state = "on"
				}
				server.SetState("input_boolean.nick_home", state, map[string]interface{}{})
				count++
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()

	// Also trigger writes from manager side
	go func() {
		count := 0
		for {
			select {
			case <-done:
				return
			default:
				manager.SetBool("isNickHome", count%2 == 0)
				count++
				time.Sleep(75 * time.Millisecond)
			}
		}
	}()

	// Run for duration
	time.Sleep(duration)
	close(done)

	// Wait for things to settle
	time.Sleep(100 * time.Millisecond)

	countMu.Lock()
	t.Logf("Total state changes observed: %d", changeCount)
	assert.Greater(t, changeCount, 0)
	countMu.Unlock()
}

// TestMultipleSubscribersOnSameEntity tests multiple subscribers to same entity
func TestMultipleSubscribersOnSameEntity(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupTest(t)
	defer cleanup()

	count1, count2, count3 := 0, 0, 0
	var mu1, mu2, mu3 sync.Mutex

	// Subscribe three times to the same entity
	sub1, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
		mu1.Lock()
		count1++
		mu1.Unlock()
	})
	require.NoError(t, err)
	defer sub1.Unsubscribe()

	sub2, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
		mu2.Lock()
		count2++
		mu2.Unlock()
	})
	require.NoError(t, err)
	defer sub2.Unsubscribe()

	sub3, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
		mu3.Lock()
		count3++
		mu3.Unlock()
	})
	require.NoError(t, err)
	defer sub3.Unsubscribe()

	// Trigger change
	server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})

	// Wait for all three subscribers to be notified
	waitForCondition(t, func() bool {
		mu1.Lock()
		defer mu1.Unlock()
		mu2.Lock()
		defer mu2.Unlock()
		mu3.Lock()
		defer mu3.Unlock()
		return count1 >= 1 && count2 >= 1 && count3 >= 1
	}, "all 3 subscribers should be called")

	mu1.Lock()
	mu2.Lock()
	mu3.Lock()

	assert.Equal(t, 1, count1, "Subscriber 1 should be called")
	assert.Equal(t, 1, count2, "Subscriber 2 should be called")
	assert.Equal(t, 1, count3, "Subscriber 3 should be called")

	mu3.Unlock()
	mu2.Unlock()
	mu1.Unlock()

	// Now unsubscribe one and verify others still work
	sub2.Unsubscribe()

	server.SetState("input_boolean.nick_home", "off", map[string]interface{}{})

	// Wait for subscribers 1 and 3 to receive second notification (sub2 is unsubscribed)
	waitForCondition(t, func() bool {
		mu1.Lock()
		defer mu1.Unlock()
		mu3.Lock()
		defer mu3.Unlock()
		return count1 >= 2 && count3 >= 2
	}, "subscribers 1 and 3 should receive second notification")

	mu1.Lock()
	mu2.Lock()
	mu3.Lock()

	// Verify per-subscription ID handling: only subscriber 2 should be unsubscribed
	// Expected: count1=2, count2=1, count3=2
	t.Logf("After unsubscribe one: count1=%d, count2=%d, count3=%d", count1, count2, count3)

	// Verify correct unsubscribe behavior (only removes specific subscription)
	assert.Equal(t, 2, count1, "Subscriber 1 should still be called after sub2 unsubscribe")
	assert.Equal(t, 1, count2, "Subscriber 2 should NOT be called after unsubscribe")
	assert.Equal(t, 2, count3, "Subscriber 3 should still be called after sub2 unsubscribe")

	mu3.Unlock()
	mu2.Unlock()
	mu1.Unlock()
}

// TestCompareAndSwapRaceCondition tests atomic operations under load
func TestCompareAndSwapRaceCondition(t *testing.T) {
	t.Parallel()
	_, _, manager, cleanup := setupTest(t)
	defer cleanup()

	const numGoroutines = 20
	successCount := 0
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Multiple goroutines try to CAS the same variable
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()

			// Try to swap from false to true
			swapped, err := manager.CompareAndSwapBool("isFadeOutInProgress", false, true)
			if err == nil && swapped {
				mu.Lock()
				successCount++
				mu.Unlock()

				// Do some "work"
				time.Sleep(10 * time.Millisecond)

				// Swap back
				manager.SetBool("isFadeOutInProgress", false)
			}
		}()
	}

	wg.Wait()

	// Only one goroutine should have succeeded at a time
	// But we set it back to false each time, so multiple could succeed sequentially
	mu.Lock()
	t.Logf("Successful CAS operations: %d", successCount)
	assert.Greater(t, successCount, 0)
	mu.Unlock()
}

// TestReconnection tests client reconnection behavior
func TestReconnection(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Start server with dynamic port allocation
	server := NewMockHAServer(testAddr, testToken)
	server.InitializeStates()
	err := server.Start()
	require.NoError(t, err)

	// Capture the dynamically assigned address for reconnection
	serverAddr := server.Addr()

	// Connect client using the dynamically assigned address
	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", serverAddr), testToken, logger)
	err = client.Connect()
	require.NoError(t, err)

	assert.True(t, client.IsConnected())

	// Stop server to force disconnect
	t.Log("Stopping server to trigger reconnection...")
	server.Stop()

	// Wait for disconnect detection
	time.Sleep(1 * time.Second)

	// Restart server on the same address
	t.Log("Restarting server...")
	server = NewMockHAServer(serverAddr, testToken)
	server.InitializeStates()
	err = server.Start()
	require.NoError(t, err)
	defer server.Stop()

	// Wait for reconnection (with timeout)
	t.Log("Waiting for reconnection...")
	reconnected := false
	for i := 0; i < 40; i++ { // 40 seconds max
		if client.IsConnected() {
			reconnected = true
			break
		}
		time.Sleep(1 * time.Second)
	}

	assert.True(t, reconnected, "Client should reconnect automatically")

	client.Disconnect()
}

// TestReconnectionMessageIDReset tests that message IDs are reset after reconnection
// This prevents the "id_reuse - Identifier values have to increase" error from HA
func TestReconnectionMessageIDReset(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Start server with dynamic port allocation
	server := NewMockHAServer(testAddr, testToken)
	server.InitializeStates()
	err := server.Start()
	require.NoError(t, err)

	// Capture the dynamically assigned address for reconnection
	serverAddr := server.Addr()

	// Connect client using the dynamically assigned address
	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", serverAddr), testToken, logger)
	err = client.Connect()
	require.NoError(t, err)
	require.True(t, client.IsConnected())

	// Send several messages to increment message ID counter
	t.Log("Sending initial messages to increment message ID counter...")
	for i := 0; i < 10; i++ {
		err = client.SetInputBoolean("nick_home", i%2 == 0)
		require.NoError(t, err, "Message %d should succeed before reconnection", i)
	}

	// Stop server to force disconnect
	t.Log("Stopping server to trigger reconnection...")
	server.Stop()

	// Wait for disconnect detection
	time.Sleep(1 * time.Second)

	// Restart server on the same address (this simulates a new HA session that expects message IDs from 1)
	t.Log("Restarting server (new session)...")
	server = NewMockHAServer(serverAddr, testToken)
	server.InitializeStates()
	err = server.Start()
	require.NoError(t, err)
	defer server.Stop()

	// Wait for reconnection (with timeout)
	t.Log("Waiting for reconnection...")
	reconnected := false
	for i := 0; i < 40; i++ { // 40 seconds max
		if client.IsConnected() {
			reconnected = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	require.True(t, reconnected, "Client should reconnect automatically")

	// CRITICAL TEST: Send messages after reconnection
	// If message IDs are NOT reset, HA will reject these with "id_reuse" error
	// because the new session expects IDs to start from 1, not continue from 11+
	t.Log("Sending messages after reconnection (testing message ID reset)...")
	for i := 0; i < 10; i++ {
		err = client.SetInputBoolean("nick_home", i%2 == 1)
		assert.NoError(t, err, "Message %d should succeed after reconnection (message IDs should be reset)", i)
		if err != nil {
			t.Logf("ERROR: Failed to send message after reconnection: %v", err)
			t.Log("This likely means message IDs were NOT reset on reconnection")
			break
		}
	}

	// Also test with different service types to ensure ID reset works for all message types
	t.Log("Testing different service types after reconnection...")
	err = client.SetInputNumber("test_number", 42.5)
	assert.NoError(t, err, "SetInputNumber should work after reconnection")

	err = client.SetInputText("test_text", "reconnection_test")
	assert.NoError(t, err, "SetInputText should work after reconnection")

	// Verify we can still read state (GetState uses message IDs too)
	state, err := client.GetState("input_boolean.nick_home")
	assert.NoError(t, err, "GetState should work after reconnection")
	assert.NotNil(t, state, "State should not be nil")

	t.Log("✅ All messages sent successfully after reconnection - message IDs are properly reset!")

	client.Disconnect()
}

// TestHighFrequencyStateChanges tests system under high load
func TestHighFrequencyStateChanges(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupTest(t)
	defer cleanup()

	const numChanges = 200
	changeCount := 0
	var mu sync.Mutex

	sub, err := manager.Subscribe("isNickHome", func(key string, oldValue, newValue interface{}) {
		mu.Lock()
		changeCount++
		mu.Unlock()
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Rapidly toggle state
	start := time.Now()
	for i := 0; i < numChanges; i++ {
		state := "off"
		if i%2 == 0 {
			state = "on"
		}
		server.SetState("input_boolean.nick_home", state, map[string]interface{}{})
		time.Sleep(1 * time.Millisecond) // Small delay to prevent overwhelming
	}
	duration := time.Since(start)

	// Wait for most events to propagate
	waitForCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return changeCount >= numChanges*8/10
	}, "should receive at least 80%% of changes")

	mu.Lock()
	finalCount := changeCount
	mu.Unlock()

	t.Logf("Sent %d changes in %v, received %d callbacks", numChanges, duration, finalCount)

	// We should receive most of them (allow some loss due to network/timing)
	assert.Greater(t, finalCount, numChanges*8/10, "Should receive at least 80% of changes")
}

// TestAllStateTypes tests all supported state types
func TestAllStateTypes(t *testing.T) {
	t.Parallel()
	_, _, manager, cleanup := setupTest(t)
	defer cleanup()

	t.Run("boolean operations", func(t *testing.T) {
		err := manager.SetBool("isHaveGuests", true)
		assert.NoError(t, err)

		value, err := manager.GetBool("isHaveGuests")
		assert.NoError(t, err)
		assert.True(t, value)
	})

	t.Run("number operations", func(t *testing.T) {
		err := manager.SetNumber("alarmTime", 1234567890.0)
		assert.NoError(t, err)

		value, err := manager.GetNumber("alarmTime")
		assert.NoError(t, err)
		assert.Equal(t, 1234567890.0, value)
	})

	t.Run("string operations", func(t *testing.T) {
		err := manager.SetString("dayPhase", "evening")
		assert.NoError(t, err)

		value, err := manager.GetString("dayPhase")
		assert.NoError(t, err)
		assert.Equal(t, "evening", value)
	})

	t.Run("JSON operations", func(t *testing.T) {
		data := map[string]interface{}{
			"playlist": "spotify:123",
			"volume":   75,
		}

		err := manager.SetJSON("currentlyPlayingMusic", data)
		assert.NoError(t, err)

		var retrieved map[string]interface{}
		err = manager.GetJSON("currentlyPlayingMusic", &retrieved)
		assert.NoError(t, err)
		assert.Equal(t, "spotify:123", retrieved["playlist"])
		assert.Equal(t, float64(75), retrieved["volume"])
	})
}

// TestReconnectStateSync tests that state is synchronized after reconnection
// This ensures that any state changes missed during disconnect are recovered
func TestReconnectStateSync(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Start server with dynamic port allocation
	server := NewMockHAServer(testAddr, testToken)
	server.InitializeStates()
	err := server.Start()
	require.NoError(t, err)

	// Capture the dynamically assigned address for reconnection
	serverAddr := server.Addr()

	// Connect client and create state manager using the dynamically assigned address
	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", serverAddr), testToken, logger)
	err = client.Connect()
	require.NoError(t, err)

	manager := state.NewManager(client, logger, false)
	err = manager.SyncFromHA()
	require.NoError(t, err)

	// Verify initial reconnect count is 0
	assert.Equal(t, 0, client.GetReconnectCount())

	// Track if callback was invoked
	callbackInvoked := false
	callbackMu := sync.Mutex{}

	// Set up reconnect callback that syncs state
	client.SetReconnectCallback(func() {
		callbackMu.Lock()
		callbackInvoked = true
		callbackMu.Unlock()

		// This is what happens in production - sync state after reconnect
		if err := manager.SyncFromHA(); err != nil {
			t.Logf("Failed to sync state after reconnect: %v", err)
		}
	})

	// Get initial state
	initialNickHome, err := manager.GetBool("isNickHome")
	require.NoError(t, err)
	assert.False(t, initialNickHome, "Nick should not be home initially")

	// Stop server to force disconnect
	t.Log("Stopping server to trigger reconnection...")
	server.Stop()

	// Wait for disconnect detection
	time.Sleep(1 * time.Second)

	// Restart server on the same address with CHANGED state
	// This simulates state changing while disconnected
	t.Log("Restarting server with changed state...")
	server = NewMockHAServer(serverAddr, testToken)
	server.InitializeStates()

	// Change state while client is disconnected - Nick comes home!
	server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})

	err = server.Start()
	require.NoError(t, err)
	defer server.Stop()

	// Wait for reconnection (with timeout)
	t.Log("Waiting for reconnection...")
	reconnected := false
	for i := 0; i < 40; i++ { // 40 seconds max
		if client.IsConnected() {
			reconnected = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	require.True(t, reconnected, "Client should reconnect automatically")

	// Wait for callback to be invoked
	waitForCondition(t, func() bool {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		return callbackInvoked
	}, "reconnect callback should be invoked")

	// Verify callback was invoked
	callbackMu.Lock()
	wasInvoked := callbackInvoked
	callbackMu.Unlock()
	assert.True(t, wasInvoked, "Reconnect callback should have been invoked")

	// Verify reconnect count was incremented
	assert.Equal(t, 1, client.GetReconnectCount(), "Reconnect count should be 1")

	// CRITICAL: Verify state was synced after reconnect
	// The state manager should now see Nick as home (changed while disconnected)
	nickHome, err := manager.GetBool("isNickHome")
	require.NoError(t, err)
	assert.True(t, nickHome, "Nick should be home after reconnect sync - state changed while disconnected")

	t.Log("✅ State successfully synchronized after reconnection!")

	client.Disconnect()
}

// TestReconnectCountIncrementsOnMultipleReconnects tests that reconnect count increases correctly
func TestReconnectCountIncrementsOnMultipleReconnects(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Start server with dynamic port allocation
	server := NewMockHAServer(testAddr, testToken)
	server.InitializeStates()
	err := server.Start()
	require.NoError(t, err)

	// Capture the dynamically assigned address for reconnection
	serverAddr := server.Addr()

	// Connect client using the dynamically assigned address
	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", serverAddr), testToken, logger)
	err = client.Connect()
	require.NoError(t, err)

	assert.Equal(t, 0, client.GetReconnectCount())

	// Disconnect and reconnect twice
	for i := 1; i <= 2; i++ {
		t.Logf("Disconnect/reconnect cycle %d...", i)

		// Stop server to force disconnect
		server.Stop()
		time.Sleep(1 * time.Second)

		// Restart server on the same address
		server = NewMockHAServer(serverAddr, testToken)
		server.InitializeStates()
		err = server.Start()
		require.NoError(t, err)

		// Wait for reconnection
		reconnected := false
		for j := 0; j < 40; j++ {
			if client.IsConnected() {
				reconnected = true
				break
			}
			time.Sleep(1 * time.Second)
		}
		require.True(t, reconnected, "Client should reconnect on cycle %d", i)

		// Wait for reconnect count to update
		waitForCondition(t, func() bool {
			return client.GetReconnectCount() >= i
		}, "reconnect count should reach %d", i)

		// Verify reconnect count
		assert.Equal(t, i, client.GetReconnectCount(), "Reconnect count should be %d after %d cycles", i, i)
	}

	t.Logf("✅ Reconnect count correctly tracked: %d", client.GetReconnectCount())

	server.Stop()
	client.Disconnect()
}
