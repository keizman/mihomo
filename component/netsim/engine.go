package netsim

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/log"
)

type Direction int

const (
	DirectionUpload   Direction = 0
	DirectionDownload Direction = 1
)

type Stats struct {
	TotalPackets     atomic.Int64 `json:"total-packets"`
	DroppedPackets   atomic.Int64 `json:"dropped-packets"`
	DelayedPackets   atomic.Int64 `json:"delayed-packets"`
	CorruptedPackets atomic.Int64 `json:"corrupted-packets"`
	DuplicatedPackets atomic.Int64 `json:"duplicated-packets"`
	ReorderedPackets atomic.Int64 `json:"reordered-packets"`
	BurstDrops       atomic.Int64 `json:"burst-drops"`
	BurstDelays      atomic.Int64 `json:"burst-delays"`
	ThrottledBytes   atomic.Int64 `json:"throttled-bytes"`
}

type Engine struct {
	mu            sync.RWMutex
	uploadLimiter *BandwidthLimiter
	downloadLimiter *BandwidthLimiter
	burstState    *BurstStateMachine
	stats         Stats
	rng           *rand.Rand
}

var (
	defaultEngine *Engine
	engineOnce    sync.Once
)

func getEngine() *Engine {
	engineOnce.Do(func() {
		defaultEngine = newEngine()
	})
	return defaultEngine
}

func newEngine() *Engine {
	return &Engine{
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		burstState: NewBurstStateMachine(),
	}
}

func Enabled() bool {
	cfg := GetConfig()
	return cfg.HasAnyEffect()
}

func UpdateConfig(cfg *Config) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	SetConfig(cfg)
	e := getEngine()
	e.mu.Lock()
	defer e.mu.Unlock()

	upBw := cfg.GetUploadBandwidth()
	downBw := cfg.GetDownloadBandwidth()
	if upBw > 0 {
		e.uploadLimiter = NewBandwidthLimiter(upBw)
	} else {
		e.uploadLimiter = nil
	}
	if downBw > 0 {
		e.downloadLimiter = NewBandwidthLimiter(downBw)
	} else {
		e.downloadLimiter = nil
	}

	e.burstState = NewBurstStateMachine()

	if cfg.Enabled {
		log.Infoln("[NetSim] enabled: latency=%dms jitter=%dms loss=%.2f%% bw=%.1fMbps corruption=%.2f%% dup=%.2f%% reorder=%.2f%%",
			cfg.Latency, cfg.Jitter, cfg.Loss, cfg.Bandwidth, cfg.Corruption, cfg.Duplication, cfg.Reordering)
	} else {
		log.Infoln("[NetSim] disabled")
	}
}

func GetStats() map[string]int64 {
	e := getEngine()
	return map[string]int64{
		"total-packets":      e.stats.TotalPackets.Load(),
		"dropped-packets":    e.stats.DroppedPackets.Load(),
		"delayed-packets":    e.stats.DelayedPackets.Load(),
		"corrupted-packets":  e.stats.CorruptedPackets.Load(),
		"duplicated-packets": e.stats.DuplicatedPackets.Load(),
		"reordered-packets":  e.stats.ReorderedPackets.Load(),
		"burst-drops":        e.stats.BurstDrops.Load(),
		"burst-delays":       e.stats.BurstDelays.Load(),
		"throttled-bytes":    e.stats.ThrottledBytes.Load(),
	}
}

func ResetStats() {
	e := getEngine()
	e.stats.TotalPackets.Store(0)
	e.stats.DroppedPackets.Store(0)
	e.stats.DelayedPackets.Store(0)
	e.stats.CorruptedPackets.Store(0)
	e.stats.DuplicatedPackets.Store(0)
	e.stats.ReorderedPackets.Store(0)
	e.stats.BurstDrops.Store(0)
	e.stats.BurstDelays.Store(0)
	e.stats.ThrottledBytes.Store(0)
}

func (e *Engine) shouldDrop(cfg *Config) bool {
	if cfg.BurstLoss > 0 {
		e.mu.Lock()
		drop := e.burstState.ShouldBurstDrop(cfg.Loss/100.0, cfg.BurstLoss)
		e.mu.Unlock()
		if drop {
			e.stats.BurstDrops.Add(1)
			return true
		}
		return false
	}
	if cfg.Loss > 0 {
		e.mu.Lock()
		drop := e.rng.Float64() < cfg.Loss/100.0
		e.mu.Unlock()
		return drop
	}
	return false
}

func (e *Engine) calcDelay(cfg *Config) time.Duration {
	base := cfg.Latency
	if base <= 0 && cfg.Jitter <= 0 && cfg.BurstDelay <= 0 {
		return 0
	}

	delay := base
	if cfg.Jitter > 0 {
		e.mu.Lock()
		delay += e.rng.Intn(cfg.Jitter*2+1) - cfg.Jitter
		e.mu.Unlock()
	}
	if cfg.BurstDelay > 0 {
		e.mu.Lock()
		extra := e.burstState.BurstDelayExtra(cfg.Loss/100.0, cfg.BurstDelay)
		e.mu.Unlock()
		if extra > 0 {
			delay += extra
			e.stats.BurstDelays.Add(1)
		}
	}
	if delay < 0 {
		delay = 0
	}
	return time.Duration(delay) * time.Millisecond
}

func (e *Engine) shouldDuplicate(cfg *Config) bool {
	if cfg.Duplication <= 0 {
		return false
	}
	e.mu.Lock()
	dup := e.rng.Float64() < cfg.Duplication/100.0
	e.mu.Unlock()
	return dup
}

func (e *Engine) shouldReorder(cfg *Config) bool {
	if cfg.Reordering <= 0 {
		return false
	}
	e.mu.Lock()
	reorder := e.rng.Float64() < cfg.Reordering/100.0
	e.mu.Unlock()
	return reorder
}

func (e *Engine) corruptData(data []byte, cfg *Config) {
	if cfg.Corruption <= 0 || len(data) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	totalBits := len(data) * 8
	bitsToFlip := int(float64(totalBits) * cfg.Corruption / 100.0)
	if bitsToFlip < 1 {
		bitsToFlip = 1
	}
	for i := 0; i < bitsToFlip; i++ {
		byteIdx := e.rng.Intn(len(data))
		bitIdx := uint(e.rng.Intn(8))
		data[byteIdx] ^= 1 << bitIdx
	}
	e.stats.CorruptedPackets.Add(1)
}

func (e *Engine) getLimiter(dir Direction) *BandwidthLimiter {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if dir == DirectionUpload {
		return e.uploadLimiter
	}
	return e.downloadLimiter
}
