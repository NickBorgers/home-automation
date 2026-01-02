//go:build !linux
// +build !linux

package ha

import (
	"net"
	"time"
)

// TCP keepalive configuration - these values are used by client.go for logging
// even on non-Linux platforms where the syscall-based configuration isn't available.
const (
	tcpKeepIdle     = 10 * time.Second
	tcpKeepInterval = 5 * time.Second
	tcpKeepCount    = 3
)

// setTCPKeepAlive is a no-op on non-Linux platforms.
// Linux uses syscalls to configure TCP_KEEPIDLE, TCP_KEEPINTVL, and TCP_KEEPCNT,
// but these constants aren't available on other platforms.
// On non-Linux, Go's default TCP keepalive (net.Dialer.KeepAlive) is used,
// which is less optimal but still functional.
func setTCPKeepAlive(conn net.Conn, idle, interval time.Duration, count int) error {
	// No-op on non-Linux platforms
	// The connection will still work, but dead connection detection may be slower
	return nil
}
