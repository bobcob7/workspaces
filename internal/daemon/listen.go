package daemon

import (
	"context"
	"log/slog"
	"net"
	"os"
)

// ListenPeer prepares socket's directory as the daemon does, removes a stale
// socket and listens on it. It accepts only connections from the current uid
// (ADR 0082).
func ListenPeer(ctx context.Context, socket string, log *slog.Logger) (net.Listener, error) {
	ln, err := listen(ctx, socket)
	if err != nil {
		return nil, err
	}
	return peerListener{Listener: ln, uid: os.Getuid(), log: log}, nil
}
