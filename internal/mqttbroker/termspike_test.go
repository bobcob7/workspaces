//go:build spike

package mqttbroker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"io"
	"log/slog"
	"math/big"
	"net"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/mqttclient"
)

const (
	spikeID    = "env-00000001"
	chunkBytes = 16 << 10
)

type spikeProfile struct {
	name string
	rtt  time.Duration
	bps  int
}

var spikeProfiles = []spikeProfile{
	{"loopback", 0, 0},
	{"20ms-10mbit", 20 * time.Millisecond, 10_000_000},
	{"80ms-2mbit", 80 * time.Millisecond, 2_000_000},
}

func spikeLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// spikeRig is the real broker with one agent connected through the proxy.
type spikeRig struct {
	broker *Broker
	agent  *mqttclient.Client
}

func spikeCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, usage x509.ExtKeyUsage, serial int64) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der, ca.Raw}, PrivateKey: key}
}

func newSpikeRig(t *testing.T, p spikeProfile) *spikeRig {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "spike ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	server := spikeCert(t, ca, caKey, "broker", x509.ExtKeyUsageServerAuth, 2)
	agentCert := spikeCert(t, ca, caKey, spikeID, x509.ExtKeyUsageClientAuth, 3)
	broker := New(spikeLog())
	t.Cleanup(func() { _ = broker.Close() })
	rules := envproto.Rules{Allow: func(peer envproto.Peer, _ envproto.Access, topic string) bool {
		return strings.HasPrefix(topic, envproto.Prefix+peer.CN+"/")
	}}
	addr, err := broker.Start(t.Context(), "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{server},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, rules)
	require.NoError(t, err)
	dial := addr
	if p.rtt > 0 || p.bps > 0 {
		dial = newThrottle(t, addr, p.rtt, p.bps).Addr()
	}
	agent := mqttclient.New(spikeLog())
	t.Cleanup(func() { _ = agent.Close() })
	require.NoError(t, agent.Connect(t.Context(), envproto.ConnectOptions{
		Address:   dial,
		TLS:       &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, Certificates: []tls.Certificate{agentCert}, ServerName: "127.0.0.1"},
		ClientID:  spikeID,
		KeepAlive: 30 * time.Second,
	}))
	return &spikeRig{broker: broker, agent: agent}
}

func streamTopic(sid, dir string) string {
	return envproto.Topic(spikeID, envproto.LeafStream, sid, dir)
}

// frame is the placeholder format: an 8-byte big-endian sequence number and a
// payload of size bytes in total.
func frame(seq uint64, size int) []byte {
	b := make([]byte, max(size, 8))
	binary.BigEndian.PutUint64(b, seq)
	return b
}

func frameSeq(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[min(len(sorted)-1, int(float64(len(sorted))*q))]
}

func millis(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 2, 64)
}

// echo makes the agent publish every frame it receives on in/ back on out/.
func (r *spikeRig) echo(t *testing.T, sid string) {
	t.Helper()
	queue := make(chan []byte, 1024)
	require.NoError(t, r.agent.Subscribe(t.Context(), streamTopic(sid, "in"), func(m envproto.Message) { queue <- m.Payload }))
	go func() {
		for {
			select {
			case <-t.Context().Done():
				return
			case payload := <-queue:
				_ = r.agent.Publish(t.Context(), envproto.Message{Topic: streamTopic(sid, "out"), Payload: payload, QoS: 0})
			}
		}
	}()
}

func TestSpike_keystrokeEcho(t *testing.T) {
	for _, p := range spikeProfiles {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			r := newSpikeRig(t, p)
			r.echo(t, "s1")
			var mu sync.Mutex
			arrived := map[uint64]time.Time{}
			got := make(chan struct{}, 2048)
			unsub, err := r.broker.Subscribe(t.Context(), streamTopic("s1", "out"), func(m envproto.Message) {
				mu.Lock()
				arrived[frameSeq(m.Payload)] = time.Now()
				mu.Unlock()
				got <- struct{}{}
			})
			require.NoError(t, err)
			defer unsub()
			const keys = 1000
			sent := make([]time.Time, keys)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for i := range keys {
				<-ticker.C
				sent[i] = time.Now()
				require.NoError(t, r.broker.Publish(t.Context(), envproto.Message{Topic: streamTopic("s1", "in"), Payload: frame(uint64(i), 8), QoS: 1}))
			}
			time.Sleep(2*p.rtt + time.Second)
			mu.Lock()
			defer mu.Unlock()
			var rtts []time.Duration
			for i := range keys {
				if at, ok := arrived[uint64(i)]; ok {
					rtts = append(rtts, at.Sub(sent[i]))
				}
			}
			slices.Sort(rtts)
			require.NotEmpty(t, rtts)
			t.Logf("RESULT echo profile=%s rtt=%s keys=%d echoed=%d p50=%sms p95=%sms p99=%sms max=%sms",
				p.name, p.rtt, keys, len(rtts), millis(percentile(rtts, 0.50)), millis(percentile(rtts, 0.95)), millis(percentile(rtts, 0.99)), millis(rtts[len(rtts)-1]))
		})
	}
}

