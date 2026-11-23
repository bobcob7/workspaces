//go:build spike

package mqttbroker

import (
	"context"
	"crypto/tls"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// sockOpts are the options set on the agent's socket before it connects.
type sockOpts struct {
	sndbuf       int // SO_SNDBUF in bytes; 0 keeps the OS default and autotuning.
	notsentLowat int // TCP_NOTSENT_LOWAT in bytes; 0 leaves it off.
}

func (o sockOpts) control(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		if o.sndbuf > 0 {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF, o.sndbuf)
		}
		if serr == nil && o.notsentLowat > 0 {
			serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, o.notsentLowat)
		}
	})
	if err != nil {
		return err
	}
	return serr
}

type altHandler struct {
	filter string
	fn     func(envproto.Message)
}

// altAgent is an MQTT agent connection like mqttclient's, whose dial sets
// sockOpts. It exists because mqttclient has no dial hook (that is T1a's
// option), and the spike changes no production code.
type altAgent struct {
	t        *testing.T
	conn     *paho.Client
	tcp      *net.TCPConn
	mu       sync.Mutex
	handlers []altHandler
}

func dialAlt(t *testing.T, address string, tlsConfig *tls.Config, clientID string, so sockOpts) *altAgent {
	t.Helper()
	a := &altAgent{t: t}
	dialer := tls.Dialer{Config: tlsConfig, NetDialer: &net.Dialer{Control: so.control}}
	raw, err := dialer.DialContext(t.Context(), "tcp", address)
	require.NoError(t, err)
	a.tcp = raw.(*tls.Conn).NetConn().(*net.TCPConn)
	t.Cleanup(func() { _ = a.tcp.Close() })
	a.conn = paho.NewClient(paho.ClientConfig{
		Conn:              packets.NewThreadSafeConn(raw),
		OnPublishReceived: []func(paho.PublishReceived) (bool, error){a.receive},
	})
	expiry := uint32(10)
	_, err = a.conn.Connect(t.Context(), &paho.Connect{
		ClientID:   clientID,
		KeepAlive:  30,
		CleanStart: true,
		Properties: &paho.ConnectProperties{SessionExpiryInterval: &expiry},
	})
	require.NoError(t, err)
	return a
}

func (a *altAgent) receive(pr paho.PublishReceived) (bool, error) {
	msg := envproto.Message{Topic: pr.Packet.Topic, Payload: pr.Packet.Payload, QoS: pr.Packet.QoS}
	a.mu.Lock()
	var fns []func(envproto.Message)
	for _, h := range a.handlers {
		if envproto.Match(h.filter, msg.Topic) {
			fns = append(fns, h.fn)
		}
	}
	a.mu.Unlock()
	for _, fn := range fns {
		fn(msg)
	}
	return len(fns) > 0, nil
}

func (a *altAgent) publish(ctx context.Context, topic string, payload []byte, qos byte) error {
	_, err := a.conn.Publish(ctx, &paho.Publish{Topic: topic, Payload: payload, QoS: qos})
	return err
}

func (a *altAgent) subscribe(filter string, fn func(envproto.Message)) {
	a.t.Helper()
	a.mu.Lock()
	a.handlers = append(a.handlers, altHandler{filter: filter, fn: fn})
	a.mu.Unlock()
	_, err := a.conn.Subscribe(a.t.Context(), &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: filter, QoS: 1}}})
	require.NoError(a.t, err)
}

// sockInt reads a socket-level integer option; sendQueue is the bytes in the
// kernel send queue (unsent and unacknowledged, SIOCOUTQ).
func (a *altAgent) sockInt(read func(fd int) (int, error)) int {
	sc, err := a.tcp.SyscallConn()
	if err != nil {
		return -1
	}
	n := -1
	_ = sc.Control(func(fd uintptr) {
		if v, err := read(int(fd)); err == nil {
			n = v
		}
	})
	return n
}

func (a *altAgent) sendQueue() int {
	return a.sockInt(func(fd int) (int, error) { return unix.IoctlGetInt(fd, unix.TIOCOUTQ) })
}

func (a *altAgent) sndbuf() int {
	return a.sockInt(func(fd int) (int, error) { return unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF) })
}

// altCell is one measured configuration.
type altCell struct {
	name   string
	sock   sockOpts // on the stream connection (the only one when single).
	second bool     // the stream rides a second connection.
	shared bool     // both connections share one bottleneck queue.
}

type altResult struct {
	idleP50, p50, p99 time.Duration
	answered          int
	echoIdleP50       time.Duration
	echoLoadP50       time.Duration
	echoLoadN         int
	mibs              float64
	peakQueue         int
	sndbuf            int
}

const (
	altSamples      = 5
	altSharedQueue  = 64 << 10
	altControlLimit = 10 * time.Second
)

// altRig is the broker with a control connection and a stream connection
// (the same one unless second).
type altRig struct {
	env    *spikeEnv
	ctl    *altAgent
	stream *altAgent
}

