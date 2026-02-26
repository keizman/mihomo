package netsim

import (
	"context"

	"golang.org/x/time/rate"
)

type BandwidthLimiter struct {
	limiter *rate.Limiter
	mbps    float64
}

func NewBandwidthLimiter(mbps float64) *BandwidthLimiter {
	if mbps <= 0 {
		return nil
	}
	bytesPerSec := mbps * 1024 * 1024 / 8
	burst := int(bytesPerSec / 10) // 100ms worth of burst
	if burst < 1500 {
		burst = 1500 // at least one MTU
	}
	return &BandwidthLimiter{
		limiter: rate.NewLimiter(rate.Limit(bytesPerSec), burst),
		mbps:    mbps,
	}
}

func (b *BandwidthLimiter) Wait(ctx context.Context, n int) error {
	if b == nil || n <= 0 {
		return nil
	}
	burst := b.limiter.Burst()
	for n > 0 {
		take := n
		if take > burst {
			take = burst
		}
		if err := b.limiter.WaitN(ctx, take); err != nil {
			return err
		}
		n -= take
	}
	return nil
}
