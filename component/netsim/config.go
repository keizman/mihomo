package netsim

import (
	"fmt"
	"sync"
)

// Config is the user facing net-sim configuration.
//
// Semantics: every value is what you measure end-to-end from the client,
// using a round trip (ping / request -> response) as the yardstick.
// The engine splits each value across the upload and download direction
// internally so the round-trip result equals the configured number:
//
//   - latency     : added RTT in ms (half on upload, half on download)
//   - jitter      : RTT varies within latency ± jitter (ms)
//   - loss        : round-trip loss rate in % (like ping's "packet loss")
//   - corruption  : round-trip rate in % of packets that arrive corrupted (UDP);
//     for TCP a corrupted segment is discarded by checksum, so it behaves as loss
//   - duplication : round-trip rate in % of packets that arrive duplicated (UDP)
//   - reordering  : round-trip rate in % of packets that arrive out of order (UDP)
//   - bandwidth   : per-direction throughput cap in Mbps (1 Mbps = 1,000,000 bit/s)
//   - burst-loss  : when > 0, losses come in runs of exactly N consecutive packets;
//     the long-run average loss still equals `loss`
//   - burst-delay : max extra RTT (ms) added while the link is in a "bad" period
//   - packet-size : path MTU in bytes; larger UDP datagrams are dropped,
//     TCP uses packet-size-40 as MSS when counting segments for loss
type Config struct {
	Enabled           bool    `yaml:"enabled" json:"enabled"`
	Latency           int     `yaml:"latency" json:"latency"`                       // ms, added RTT
	Jitter            int     `yaml:"jitter" json:"jitter"`                         // ms, RTT ± jitter
	Loss              float64 `yaml:"loss" json:"loss"`                             // %, round-trip loss
	Bandwidth         float64 `yaml:"bandwidth" json:"bandwidth"`                   // Mbps, symmetric
	UploadBandwidth   float64 `yaml:"upload-bandwidth" json:"upload-bandwidth"`     // Mbps, override upload
	DownloadBandwidth float64 `yaml:"download-bandwidth" json:"download-bandwidth"` // Mbps, override download
	Corruption        float64 `yaml:"corruption" json:"corruption"`                 // %, round-trip corruption
	Duplication       float64 `yaml:"duplication" json:"duplication"`               // %, round-trip duplication
	Reordering        float64 `yaml:"reordering" json:"reordering"`                 // %, round-trip reordering
	BurstLoss         int     `yaml:"burst-loss" json:"burst-loss"`                 // packets per loss burst
	BurstDelay        int     `yaml:"burst-delay" json:"burst-delay"`               // ms, max extra RTT in burst
	PacketSize        int     `yaml:"packet-size" json:"packet-size"`               // bytes, path MTU
	QueueType         string  `yaml:"queue-type" json:"queue-type"`                 // fifo / prio / fair-queue
}

func DefaultConfig() *Config {
	return &Config{
		QueueType: QueueFIFO,
	}
}

func (c *Config) GetUploadBandwidth() float64 {
	if c.UploadBandwidth > 0 {
		return c.UploadBandwidth
	}
	return c.Bandwidth
}

func (c *Config) GetDownloadBandwidth() float64 {
	if c.DownloadBandwidth > 0 {
		return c.DownloadBandwidth
	}
	return c.Bandwidth
}

func (c *Config) HasAnyEffect() bool {
	return c.Enabled && (c.Latency > 0 || c.Jitter > 0 || c.Loss > 0 ||
		c.Bandwidth > 0 || c.UploadBandwidth > 0 || c.DownloadBandwidth > 0 ||
		c.Corruption > 0 || c.Duplication > 0 || c.Reordering > 0 ||
		c.BurstDelay > 0 || c.PacketSize > 0)
}

// Validate checks value ranges and fills defaults.
func (c *Config) Validate() error {
	if c.Latency < 0 || c.Jitter < 0 || c.BurstLoss < 0 || c.BurstDelay < 0 || c.PacketSize < 0 {
		return fmt.Errorf("latency/jitter/burst-loss/burst-delay/packet-size must be >= 0")
	}
	if c.Bandwidth < 0 || c.UploadBandwidth < 0 || c.DownloadBandwidth < 0 {
		return fmt.Errorf("bandwidth values must be >= 0")
	}
	for name, v := range map[string]float64{
		"loss": c.Loss, "corruption": c.Corruption, "duplication": c.Duplication, "reordering": c.Reordering,
	} {
		if v < 0 || v > 100 {
			return fmt.Errorf("%s must be within [0, 100], got %v", name, v)
		}
	}
	if c.PacketSize > 0 && c.PacketSize < 68 {
		return fmt.Errorf("packet-size must be 0 (off) or >= 68, got %d", c.PacketSize)
	}
	if c.QueueType == "" {
		c.QueueType = QueueFIFO
	}
	if !ValidQueueType(c.QueueType) {
		return fmt.Errorf("invalid queue-type %q, must be: fifo, prio, or fair-queue", c.QueueType)
	}
	return nil
}

var (
	globalConfig = DefaultConfig()
	configMu     sync.RWMutex
)

func GetConfig() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return *globalConfig
}

func SetConfig(cfg *Config) {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig = cfg
}
