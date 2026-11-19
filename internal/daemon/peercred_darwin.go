package daemon

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID is the uid of the process on the other end of conn
// (LOCAL_PEERCRED).
func peerUID(conn net.Conn) (int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errNotUnixConn
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("reading the peer: %w", err)
	}
	var cred *unix.Xucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if err != nil {
		return 0, fmt.Errorf("reading the peer: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("reading the peer: %w", credErr)
	}
	return int(cred.Uid), nil
}
