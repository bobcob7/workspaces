//go:build windows

package daemon

import (
	"net"
	"os"
)

// peerUID is a pass-through on Windows: AF_UNIX there has no peer
// credentials, so it reports the listener's own uid and every connection
// passes. The guard is the socket directory's DACL, which prepareSocketDir
// restricts to the current user and then checks (ADR 0081 D4). Keep that
// Restrict-then-Check order; don't drop the CheckPrivate.
func peerUID(net.Conn) (int, error) { return os.Getuid(), nil }
