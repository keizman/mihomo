package netsim

import (
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/log"
)

type Direction int

const (
	DirectionUpload   Direction = 0 // client -> remote
	DirectionDownload Direction = 1 // remote -> client
)

const (
	defaultMSS = 1460
	minRTO     = 200 * time.Millisecond

	burstDelayEnter = 0.05 // per-packet chance to enter a burst-delay period
	burstDelayExit  = 0.30 // per-packet chance to leave a burst-delay period

	reorderHoldTimeout = 50 * time.Millisecond
)

// params is an immutable snapshot derived from Config. Probabilities are
// already converted to per-direction values (0..1) so that the round trip
// result equals the configured percentage.
type params struct {
	cfg Config

	oneWay     time.Duration // latency / 2
	jitter     time.Duration // per-direction half width: jitter / 2
	burstDelay time.Duration // per-direction max extra: burst-delay / 2

	loss    float64
	corrupt float64
	dup     float64
	reorder float64

	burstLen   int     // > 1 means bursty loss
	burstEnter float64 // chance to start a burst so the average loss stays `loss`

	mtu int           // 0 = unlimited
	mss int           // TCP segment size used to count lost segments
	rto time.Duration // stall added to a TCP stream for a lost segment
}

// perDirection returns p such that 1-(1-p)^2 == roundTrip.
func perDirection(roundTripPercent float64) float64 {
	rt := roundTripPercent / 100
	if rt <= 0 {
		return 0
	}
	if rt >= 1 {
		return 1
	}
	return 1 - math.Sqrt(1-rt)
}

func newParams(cfg Config) *params {
	p := &params{cfg: cfg}
	if !cfg.HasAnyEffect() {
		return p
	}
	p.oneWay = time.Duration(cfg.Latency) * time.Millisecond / 2
	p.jitter = time.Duration(cfg.Jitter) * time.Millisecond / 2
	p.burstDelay = time.Duration(cfg.BurstDelay) * time.Millisecond / 2
	p.loss = perDirection(cfg.Loss)
	p.corrupt = perDirection(cfg.Corruption)
	p.dup = perDirection(cfg.Duplication)
	// Reordering. Two hops in series sometimes undo each other's swap, so a
	// round trip measures about 2r-2r^2 for a per-direction rate r. A held
	// datagram also makes the next one ineligible: hold chance q yields
	// q/(1+q) late packets, hence q = r/(1-r).
	if rt := cfg.Reordering / 100; rt > 0 {
		if rt > 0.5 {
			rt = 0.5
		}
		r := (1 - math.Sqrt(1-2*rt)) / 2
		p.reorder = r / (1 - r)
	}

	if cfg.BurstLoss > 1 && p.loss > 0 && p.loss < 1 {
		n := float64(cfg.BurstLoss)
		p.burstLen = cfg.BurstLoss
		// long-run loss of fixed-length bursts: n*q / (1 + (n-1)*q) == loss
		p.burstEnter = p.loss / (n - (n-1)*p.loss)
	}

	p.mss = defaultMSS
	if cfg.PacketSize > 0 {
		p.mtu = cfg.PacketSize
		if cfg.PacketSize-40 < p.mss {
			p.mss = cfg.PacketSize - 40
		}
		if p.mss < 28 {
			p.mss = 28
		}
	}

	// Linux style RTO: srtt + 4*rttvar, at least 200ms
	p.rto = time.Duration(cfg.Latency+2*cfg.Jitter) * time.Millisecond
	if p.rto < minRTO {
		p.rto = minRTO
	}
	return p
}

type dirState struct {
	limiter atomic.Pointer[BandwidthLimiter]

	mu             sync.Mutex
	burstRemaining int  // packets still to drop in the current loss burst
	delayBad       bool // inside a burst-delay period
}

type Stats struct {
	TotalPackets      atomic.Int64
	DroppedPackets    atomic.Int64
	DelayedPackets    atomic.Int64
	CorruptedPackets  atomic.Int64
	DuplicatedPackets atomic.Int64
	ReorderedPackets  atomic.Int64
	BurstDrops        atomic.Int64
	BurstDelays       atomic.Int64
	ThrottledBytes    atomic.Int64
	OversizeDrops     atomic.Int64
	TCPRetransmits    atomic.Int64
	UploadPackets     atomic.Int64
	DownloadPackets   atomic.Int64
	UploadDropped     atomic.Int64
	DownloadDropped   atomic.Int64
}

var (
	current atomic.Pointer[params]
	dirs    [2]dirState
	stats   Stats
)

func init() {
	current.Store(newParams(*DefaultConfig()))
}

func load() *params { return current.Load() }

func Enabled() bool {
	return load().cfg.HasAnyEffect()
}

