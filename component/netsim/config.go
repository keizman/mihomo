package netsim

import "sync"

type Config struct {
	Enabled           bool    `yaml:"enabled" json:"enabled"`
	Latency           int     `yaml:"latency" json:"latency"`                         // ms, base one-way delay
	Jitter            int     `yaml:"jitter" json:"jitter"`                            // ms, random variation
	Loss              float64 `yaml:"loss" json:"loss"`                                // %, independent random drop
	Bandwidth         float64 `yaml:"bandwidth" json:"bandwidth"`                      // Mbps, symmetric
	UploadBandwidth   float64 `yaml:"upload-bandwidth" json:"upload-bandwidth"`         // Mbps, override upload
	DownloadBandwidth float64 `yaml:"download-bandwidth" json:"download-bandwidth"`     // Mbps, override download
	Corruption        float64 `yaml:"corruption" json:"corruption"`                    // %, random bit corruption
	Duplication       float64 `yaml:"duplication" json:"duplication"`                  // %, random packet duplication
	Reordering        float64 `yaml:"reordering" json:"reordering"`                    // %, random reordering
	BurstLoss         int     `yaml:"burst-loss" json:"burst-loss"`                    // max consecutive drops
	BurstDelay        int     `yaml:"burst-delay" json:"burst-delay"`                  // ms, max burst delay
	PacketSize        int     `yaml:"packet-size" json:"packet-size"`                  // bytes, MTU
	QueueType         string  `yaml:"queue-type" json:"queue-type"`                    // fifo / prio / fair-queue
}

func DefaultConfig() *Config {
	return &Config{
		QueueType: "fifo",
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
		c.BurstLoss > 0 || c.BurstDelay > 0 || c.PacketSize > 0)
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
