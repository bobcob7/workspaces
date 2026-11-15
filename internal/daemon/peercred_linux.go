package daemon

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID is the uid of the process on the other end of conn (SO_PEERCRED).
func peerUID(conn net.Conn) (int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errNotUnixConn
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("reading the peer: %w", err)
	}
	var cred *unix.Ucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return 0, fmt.Errorf("reading the peer: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("reading the peer: %w", credErr)
	}
	return int(cred.Uid), nil
}
