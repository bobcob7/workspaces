package mqttclient

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func Test_connectPacket(t *testing.T) {
	will := &envproto.Message{
		Topic:           "ws/v1/env/env-a/status",
		Payload:         []byte("offline"),
		QoS:             1,
		Retain:          true,
		ResponseTopic:   "ws/v1/env/env-a/req",
		CorrelationData: []byte("c"),
		Expiry:          time.Minute,
	}
	t.Run("sets Clean Start, the client id and the keep-alive in seconds", func(t *testing.T) {
		pc := connectPacket(envproto.ConnectOptions{ClientID: "env-a", KeepAlive: 30 * time.Second})
		assert.True(t, pc.CleanStart)
		assert.Equal(t, "env-a", pc.ClientID)
		assert.Equal(t, uint16(30), pc.KeepAlive)
		assert.Nil(t, pc.WillMessage)
		assert.Nil(t, pc.WillProperties)
	})
	t.Run("asks for a session that outlives the will delay by at least 10 s", func(t *testing.T) {
		pc := connectPacket(envproto.ConnectOptions{WillDelay: time.Second})
		require.NotNil(t, pc.Properties.SessionExpiryInterval)
		assert.Equal(t, uint32(10), *pc.Properties.SessionExpiryInterval)
		pc = connectPacket(envproto.ConnectOptions{WillDelay: time.Minute})
		assert.Equal(t, uint32(60), *pc.Properties.SessionExpiryInterval)
		pc = connectPacket(envproto.ConnectOptions{})
		assert.Equal(t, uint32(10), *pc.Properties.SessionExpiryInterval)
	})
	t.Run("carries the will and its Will Delay", func(t *testing.T) {
		pc := connectPacket(envproto.ConnectOptions{Will: will, WillDelay: 10 * time.Second})
		require.NotNil(t, pc.WillMessage)
		assert.Equal(t, will.Topic, pc.WillMessage.Topic)
		assert.Equal(t, will.Payload, pc.WillMessage.Payload)
		assert.Equal(t, byte(1), pc.WillMessage.QoS)
		assert.True(t, pc.WillMessage.Retain)
		require.NotNil(t, pc.WillProperties)
		require.NotNil(t, pc.WillProperties.WillDelayInterval)
		assert.Equal(t, uint32(10), *pc.WillProperties.WillDelayInterval)
		assert.Equal(t, will.ResponseTopic, pc.WillProperties.ResponseTopic)
		assert.Equal(t, will.CorrelationData, pc.WillProperties.CorrelationData)
		require.NotNil(t, pc.WillProperties.MessageExpiry)
		assert.Equal(t, uint32(60), *pc.WillProperties.MessageExpiry)
	})
	t.Run("a will without a delay sends no Will Delay", func(t *testing.T) {
		pc := connectPacket(envproto.ConnectOptions{Will: will})
		assert.Nil(t, pc.WillProperties.WillDelayInterval)
	})
	t.Run("a keep-alive under a second rounds up and a huge one is capped", func(t *testing.T) {
		assert.Equal(t, uint16(1), connectPacket(envproto.ConnectOptions{KeepAlive: time.Millisecond}).KeepAlive)
		assert.Equal(t, uint16(65535), connectPacket(envproto.ConnectOptions{KeepAlive: 24 * time.Hour}).KeepAlive)
		assert.Zero(t, connectPacket(envproto.ConnectOptions{}).KeepAlive)
	})
}

func Test_toPublish(t *testing.T) {
	t.Run("keeps every property", func(t *testing.T) {
		msg := envproto.Message{
			Topic:           "t",
			Payload:         []byte("p"),
			QoS:             2,
			Retain:          true,
			ResponseTopic:   "r",
			CorrelationData: []byte{0, 1, 0xff},
			Expiry:          90 * time.Second,
		}
		p := toPublish(&msg)
		assert.Equal(t, "t", p.Topic)
		assert.Equal(t, []byte("p"), p.Payload)
		assert.Equal(t, byte(2), p.QoS)
		assert.True(t, p.Retain)
		assert.Equal(t, "r", p.Properties.ResponseTopic)
		assert.Equal(t, []byte{0, 1, 0xff}, p.Properties.CorrelationData)
		require.NotNil(t, p.Properties.MessageExpiry)
		assert.Equal(t, uint32(90), *p.Properties.MessageExpiry)
	})
	t.Run("no expiry sends none", func(t *testing.T) {
		assert.Nil(t, toPublish(&envproto.Message{Topic: "t"}).Properties.MessageExpiry)
	})
	t.Run("a sub-second expiry rounds up to a second", func(t *testing.T) {
		assert.Equal(t, uint32(1), *toPublish(&envproto.Message{Topic: "t", Expiry: time.Millisecond}).Properties.MessageExpiry)
	})
}

func Test_fromPublish(t *testing.T) {
	t.Run("is the reverse of toPublish", func(t *testing.T) {
		msg := envproto.Message{
			Topic:           "t",
			Payload:         []byte("p"),
			QoS:             1,
			Retain:          true,
			ResponseTopic:   "r",
			CorrelationData: []byte{9, 8},
			Expiry:          45 * time.Second,
		}
		assert.Equal(t, msg, fromPublish(toPublish(&msg)))
	})
	t.Run("a packet without properties has none", func(t *testing.T) {
		got := fromPublish(&paho.Publish{Topic: "t", Payload: []byte("p")})
		assert.Equal(t, envproto.Message{Topic: "t", Payload: []byte("p")}, got)
	})
}

func TestClient_Connect(t *testing.T) {
	t.Run("a missing TLS config fails", func(t *testing.T) {
		err := New(discardLog()).Connect(t.Context(), envproto.ConnectOptions{Address: "127.0.0.1:1"})
		assert.ErrorIs(t, err, errNoTLSConfig)
	})
	t.Run("an unreachable broker fails", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		err := New(discardLog()).Connect(ctx, envproto.ConnectOptions{Address: "127.0.0.1:1", TLS: &tls.Config{MinVersion: tls.VersionTLS13}})
		assert.Error(t, err)
	})
}

func TestClient_Publish(t *testing.T) {
	t.Run("fails when not connected", func(t *testing.T) {
		err := New(discardLog()).Publish(t.Context(), envproto.Message{Topic: "t"})
		assert.ErrorIs(t, err, ErrNotConnected)
	})
}

func TestClient_Subscribe(t *testing.T) {
	t.Run("fails when not connected", func(t *testing.T) {
		err := New(discardLog()).Subscribe(t.Context(), "t", func(envproto.Message) {})
		assert.ErrorIs(t, err, ErrNotConnected)
	})
}

func TestClient_Done(t *testing.T) {
	t.Run("is nil before the first Connect", func(t *testing.T) {
		assert.Nil(t, New(discardLog()).Done())
	})
}

func TestClient_Close(t *testing.T) {
	t.Run("does nothing when not connected", func(t *testing.T) {
		assert.NoError(t, New(discardLog()).Close())
	})
}