// flood publishes chunkBytes frames on out/ from the agent until stop closes
// and returns how many it sent.
func (r *spikeRig) flood(t *testing.T, sid string, stop <-chan struct{}) *atomic.Uint64 {
	t.Helper()
	var sent atomic.Uint64
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			seq := sent.Load()
			if err := r.agent.Publish(t.Context(), envproto.Message{Topic: streamTopic(sid, "out"), Payload: frame(seq, chunkBytes), QoS: 0}); err != nil {
				return
			}
			sent.Add(1)
		}
	}()
	return &sent
}

// gaps counts sequence numbers a receiver never saw, per stream.
type gaps struct {
	mu       sync.Mutex
	next     map[string]uint64
	received uint64
	bytes    uint64
	lost     uint64
}

func (g *gaps) see(topic string, payload []byte) {
	seq := frameSeq(payload)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next == nil {
		g.next = map[string]uint64{}
	}
	if want := g.next[topic]; seq > want {
		g.lost += seq - want
	}
	g.next[topic] = seq + 1
	g.received++
	g.bytes += uint64(len(payload))
}

func TestSpike_throughput(t *testing.T) {
	for _, p := range spikeProfiles {
		t.Run(p.name, func(t *testing.T) {
			r := newSpikeRig(t, p)
			var g gaps
			unsub, err := r.broker.Subscribe(t.Context(), streamTopic("s1", "out"), func(m envproto.Message) { g.see(m.Topic, m.Payload) })
			require.NoError(t, err)
			defer unsub()
			stop := make(chan struct{})
			start := time.Now()
			sent := r.flood(t, "s1", stop)
			time.Sleep(10 * time.Second)
			close(stop)
			elapsed := time.Since(start)
			time.Sleep(time.Second + 2*p.rtt)
			g.mu.Lock()
			defer g.mu.Unlock()
			mibs := float64(g.bytes) / (1 << 20) / elapsed.Seconds()
			link := "n/a"
			if p.bps > 0 {
				link = strconv.FormatFloat(mibs*8*1.048576/(float64(p.bps)/1e6)*100, 'f', 1, 64) + "%"
			}
			t.Logf("RESULT throughput profile=%s sent=%d received=%d lost=%d MiB/s=%.2f of_link=%s", p.name, sent.Load(), g.received, g.lost, mibs, link)
		})
	}
}

// slowHandler is the controller's in-process subscriber for one measurement.
type slowHandler struct {
	g       gaps
	dropped atomic.Uint64
	handled atomic.Uint64
}

const slowDelay = 5 * time.Millisecond

// inline sleeps in the broker's handler call, on the agent's read loop.
func (h *slowHandler) inline(m envproto.Message) {
	time.Sleep(slowDelay)
	h.g.see(m.Topic, m.Payload)
	h.handled.Add(1)
}

// buffered hands frames to a bounded channel (cap 64) and returns at once;
// a goroutine drains it at the same slow pace.
func (h *slowHandler) buffered(t *testing.T) func(envproto.Message) {
	queue := make(chan envproto.Message, 64)
	go func() {
		for {
			select {
			case <-t.Context().Done():
				return
			case m := <-queue:
				time.Sleep(slowDelay)
				h.g.see(m.Topic, m.Payload)
				h.handled.Add(1)
			}
		}
	}()
	return func(m envproto.Message) {
		select {
		case queue <- m:
		default:
			h.dropped.Add(1)
		}
	}
}

// probe is a control request: the controller publishes req/<n> (QoS 1), the
// agent answers on res/<n> (a stand-in for workspace.list), and the test
// measures the controller's wait. oneWay is an agent QoS 1 publish on res/x
// timed from the agent's call to the controller's handler.
type probe struct {
	mu    sync.Mutex
	reply map[string]chan struct{}
}

func (r *spikeRig) newProbe(t *testing.T) *probe {
	t.Helper()
	pr := &probe{reply: map[string]chan struct{}{}}
	require.NoError(t, r.agent.Subscribe(t.Context(), envproto.Topic(spikeID, envproto.LeafReq, "+"), func(m envproto.Message) {
		id := m.Topic[strings.LastIndex(m.Topic, "/")+1:]
		go func() {
			_ = r.agent.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(spikeID, envproto.LeafRes, id), Payload: []byte("ok"), QoS: 1})
		}()
	}))
	_, err := r.broker.Subscribe(t.Context(), envproto.Topic(spikeID, envproto.LeafRes, "+"), func(m envproto.Message) {
		id := m.Topic[strings.LastIndex(m.Topic, "/")+1:]
		pr.mu.Lock()
		ch := pr.reply[id]
		pr.mu.Unlock()
		if ch != nil {
			close(ch)
		}
	})
	require.NoError(t, err)
	return pr
}

