package netsim

import (
	"context"

	"golang.org/x/time/rate"
)

// BandwidthLimiter is a token bucket shared by every connection of one
// direction, so the configured value is the total link capacity.
type BandwidthLimiter struct {
	limiter *rate.Limiter
	mbps    float64
}

// NewBandwidthLimiter uses decimal megabits like speed test tools do:
// 1 Mbps = 1,000,000 bit/s = 125,000 byte/s.
func NewBandwidthLimiter(mbps float64) *BandwidthLimiter {
	if mbps <= 0 {
		return nil
	}
	bytesPerSec := mbps * 1_000_000 / 8
	burst := int(bytesPerSec / 50) // 20ms worth of burst
	if burst < 1500 {
		burst = 1500 // at least one MTU
	}
	return &BandwidthLimiter{
		limiter: rate.NewLimiter(rate.Limit(bytesPerSec), burst),
		mbps:    mbps,
	}
}

func (b *BandwidthLimiter) Wait(n int) {
	if b == nil || n <= 0 {
		return
	}
	burst := b.limiter.Burst()
	for n > 0 {
		take := n
		if take > burst {
			take = burst
		}
		_ = b.limiter.WaitN(context.Background(), take)
		n -= take
	}
}