func UpdateConfig(cfg *Config) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if cfg.QueueType == "" {
		cfg.QueueType = QueueFIFO
	}
	c := *cfg
	SetConfig(&c)

	var up, down *BandwidthLimiter
	if c.Enabled {
		up = NewBandwidthLimiter(c.GetUploadBandwidth())
		down = NewBandwidthLimiter(c.GetDownloadBandwidth())
	}
	dirs[DirectionUpload].limiter.Store(up)
	dirs[DirectionDownload].limiter.Store(down)
	for i := range dirs {
		dirs[i].mu.Lock()
		dirs[i].burstRemaining = 0
		dirs[i].delayBad = false
		dirs[i].mu.Unlock()
	}
	current.Store(newParams(c))

	if c.Enabled {
		log.Infoln("[NetSim] enabled: rtt+%dms ±%dms loss=%.3f%% up=%.3fMbps down=%.3fMbps corruption=%.3f%% dup=%.3f%% reorder=%.3f%% burst-loss=%d burst-delay=%dms packet-size=%d",
			c.Latency, c.Jitter, c.Loss, c.GetUploadBandwidth(), c.GetDownloadBandwidth(),
			c.Corruption, c.Duplication, c.Reordering, c.BurstLoss, c.BurstDelay, c.PacketSize)
	} else {
		log.Infoln("[NetSim] disabled")
	}
}

func GetStats() map[string]int64 {
	return map[string]int64{
		"total-packets":      stats.TotalPackets.Load(),
		"dropped-packets":    stats.DroppedPackets.Load(),
		"delayed-packets":    stats.DelayedPackets.Load(),
		"corrupted-packets":  stats.CorruptedPackets.Load(),
		"duplicated-packets": stats.DuplicatedPackets.Load(),
		"reordered-packets":  stats.ReorderedPackets.Load(),
		"burst-drops":        stats.BurstDrops.Load(),
		"burst-delays":       stats.BurstDelays.Load(),
		"throttled-bytes":    stats.ThrottledBytes.Load(),
		"oversize-drops":     stats.OversizeDrops.Load(),
		"tcp-retransmits":    stats.TCPRetransmits.Load(),
		"upload-packets":     stats.UploadPackets.Load(),
		"download-packets":   stats.DownloadPackets.Load(),
		"upload-dropped":     stats.UploadDropped.Load(),
		"download-dropped":   stats.DownloadDropped.Load(),
	}
}

func ResetStats() {
	for _, c := range []*atomic.Int64{
		&stats.TotalPackets, &stats.DroppedPackets, &stats.DelayedPackets, &stats.CorruptedPackets,
		&stats.DuplicatedPackets, &stats.ReorderedPackets, &stats.BurstDrops, &stats.BurstDelays,
		&stats.ThrottledBytes, &stats.OversizeDrops, &stats.TCPRetransmits, &stats.UploadPackets,
		&stats.DownloadPackets, &stats.UploadDropped, &stats.DownloadDropped,
	} {
		c.Store(0)
	}
}

func countPacket(dir Direction) {
	stats.TotalPackets.Add(1)
	if dir == DirectionUpload {
		stats.UploadPackets.Add(1)
	} else {
		stats.DownloadPackets.Add(1)
	}
}

func countDrop(dir Direction) {
	stats.DroppedPackets.Add(1)
	if dir == DirectionUpload {
		stats.UploadDropped.Add(1)
	} else {
		stats.DownloadDropped.Add(1)
	}
}

func chance(p float64) bool {
	return p > 0 && (p >= 1 || rand.Float64() < p)
}

// lost decides whether one packet (or TCP segment) is lost in direction dir.
func lost(p *params, dir Direction) bool {
	if p.loss <= 0 {
		return false
	}
	if p.burstLen <= 1 {
		return chance(p.loss)
	}
	s := &dirs[dir]
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.burstRemaining > 0 {
		s.burstRemaining--
		stats.BurstDrops.Add(1)
		return true
	}
	if chance(p.burstEnter) {
		s.burstRemaining = p.burstLen - 1
		stats.BurstDrops.Add(1)
		return true
	}
	return false
}

// oneWayDelay samples the delay for one packet in direction dir.
func oneWayDelay(p *params, dir Direction) time.Duration {
	d := p.oneWay
	if p.jitter > 0 {
		d += time.Duration(rand.Int63n(int64(2*p.jitter)+1)) - p.jitter
	}
	if p.burstDelay > 0 {
		s := &dirs[dir]
		s.mu.Lock()
		if s.delayBad {
			if chance(burstDelayExit) {
				s.delayBad = false
			}
		} else if chance(burstDelayEnter) {
			s.delayBad = true
		}
		bad := s.delayBad
		s.mu.Unlock()
		if bad {
			d += time.Duration(rand.Int63n(int64(p.burstDelay) + 1))
			stats.BurstDelays.Add(1)
		}
	}
	if d < 0 {
		d = 0
	}
	return d
}

// flipOneBit corrupts a copy of data by flipping a single random bit.
func flipOneBit(data []byte) []byte {
	out := make([]byte, len(data))
	copy(out, data)
	if len(out) > 0 {
		out[rand.Intn(len(out))] ^= 1 << uint(rand.Intn(8))
	}
	return out
}

// throttle blocks until the bandwidth limiter of dir admits n bytes.
func throttle(dir Direction, n int) {
	if l := dirs[dir].limiter.Load(); l != nil && n > 0 {
		l.Wait(n)
		stats.ThrottledBytes.Add(int64(n))
	}
}
