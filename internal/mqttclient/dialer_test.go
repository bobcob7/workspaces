package mqttclient

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func Test_newDialer(t *testing.T) {
	t.Run("uses the TLS config of the options", func(t *testing.T) {
		cfg := &tls.Config{ServerName: "broker"}
		d := newDialer(envproto.ConnectOptions{TLS: cfg})
		assert.Same(t, cfg, d.Config)
	})
	t.Run("leaves the socket alone when NotSentLowWater is 0", func(t *testing.T) {
		d := newDialer(envproto.ConnectOptions{})
		require.NotNil(t, d.NetDialer)
		assert.Nil(t, d.NetDialer.Control)
	})
	t.Run("sets a Control function when NotSentLowWater is positive", func(t *testing.T) {
		d := newDialer(envproto.ConnectOptions{NotSentLowWater: 16384})
		require.NotNil(t, d.NetDialer)
		assert.NotNil(t, d.NetDialer.Control)
	})
}