func newAltRig(t *testing.T, p spikeProfile, c altCell) *altRig {
	t.Helper()
	env := startSpikeBroker(t)
	ctlAddr, streamAddr := env.addr, env.addr
	if p.rtt > 0 || p.bps > 0 {
		if c.shared {
			ctlAddr = newSharedThrottle(t, env.addr, p.rtt, p.bps, altSharedQueue).Addr()
		} else {
			ctlAddr = newThrottle(t, env.addr, p.rtt, p.bps).Addr()
		}
		streamAddr = ctlAddr
	}
	r := &altRig{env: env}
	if !c.second {
		r.ctl = dialAlt(t, ctlAddr, env.agentTLS(t, spikeID, 3), spikeID, c.sock)
		r.stream = r.ctl
		return r
	}
	r.ctl = dialAlt(t, ctlAddr, env.agentTLS(t, spikeID, 3), spikeID, sockOpts{})
	r.stream = dialAlt(t, streamAddr, env.agentTLS(t, spikeID+".t", 4), spikeID+".t", c.sock)
	return r
}

// arrivals records when each frame sequence number reached the controller.
type arrivals struct {
	mu sync.Mutex
	at map[uint64]time.Time
}

func (a *arrivals) record(m envproto.Message) {
	a.mu.Lock()
	a.at[frameSeq(m.Payload)] = time.Now()
	a.mu.Unlock()
}

// keys sends n echo keystrokes 100 ms apart starting at sequence base and
// returns each one's round trip; keys not echoed within 5 s are left out.
func (r *altRig) keys(t *testing.T, arr *arrivals, base uint64, n int) []time.Duration {
	t.Helper()
	sent := make([]time.Time, n)
	for i := range n {
		sent[i] = time.Now()
		require.NoError(t, r.env.broker.Publish(t.Context(), envproto.Message{Topic: streamTopic("e", "in"), Payload: frame(base+uint64(i), 8), QoS: 1}))
		time.Sleep(100 * time.Millisecond)
	}
	deadline := time.Now().Add(5 * time.Second)
	var out []time.Duration
	for len(out) < n && time.Now().Before(deadline) {
		out = out[:0]
		arr.mu.Lock()
		for i := range n {
			if at, ok := arr.at[base+uint64(i)]; ok {
				out = append(out, at.Sub(sent[i]))
			}
		}
		arr.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	slices.Sort(out)
	return out
}

func runAltCell(t *testing.T, p spikeProfile, c altCell) altResult {
	t.Helper()
	r := newAltRig(t, p, c)
	var res altResult
	res.sndbuf = r.stream.sndbuf()
	r.ctl.subscribe(envproto.Topic(spikeID, envproto.LeafReq, "+"), func(m envproto.Message) {
		id := m.Topic[strings.LastIndex(m.Topic, "/")+1:]
		go func() { _ = r.ctl.publish(t.Context(), envproto.Topic(spikeID, envproto.LeafRes, id), []byte("ok"), 1) }()
	})
	var mu sync.Mutex
	reply := map[string]chan struct{}{}
	_, err := r.env.broker.Subscribe(t.Context(), envproto.Topic(spikeID, envproto.LeafRes, "+"), func(m envproto.Message) {
		id := m.Topic[strings.LastIndex(m.Topic, "/")+1:]
		mu.Lock()
		ch := reply[id]
		mu.Unlock()
		if ch != nil {
			close(ch)
		}
	})
	require.NoError(t, err)
	request := func(id string) (time.Duration, bool) {
		ch := make(chan struct{})
		mu.Lock()
		reply[id] = ch
		mu.Unlock()
		start := time.Now()
		if err := r.env.broker.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(spikeID, envproto.LeafReq, id), Payload: []byte("workspace.list"), QoS: 1}); err != nil {
			return 0, false
		}
		select {
		case <-ch:
			return time.Since(start), true
		case <-time.After(altControlLimit):
			return 0, false
		}
	}
	series := func(prefix string) []time.Duration {
		var out []time.Duration
		for i := range altSamples {
			if d, ok := request(prefix + strconv.Itoa(i)); ok {
				out = append(out, d)
			}
			time.Sleep(200 * time.Millisecond)
		}
		slices.Sort(out)
		return out
	}
	r.stream.subscribe(streamTopic("e", "in"), func(m envproto.Message) {
		go func() { _ = r.stream.publish(t.Context(), streamTopic("e", "out"), m.Payload, 0) }()
	})
	arr := &arrivals{at: map[uint64]time.Time{}}
	_, err = r.env.broker.Subscribe(t.Context(), streamTopic("e", "out"), arr.record)
	require.NoError(t, err)
	var g gaps
	_, err = r.env.broker.Subscribe(t.Context(), streamTopic("f", "out"), func(m envproto.Message) { g.see(m.Topic, m.Payload) })
	require.NoError(t, err)
	idle := series("idle")
	require.NotEmpty(t, idle)
	res.idleP50 = median(idle)
	res.echoIdleP50 = median(r.keys(t, arr, 0, 20))
	stop := make(chan struct{})
	var seq uint64
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := r.stream.publish(t.Context(), streamTopic("f", "out"), frame(seq, chunkBytes), 0); err != nil {
				return
			}
			seq++
		}
	}()
	time.Sleep(time.Second)
	g.mu.Lock()
	b0 := g.bytes
	g.mu.Unlock()
	t0 := time.Now()
	sampling := make(chan struct{})
	peak := make(chan int, 1)
	go func() {
		top := 0
		for {
			select {
			case <-sampling:
				peak <- top
				return
			case <-time.After(20 * time.Millisecond):
				top = max(top, r.stream.sendQueue())
			}
		}
	}()
	busy := series("busy")
	g.mu.Lock()
	b1 := g.bytes
	g.mu.Unlock()
	elapsed := time.Since(t0)
	close(sampling)
	res.peakQueue = <-peak
	res.mibs = float64(b1-b0) / (1 << 20) / elapsed.Seconds()
	res.answered = len(busy)
	if len(busy) > 0 {
		res.p50, res.p99 = median(busy), busy[len(busy)-1]
	}
	loaded := r.keys(t, arr, 1000, 10)
	res.echoLoadN = len(loaded)
	res.echoLoadP50 = median(loaded)
	close(stop)
	return res
}

