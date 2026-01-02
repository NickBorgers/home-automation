//go:build linux
// +build linux

package ha

import (
	"fmt"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// TCP keepalive configuration for detecting dead connections.
// These values determine how quickly the OS detects and closes stale connections.
const (
	// tcpKeepIdle is the time before the first keepalive probe is sent.
	// OS default is 7200 seconds (2 hours), which is far too slow to detect
	// dead connections. We use 10 seconds to detect issues quickly.
	tcpKeepIdle = 10 * time.Second

	// tcpKeepInterval is the interval between keepalive probes after the first.
	// OS default is 75 seconds. We use 5 seconds for faster detection.
	tcpKeepInterval = 5 * time.Second

	// tcpKeepCount is the number of unacknowledged probes before giving up.
	// OS default is 9. We use 3 probes for faster failure detection.
	// With our settings: 10s + (5s * 3) = 25 seconds max to detect dead connection.
	tcpKeepCount = 3
)

// setTCPKeepAlive configures TCP keepalive settings on a connection using syscalls.
//
// Go's net.Dialer.KeepAlive only sets the keepalive interval via SO_KEEPALIVE,
// but does NOT configure TCP_KEEPIDLE (time before first probe). The OS default
// for TCP_KEEPIDLE is typically 7200 seconds (2 hours), which means dead connections
// aren't detected for hours.
//
// This function sets:
//   - TCP_KEEPIDLE: Time to wait before sending the first probe (default: 10s)
//   - TCP_KEEPINTVL: Interval between subsequent probes (default: 5s)
//   - TCP_KEEPCNT: Number of unacked probes before marking connection dead (default: 3)
//
// Reference: https://github.com/golang/go/issues/62254
func setTCPKeepAlive(conn net.Conn, idle, interval time.Duration, count int) error {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return fmt.Errorf("not a TCP connection: %T", conn)
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("failed to get raw connection: %w", err)
	}

	var syscallErr error
	err = rawConn.Control(func(fd uintptr) {
		// Enable TCP keepalive
		syscallErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
		if syscallErr != nil {
			return
		}

		// Set time before first keepalive probe (TCP_KEEPIDLE)
		// This is the critical setting - OS default is 7200 seconds!
		syscallErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, unix.TCP_KEEPIDLE, int(idle.Seconds()))
		if syscallErr != nil {
			return
		}

		// Set interval between keepalive probes (TCP_KEEPINTVL)
		syscallErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, unix.TCP_KEEPINTVL, int(interval.Seconds()))
		if syscallErr != nil {
			return
		}

		// Set number of keepalive probes before giving up (TCP_KEEPCNT)
		syscallErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, unix.TCP_KEEPCNT, count)
	})

	if err != nil {
		return fmt.Errorf("failed to control raw connection: %w", err)
	}
	if syscallErr != nil {
		return fmt.Errorf("setsockopt failed: %w", syscallErr)
	}

	return nil
}
