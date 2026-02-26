package netsim

import (
	"math/rand"
	"time"
)

type burstPhase int

const (
	phaseGood burstPhase = iota
	phaseBad
)

type BurstStateMachine struct {
	phase         burstPhase
	consecutiveDrops int
	rng           *rand.Rand
}

func NewBurstStateMachine() *BurstStateMachine {
	return &BurstStateMachine{
		phase: phaseGood,
		rng:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// ShouldBurstDrop implements a 2-state Markov chain.
// In good state: transition to bad with probability = lossRate.
// In bad state: continue dropping up to maxConsecutive, then return to good.
func (b *BurstStateMachine) ShouldBurstDrop(lossRate float64, maxConsecutive int) bool {
	if maxConsecutive <= 0 {
		return false
	}
	switch b.phase {
	case phaseGood:
		if b.rng.Float64() < lossRate {
			b.phase = phaseBad
			b.consecutiveDrops = 1
			return true
		}
		return false
	case phaseBad:
		b.consecutiveDrops++
		if b.consecutiveDrops >= maxConsecutive {
			b.phase = phaseGood
			b.consecutiveDrops = 0
		}
		return true
	}
	return false
}

// BurstDelayExtra returns extra delay in ms when in bad state.
// Transition follows same Markov model as burst loss.
func (b *BurstStateMachine) BurstDelayExtra(transitionRate float64, maxDelayMs int) int {
	if maxDelayMs <= 0 {
		return 0
	}
	if transitionRate <= 0 {
		transitionRate = 0.05 // default 5% transition rate
	}
	switch b.phase {
	case phaseGood:
		if b.rng.Float64() < transitionRate {
			b.phase = phaseBad
			return b.rng.Intn(maxDelayMs + 1)
		}
		return 0
	case phaseBad:
		// 30% chance to return to good state each packet
		if b.rng.Float64() < 0.3 {
			b.phase = phaseGood
			return 0
		}
		return b.rng.Intn(maxDelayMs + 1)
	}
	return 0
}
