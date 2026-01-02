//go:build linux
// +build linux

package ha

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetTCPKeepAlive(t *testing.T) {
	t.Parallel(
	// Create a TCP listener to accept our test connection
	)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	// Connect to the listener
	conn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	// Accept the connection on the server side (required for handshake)
	serverConn, err := listener.Accept()
	require.NoError(t, err)
	defer serverConn.Close()

	// Test setting TCP keepalive options
	err = setTCPKeepAlive(conn, 10*time.Second, 5*time.Second, 3)
	assert.NoError(t, err, "setTCPKeepAlive should succeed on a TCP connection")
}

func TestSetTCPKeepAlive_NotTCPConnection(t *testing.T) {
	t.Parallel(
	// Create a non-TCP connection (Unix socket)
	)

	listener, err := net.Listen("unix", "/tmp/test_keepalive.sock")
	if err != nil {
		t.Skip("Cannot create Unix socket, skipping test")
	}
	defer listener.Close()

	conn, err := net.Dial("unix", "/tmp/test_keepalive.sock")
	if err != nil {
		t.Skip("Cannot connect to Unix socket, skipping test")
	}
	defer conn.Close()

	// Setting TCP keepalive on a non-TCP connection should fail
	err = setTCPKeepAlive(conn, 10*time.Second, 5*time.Second, 3)
	assert.Error(t, err, "setTCPKeepAlive should fail on non-TCP connection")
	assert.Contains(t, err.Error(), "not a TCP connection")
}

func TestTCPKeepAliveConstants(t *testing.T) {
	t.Parallel(
	// Verify the constants are set to expected values
	)

	assert.Equal(t, 10*time.Second, tcpKeepIdle, "tcpKeepIdle should be 10 seconds")
	assert.Equal(t, 5*time.Second, tcpKeepInterval, "tcpKeepInterval should be 5 seconds")
	assert.Equal(t, 3, tcpKeepCount, "tcpKeepCount should be 3")

	// Calculate expected detection time: idle + (interval * count)
	expectedDetectionTime := tcpKeepIdle + (tcpKeepInterval * time.Duration(tcpKeepCount))
	assert.Equal(t, 25*time.Second, expectedDetectionTime,
		"Dead connection should be detected in ~25 seconds")
}
