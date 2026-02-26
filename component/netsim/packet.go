package netsim

import (
	"context"
	"time"
)

// SimulateUDPSend applies net-sim effects before sending a UDP packet.
// Returns: data to send (possibly corrupted/truncated), shouldDrop, shouldDuplicate.
func SimulateUDPSend(data []byte) (out []byte, drop bool, duplicate bool) {
	cfg := GetConfig()
	if !cfg.HasAnyEffect() {
		return data, false, false
	}
	e := getEngine()
	e.stats.TotalPackets.Add(1)

	// loss
	if e.shouldDrop(&cfg) {
		e.stats.DroppedPackets.Add(1)
		return nil, true, false
	}

	// latency + jitter + burst delay
	if delay := e.calcDelay(&cfg); delay > 0 {
		e.stats.DelayedPackets.Add(1)
		time.Sleep(delay)
	}

	// packet size truncation (MTU simulation)
	out = data
	if cfg.PacketSize > 0 && len(out) > cfg.PacketSize {
		out = out[:cfg.PacketSize]
	}

	// corruption: work on a copy
	if cfg.Corruption > 0 {
		dataCopy := make([]byte, len(out))
		copy(dataCopy, out)
		e.corruptData(dataCopy, &cfg)
		out = dataCopy
	}

	// bandwidth throttle (upload)
	if limiter := e.getLimiter(DirectionUpload); limiter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = limiter.Wait(ctx, len(out))
		cancel()
		e.stats.ThrottledBytes.Add(int64(len(out)))
	}

	// duplication
	duplicate = e.shouldDuplicate(&cfg)
	if duplicate {
		e.stats.DuplicatedPackets.Add(1)
	}

	return out, false, duplicate
}

// SimulateUDPRecv applies net-sim effects on received UDP packet data.
// Returns: data (possibly corrupted/truncated), shouldDrop.
func SimulateUDPRecv(data []byte) (out []byte, drop bool) {
	cfg := GetConfig()
	if !cfg.HasAnyEffect() {
		return data, false
	}
	e := getEngine()
	e.stats.TotalPackets.Add(1)

	// loss
	if e.shouldDrop(&cfg) {
		e.stats.DroppedPackets.Add(1)
		return nil, true
	}

	// latency + jitter + burst delay
	if delay := e.calcDelay(&cfg); delay > 0 {
		e.stats.DelayedPackets.Add(1)
		time.Sleep(delay)
	}

	out = data
	// packet size truncation
	if cfg.PacketSize > 0 && len(out) > cfg.PacketSize {
		out = out[:cfg.PacketSize]
	}

	// corruption
	if cfg.Corruption > 0 {
		dataCopy := make([]byte, len(out))
		copy(dataCopy, out)
		e.corruptData(dataCopy, &cfg)
		out = dataCopy
	}

	// bandwidth throttle (download)
	if limiter := e.getLimiter(DirectionDownload); limiter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = limiter.Wait(ctx, len(out))
		cancel()
		e.stats.ThrottledBytes.Add(int64(len(out)))
	}

	return out, false
}

// SimulateUDPReorder returns true if this packet should be delayed for reordering.
// The caller should buffer and send it later.
func SimulateUDPReorder() bool {
	cfg := GetConfig()
	if !cfg.HasAnyEffect() {
		return false
	}
	e := getEngine()
	if e.shouldReorder(&cfg) {
		e.stats.ReorderedPackets.Add(1)
		return true
	}
	return false
}
