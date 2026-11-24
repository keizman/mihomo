package netsim

import (
	"net"
	"sync"
	"time"

	"github.com/metacubex/sing/common"
	N "github.com/metacubex/sing/common/network"
)

// maxQueuedBytes bounds the bytes "in flight" on the simulated wire per
// connection direction (acts like a TCP window, gives back-pressure).
const maxQueuedBytes = 4 << 20

// SimConn wraps the *destination* side of one relay direction.
//
// Only Write is simulated, so every byte crosses the simulator exactly once
// per direction:
//   - inbound  (client side)  Write = download (remote -> client)
//   - outbound (remote side)  Write = upload   (client -> remote)
//
// Read is passed through untouched.
//
// TCP is a reliable byte stream, so packet-level effects are translated into
// what an application would observe on a real lossy link:
//   - loss / corruption : each lost (or checksum-failed) segment stalls the
//     stream by one RTO (retransmission), data is never dropped or altered
//   - duplication / reordering : absorbed by the TCP stack, no visible effect
type SimConn struct {
	net.Conn
	dir  Direction
	line *delayLine

	errMu sync.Mutex
	err   error

	closeOnce      sync.Once
	closeWriteOnce sync.Once
}

func WrapConn(conn net.Conn, dir Direction) net.Conn {
	return &SimConn{
		Conn: conn,
		dir:  dir,
		line: newDelayLine(maxQueuedBytes),
	}
}

func (c *SimConn) setErr(err error) {
	c.errMu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.errMu.Unlock()
}

func (c *SimConn) getErr() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

func (c *SimConn) Write(b []byte) (int, error) {
	if err := c.getErr(); err != nil {
		return 0, err
	}
	p := load()
	if !p.cfg.HasAnyEffect() && c.line.idle() {
		// net-sim switched off while this connection was open
		return c.Conn.Write(b)
	}
	n := len(b)
	if n == 0 {
		return 0, nil
	}
	countPacket(c.dir)

	// 1. serialization on the bottleneck link (blocks -> real back-pressure)
	throttle(c.dir, n)

	// 2. propagation delay (+ jitter + burst delay) and retransmission stalls
	var delay, stall time.Duration
	if p.cfg.HasAnyEffect() {
		delay = oneWayDelay(p, c.dir)
		if delay > 0 {
			stats.DelayedPackets.Add(1)
		}
		stall = tcpLossStall(p, c.dir, n)
	}

	data := make([]byte, n)
	copy(data, b)
	ok := c.line.schedule(delay, stall, n, func() {
		if c.getErr() != nil {
			return
		}
		if _, err := c.Conn.Write(data); err != nil {
			c.setErr(err)
		}
	})
	if !ok {
		return 0, net.ErrClosed
	}
	return n, nil
}

// tcpLossStall returns the retransmission stall for a chunk of n bytes.
// Every MSS-sized segment is subject to loss and corruption; any loss costs
// one RTO for the chunk (SACK recovers several holes in one round).
func tcpLossStall(p *params, dir Direction, n int) time.Duration {
	if p.loss <= 0 && p.corrupt <= 0 {
		return 0
	}
	segments := (n + p.mss - 1) / p.mss
	hit := false
	for i := 0; i < segments; i++ {
		if lost(p, dir) {
			countDrop(dir)
			hit = true
		} else if chance(p.corrupt) {
			stats.CorruptedPackets.Add(1)
			hit = true
		}
	}
	if !hit {
		return 0
	}
	stats.TCPRetransmits.Add(1)
	return p.rto
}

// CloseWrite half-closes after all queued data has been delivered.
func (c *SimConn) CloseWrite() error {
	c.closeWriteOnce.Do(func() {
		c.line.closeAfter(func() {
			// same rule as sing's CopyConn: half-close when supported
			if cw, ok := common.Cast[N.WriteCloser](c.Conn); ok {
				_ = cw.CloseWrite()
			} else {
				_ = c.Conn.Close()
			}
		})
	})
	return nil
}

// Close does not block: data already on the simulated wire is still
// delivered at its due time, then the underlying connection is closed.
// After a write error the connection is closed immediately.
func (c *SimConn) Close() error {
	c.closeOnce.Do(func() {
		if c.getErr() != nil {
			c.line.closeAfter(func() {})
			_ = c.Conn.Close()
			return
		}
		c.line.closeAfter(func() { _ = c.Conn.Close() })
	})
	return nil
}
