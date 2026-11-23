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
		p.mu.Lock()
		p.conns = append(p.conns, in, out)
		p.mu.Unlock()
		go p.pipe(out, in)
		go p.pipe(in, out)
	}
}

type stamped struct {
	at   time.Time
	data []byte
}

// pipe copies src to dst: a reader stamps each chunk, a writer releases it
// rtt/2 later and paces it through a token bucket over bytes.
func (p *throttle) pipe(dst, src net.Conn) {
	queue := make(chan stamped, 256)
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