func (pr *probe) request(t *testing.T, r *spikeRig, id string) (time.Duration, bool) {
	ch := make(chan struct{})
	pr.mu.Lock()
	pr.reply[id] = ch
	pr.mu.Unlock()
	start := time.Now()
	if err := r.broker.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(spikeID, envproto.LeafReq, id), Payload: []byte("workspace.list"), QoS: 1}); err != nil {
		return 0, false
	}
	select {
	case <-ch:
		return time.Since(start), true
	case <-time.After(20 * time.Second):
		return 0, false
	}
}

func (pr *probe) series(t *testing.T, r *spikeRig, prefix string, n int) []time.Duration {
	var out []time.Duration
	for i := range n {
		if d, ok := pr.request(t, r, prefix+strconv.Itoa(i)); ok {
			out = append(out, d)
		}
		time.Sleep(200 * time.Millisecond)
	}
	slices.Sort(out)
	return out
}

func median(sorted []time.Duration) time.Duration { return percentile(sorted, 0.5) }

func TestSpike_slowReader(t *testing.T) {
	for _, p := range spikeProfiles {
		t.Run(p.name, func(t *testing.T) {
			r := newSpikeRig(t, p)
			pr := r.newProbe(t)
			idle := pr.series(t, r, "idle", 10)
			require.NotEmpty(t, idle)
			t.Logf("RESULT slowreader profile=%s control request idle p50=%sms max=%sms n=%d", p.name, millis(median(idle)), millis(idle[len(idle)-1]), len(idle))
			for _, mode := range []string{"inline", "buffered"} {
				var h slowHandler
				fn := h.inline
				if mode == "buffered" {
					fn = h.buffered(t)
				}
				unsub, err := r.broker.Subscribe(t.Context(), streamTopic(mode, "out"), fn)
				require.NoError(t, err)
				stop := make(chan struct{})
				start := time.Now()
				sent := r.flood(t, mode, stop)
				time.Sleep(time.Second)
				busy := pr.series(t, r, mode, 10)
				close(stop)
				elapsed := time.Since(start)
				time.Sleep(time.Second + 2*p.rtt)
				unsub()
				var worst, mid time.Duration
				if len(busy) > 0 {
					worst, mid = busy[len(busy)-1], median(busy)
				}
				h.g.mu.Lock()
				t.Logf("RESULT slowreader profile=%s handler=%s sent=%d handled=%d dropped_by_handler=%d lost_by_broker=%d in=%.1fs control request answered=%d/10 p50=%sms max=%sms (idle p50 %sms)",
					p.name, mode, sent.Load(), h.handled.Load(), h.dropped.Load(), h.g.lost, elapsed.Seconds(), len(busy), millis(mid), millis(worst), millis(median(idle)))
				h.g.mu.Unlock()
			}
		})
	}
}

func heapInuse() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse
}

func TestSpike_brokerMemory(t *testing.T) {
	for _, p := range spikeProfiles {
		t.Run(p.name, func(t *testing.T) {
			for _, streams := range []int{1, 4, 8} {
				r := newSpikeRig(t, p)
				before := heapInuse()
				var h slowHandler
				stop := make(chan struct{})
				var unsubs []func()
				for i := range streams {
					sid := "m" + strconv.Itoa(i)
					unsub, err := r.broker.Subscribe(t.Context(), streamTopic(sid, "out"), h.inline)
					require.NoError(t, err)
					unsubs = append(unsubs, unsub)
					r.flood(t, sid, stop)
				}
				var peak uint64
				for range 20 {
					time.Sleep(250 * time.Millisecond)
					var ms runtime.MemStats
					runtime.ReadMemStats(&ms)
					peak = max(peak, ms.HeapInuse)
				}
				close(stop)
				time.Sleep(time.Second + 2*p.rtt)
				for _, u := range unsubs {
					u()
				}
				time.Sleep(time.Second)
				after := heapInuse()
				grow := float64(int64(peak)-int64(before)) / (1 << 20)
				t.Logf("RESULT memory profile=%s streams=%d before=%.1fMiB peak=%.1fMiB after=%.1fMiB growth=%.1fMiB per_stream=%.1fMiB",
					p.name, streams, float64(before)/(1<<20), float64(peak)/(1<<20), float64(after)/(1<<20), grow, grow/float64(streams))
			}
		})
	}
}
