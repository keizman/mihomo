package netsim

import (
	"sync"
	"time"
)

// delayLine is a FIFO "wire": every item is released at its due time and
// due times never go backwards, so ordering is preserved while delays are
// pipelined (a 100ms delay does NOT cap throughput at one chunk per 100ms).
// A worker goroutine is started on demand and exits when the line is empty.
type delayLine struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   []delayItem
	last    time.Time
	pending int // bytes queued
	limit   int // max queued bytes before schedule blocks, 0 = unlimited
	running bool
	closed  bool
}

type delayItem struct {
	due  time.Time
	size int
	fn   func()
}

func newDelayLine(limit int) *delayLine {
	d := &delayLine{limit: limit}
	d.cond = sync.NewCond(&d.mu)
	return d
}

// schedule queues fn to run `delay` from now (or right after the previous
// item, whichever is later). extra is added after the ordering constraint and
// models a stall that also holds back everything queued behind it.
// It blocks while the line holds more than limit bytes. Returns false if the
// line was closed.
func (d *delayLine) schedule(delay, extra time.Duration, size int, fn func()) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for d.limit > 0 && d.pending > 0 && d.pending+size > d.limit && !d.closed {
		d.cond.Wait()
	}
	if d.closed {
		return false
	}
	d.pushLocked(delay, extra, size, fn)
	return true
}

func (d *delayLine) pushLocked(delay, extra time.Duration, size int, fn func()) {
	due := time.Now().Add(delay)
	if due.Before(d.last) {
		due = d.last
	}
	due = due.Add(extra)
	d.last = due
	d.items = append(d.items, delayItem{due: due, size: size, fn: fn})
	d.pending += size
	if !d.running {
		d.running = true
		go d.run()
	}
}

// closeAfter queues fn behind everything already scheduled and rejects any
// later schedule. It may be called more than once (e.g. CloseWrite + Close);
// each fn runs in call order.
func (d *delayLine) closeAfter(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	d.pushLocked(0, 0, 0, fn)
	d.cond.Broadcast()
}

// idle reports whether nothing is queued or being delivered, i.e. the
// caller may write directly without racing the worker.
func (d *delayLine) idle() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.running
}

func (d *delayLine) run() {
	for {
		d.mu.Lock()
		if len(d.items) == 0 {
			d.running = false
			d.mu.Unlock()
			return
		}
		it := d.items[0]
		d.mu.Unlock()

		if wait := time.Until(it.due); wait > 0 {
			time.Sleep(wait) // items behind are never due earlier
		}

		d.mu.Lock()
		d.items[0] = delayItem{}
		d.items = d.items[1:]
		d.pending -= it.size
		d.cond.Broadcast()
		d.mu.Unlock()

		it.fn()
	}
}
