//go:build !linux && !darwin

package mqttclient

import "syscall"

// notSentLowWaterControl is a no-op where TCP_NOTSENT_LOWAT isn't available.
func notSentLowWaterControl(int) func(network, address string, c syscall.RawConn) error {
	return nil
}
