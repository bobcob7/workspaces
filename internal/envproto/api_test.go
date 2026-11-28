package envproto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckEndpoint(t *testing.T) {
	t.Run("accepts host:port with a host and a port from 1 to 65535", func(t *testing.T) {
		for _, endpoint := range []string{"ws.example.com:8885", "127.0.0.1:1", "[::1]:65535"} {
			assert.NoError(t, CheckEndpoint(endpoint), endpoint)
		}
	})
	t.Run("refuses an endpoint that is not host:port with a usable port", func(t *testing.T) {
		bad := []string{"", "ws.example.com", "ws.example.com:0", "ws.example.com:65536", "ws.example.com:+80", ":8885"}
		for _, endpoint := range bad {
			assert.Error(t, CheckEndpoint(endpoint), endpoint)
		}
	})
	t.Run("refuses an endpoint longer than MaxEndpointBytes", func(t *testing.T) {
		long := strings.Repeat("h", MaxEndpointBytes-4) + ":8885"
		assert.Len(t, long, MaxEndpointBytes+1)
		assert.Error(t, CheckEndpoint(long))
	})
}