// altLimit is D1's limit for a control request under load: 2 x idle + 100 ms.
func altLimit(idle time.Duration) time.Duration { return 2*idle + 100*time.Millisecond }

var altCells = []altCell{
	{name: "a single, default buffer"},
	{name: "b single, SO_SNDBUF 64K", sock: sockOpts{sndbuf: 64 << 10}},
	{name: "c single, NOTSENT_LOWAT 16K", sock: sockOpts{notsentLowat: 16 << 10}},
	{name: "d single, both", sock: sockOpts{sndbuf: 64 << 10, notsentLowat: 16 << 10}},
	{name: "e second, default buffer", second: true},
	{name: "f1 second, SO_SNDBUF 64K", second: true, sock: sockOpts{sndbuf: 64 << 10}},
	{name: "f2 second, NOTSENT_LOWAT 16K", second: true, sock: sockOpts{notsentLowat: 16 << 10}},
}

var altProfiles = []spikeProfile{
	{"20ms-10mbit", 20 * time.Millisecond, 10_000_000},
	{"40ms-100mbit", 40 * time.Millisecond, 100_000_000},
}

var altTightCells = []altCell{
	{name: "g1 single, SO_SNDBUF 32K", sock: sockOpts{sndbuf: 32 << 10}},
	{name: "g2 single, SO_SNDBUF 16K", sock: sockOpts{sndbuf: 16 << 10}},
	{name: "g3 single, NOTSENT_LOWAT 4K", sock: sockOpts{notsentLowat: 4 << 10}},
	{name: "g4 single, SO_SNDBUF 32K + NOTSENT_LOWAT 4K", sock: sockOpts{sndbuf: 32 << 10, notsentLowat: 4 << 10}},
}

// TestSpike_alternatives measures the control request, echo and stream
// throughput for each way of bounding the agent's send queue.
func TestSpike_alternatives(t *testing.T) { runAltMatrix(t, altCells) }

// TestSpike_alternativesTight repeats single-connection cells with tighter
// bounds.
func TestSpike_alternativesTight(t *testing.T) { runAltMatrix(t, altTightCells) }

// runAltMatrix runs every cell on each profile through per-connection pacing
// (separate) and through one shared 64 KiB bottleneck queue. The two profiles
// run in parallel; the cells of a profile run one after another.
func runAltMatrix(t *testing.T, cells []altCell) {
	for _, p := range altProfiles {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			for _, shared := range []bool{false, true} {
				for _, c := range cells {
					c.shared = shared
					proxy := "separate"
					if shared {
						proxy = "shared-64k"
					}
					t.Run(proxy+"/"+strings.ReplaceAll(c.name, " ", "_"), func(t *testing.T) {
						res := runAltCell(t, p, c)
						limit := altLimit(res.idleP50)
						verdict := "FAIL"
						if res.answered == altSamples && res.p50 <= limit {
							verdict = "PASS"
						}
						link := res.mibs * 8 * 1.048576 / (float64(p.bps) / 1e6) * 100
						t.Logf("RESULT alt profile=%s proxy=%s cell=%q idle_p50=%sms ctl_p50=%sms ctl_p99=%sms answered=%d/%d limit=%sms echo_idle_p50=%sms echo_load_p50=%sms echo_load_n=%d/10 MiB/s=%.2f of_link=%.1f%% peak_sendq=%dKiB sndbuf=%d %s",
							p.name, proxy, c.name, millis(res.idleP50), millis(res.p50), millis(res.p99), res.answered, altSamples, millis(limit),
							millis(res.echoIdleP50), millis(res.echoLoadP50), res.echoLoadN, res.mibs, link, res.peakQueue>>10, res.sndbuf, verdict)
					})
				}
			}
		})
	}
}
