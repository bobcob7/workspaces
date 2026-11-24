package mqttclient

import (
	"crypto/tls"
	"net"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// newDialer returns the TLS dialer for opts; its TCP connection has
// TCP_NOTSENT_LOWAT set when opts.NotSentLowWater is positive.
func newDialer(opts envproto.ConnectOptions) *tls.Dialer {
	d := &net.Dialer{}
	if opts.NotSentLowWater > 0 {
		d.Control = notSentLowWaterControl(opts.NotSentLowWater)
	}
	return &tls.Dialer{NetDialer: d, Config: opts.TLS}
}
