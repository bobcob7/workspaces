//go:build spike

package mqttbroker

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const throttleChunk = 4 << 10

// throttle is a TCP proxy on 127.0.0.1:0 to target that delays each
// direction by rtt/2 and paces bytes at bitsPerSecond (0: unpaced).
type throttle struct {
	t      *testing.T
	ln     net.Listener
	target string
	rtt    time.Duration
	bps    int
	mu     sync.Mutex
	conns  []net.Conn
}

func newThrottle(t *testing.T, target string, rtt time.Duration, bitsPerSecond int) *throttle {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &throttle{t: t, ln: ln, target: target, rtt: rtt, bps: bitsPerSecond}
	t.Cleanup(p.close)
	go p.serve()
	return p
}

// Addr is the proxy's listening address.
func (p *throttle) Addr() string { return p.ln.Addr().String() }

func (p *throttle) close() {
	_ = p.ln.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

func (p *throttle) serve() {
	for {
		in, err := p.ln.Accept()
		if err != nil {
			return
		}
		out, err := (&net.Dialer{}).DialContext(p.t.Context(), "tcp", p.target)
		if err != nil {
			_ = in.Close()
			continue
		}
		for _, c := range []net.Conn{in, out} {
			if tcp, ok := c.(*net.TCPConn); ok {
				_ = tcp.SetReadBuffer(32 << 10)
				_ = tcp.SetWriteBuffer(32 << 10)
			}
		}
		p.mu.Lock()
		p.conns = append(p.conns, in, out)
		p.mu.Unlock()
		go p.pipe(out, in)
		go p.pipe(in, out)
	}
}

// depth is the proxy's queue length in chunks per direction: 16, or the
// link's bandwidth-delay product plus 16 when that is larger, so the delay
// line itself never caps throughput.
func (p *throttle) depth() int {
	bdp := int(float64(p.bps) / 8 * (p.rtt / 2).Seconds() / throttleChunk)
	if bdp <= 16 {
		return 16
	}
	return bdp + 16
}

type stamped struct {
	at   time.Time
	data []byte
}

// pipe copies src to dst: a reader stamps each chunk, a writer releases it
// rtt/2 later and paces it through a token bucket over bytes.
func (p *throttle) pipe(dst, src net.Conn) {
	queue := make(chan stamped, p.depth())
	go func() {
		defer close(queue)
		for {
			buf := make([]byte, throttleChunk)
			n, err := src.Read(buf)
			if n > 0 {
				queue <- stamped{at: time.Now(), data: buf[:n]}
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { _ = dst.Close(); _ = src.Close() }()
	var free time.Time
	for chunk := range queue {
		release := chunk.at.Add(p.rtt / 2)
		if free.After(release) {
			release = free
		}
		time.Sleep(time.Until(release))
		if p.bps > 0 {
			cost := time.Duration(float64(len(chunk.data)*8) / float64(p.bps) * float64(time.Second))
			free = release.Add(cost)
		}
		if _, err := dst.Write(chunk.data); err != nil {
			return
		}
		if p.bps > 0 {
			time.Sleep(time.Until(free))
		}
	}
}

type sharedChunk struct {
	at   time.Time
	dst  net.Conn
	data []byte
}

// sharedThrottle is a TCP proxy like throttle whose connections share one
// bottleneck per direction: a FIFO of at most queueBytes (4 KiB chunks from
// every connection, in arrival order) drained by one goroutine that paces at
// bitsPerSecond, followed by a delay line of rtt/2.
type sharedThrottle struct {
	t        *testing.T
	ln       net.Listener
	target   string
	rtt      time.Duration
	bps      int
	toBroker chan sharedChunk
	toAgent  chan sharedChunk
	mu       sync.Mutex
	conns    []net.Conn
}

func newSharedThrottle(t *testing.T, target string, rtt time.Duration, bitsPerSecond, queueBytes int) *sharedThrottle {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	depth := max(1, queueBytes/throttleChunk)
	p := &sharedThrottle{t: t, ln: ln, target: target, rtt: rtt, bps: bitsPerSecond, toBroker: make(chan sharedChunk, depth), toAgent: make(chan sharedChunk, depth)}
	t.Cleanup(p.close)
	go p.bottleneck(p.toBroker)
	go p.bottleneck(p.toAgent)
	go p.serve()
	return p
}

// Addr is the proxy's listening address.
func (p *sharedThrottle) Addr() string { return p.ln.Addr().String() }

func (p *sharedThrottle) close() {
	_ = p.ln.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

func (p *sharedThrottle) serve() {
	for {
		in, err := p.ln.Accept()
		if err != nil {
			return
		}
		out, err := (&net.Dialer{}).DialContext(p.t.Context(), "tcp", p.target)
		if err != nil {
			_ = in.Close()
			continue
		}
		for _, c := range []net.Conn{in, out} {
			if tcp, ok := c.(*net.TCPConn); ok {
				_ = tcp.SetReadBuffer(32 << 10)
				_ = tcp.SetWriteBuffer(32 << 10)
			}
		}
		p.mu.Lock()
		p.conns = append(p.conns, in, out)
		p.mu.Unlock()
		go p.feed(in, out, p.toBroker)
		go p.feed(out, in, p.toAgent)
	}
}

// feed reads src and queues each chunk, for dst, in the shared FIFO; a full
// FIFO blocks the reader, as a full bottleneck queue backs up its senders.
func (p *sharedThrottle) feed(src, dst net.Conn, fifo chan<- sharedChunk) {
	defer func() { _ = dst.Close(); _ = src.Close() }()
	for {
		buf := make([]byte, throttleChunk)
		n, err := src.Read(buf)
		if n > 0 {
			fifo <- sharedChunk{at: time.Now(), dst: dst, data: buf[:n]}
		}
		if err != nil {
			return
		}
	}
}

// bottleneck serialises the FIFO at the link rate and hands each chunk to the
// delay line.
func (p *sharedThrottle) bottleneck(fifo <-chan sharedChunk) {
	line := make(chan sharedChunk, 1<<14)
	go p.deliver(line)
	var free time.Time
	for c := range fifo {
		if free.Before(c.at) {
			free = c.at
		}
		if p.bps > 0 {
			free = free.Add(time.Duration(float64(len(c.data)*8) / float64(p.bps) * float64(time.Second)))
			time.Sleep(time.Until(free))
		}
		c.at = free.Add(p.rtt / 2)
		line <- c
	}
}

func (p *sharedThrottle) deliver(line <-chan sharedChunk) {
	for c := range line {
		time.Sleep(time.Until(c.at))
		_, _ = c.dst.Write(c.data)
	}
}
