package netsim

import (
	"context"
	"net"
	"time"
)

// SimConn wraps a net.Conn to apply network simulation on TCP streams.
type SimConn struct {
	net.Conn
	dir Direction
}

func WrapConn(conn net.Conn, dir Direction) net.Conn {
	return &SimConn{Conn: conn, dir: dir}
}

func (c *SimConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil || n == 0 {
		return n, err
	}

	cfg := GetConfig()
	if !cfg.HasAnyEffect() {
		return n, nil
	}
	e := getEngine()
	e.stats.TotalPackets.Add(1)

	// loss: simulate by returning 0 bytes read (force re-read)
	if e.shouldDrop(&cfg) {
		e.stats.DroppedPackets.Add(1)
		// for TCP we skip this chunk, caller will retry
		return 0, nil
	}

	// latency + jitter + burst delay
	if delay := e.calcDelay(&cfg); delay > 0 {
		e.stats.DelayedPackets.Add(1)
		time.Sleep(delay)
	}

	// corruption
	if cfg.Corruption > 0 {
		e.corruptData(b[:n], &cfg)
	}

	return n, nil
}

func (c *SimConn) Write(b []byte) (int, error) {
	cfg := GetConfig()
	if !cfg.HasAnyEffect() {
		return c.Conn.Write(b)
	}
	e := getEngine()
	e.stats.TotalPackets.Add(1)

	// loss
	if e.shouldDrop(&cfg) {
		e.stats.DroppedPackets.Add(1)
		return len(b), nil // pretend we wrote it
	}

	// latency + jitter + burst delay
	if delay := e.calcDelay(&cfg); delay > 0 {
		e.stats.DelayedPackets.Add(1)
		time.Sleep(delay)
	}

	// corruption: work on a copy
	data := b
	if cfg.Corruption > 0 {
		dataCopy := make([]byte, len(b))
		copy(dataCopy, b)
		e.corruptData(dataCopy, &cfg)
		data = dataCopy
	}

	// Apply bandwidth shaping on write path using connection direction.
	// - inbound(DirectionDownload) Write: remote -> local (download)
	// - outbound(DirectionUpload) Write: local -> remote (upload)
	if limiter := e.getLimiter(c.dir); limiter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = limiter.Wait(ctx, len(data))
		cancel()
		e.stats.ThrottledBytes.Add(int64(len(data)))
	}

	n, err := c.Conn.Write(data)

	// duplication
	if err == nil && e.shouldDuplicate(&cfg) {
		e.stats.DuplicatedPackets.Add(1)
		_, _ = c.Conn.Write(data)
	}

	return n, err
}
