//go:build linux || darwin

package mqttclient

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// notSentLowWaterControl returns a net.Dialer Control function that sets
// TCP_NOTSENT_LOWAT to n bytes on the socket. A failure to set it is returned,
// so the connect fails loudly and the agent's reconnect loop logs and retries
// rather than running without the bound: the option exists on every Linux
// since 3.12 and on macOS.
func notSentLowWaterControl(n int) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var sockErr error
		if err := c.Control(func(fd uintptr) {
			sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, n)
		}); err != nil {
			return err
		}
		return sockErr
	}
}
