//go:build windows

package daemon

import (
	"net"
	"os"
)

// peerUID is the listener's own uid: the socket directory's DACL is the
// check on Windows, since AF_UNIX there has no peer credentials (ADR 0081 D4).
func peerUID(net.Conn) (int, error) { return os.Getuid(), nil }
