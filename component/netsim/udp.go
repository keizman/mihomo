package netsim

import (
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

// UDPFlow simulates one direction of one UDP association. Datagrams are
// delivered asynchronously through a FIFO delay line, so latency does not
// limit the packet rate. Reordering is the only thing that changes order.
type UDPFlow struct {
	dir  Direction
	line *delayLine

	mu   sync.Mutex
	held *heldPacket

	err atomic.Pointer[error]
}

type heldPacket struct {
	due   time.Time
	fn    func()
	timer *time.Timer
}

func NewUDPFlow(dir Direction) *UDPFlow {
	return &UDPFlow{dir: dir, line: newDelayLine(0)}
}

// Err returns the first error returned by the send callback.
func (f *UDPFlow) Err() error {
	if e := f.err.Load(); e != nil {
		return *e
	}
	return nil
}

// Send applies net-sim to one datagram. data is copied, so the caller may
// release its buffer right after Send returns. send is called later (maybe
// twice when duplicated) from a single goroutine per flow.
func (f *UDPFlow) Send(data []byte, send func([]byte) error) {
	p := load()
	deliver := func(b []byte, dup bool) func() {
		return func() {
			if f.Err() != nil {
				return
			}
			err := send(b)
			if err == nil && dup {
				err = send(b)
			}
			if err != nil {
				f.err.CompareAndSwap(nil, &err)
			}
		}
	}

	if !p.cfg.HasAnyEffect() {
		buf := append([]byte(nil), data...)
		if f.line.idle() {
			deliver(buf, false)()
		} else {
			f.line.schedule(0, 0, len(buf), deliver(buf, false))
		}
		return
	}

	countPacket(f.dir)

	// path MTU: oversized datagrams are black-holed (DF set / no fragmentation)
	if p.mtu > 0 && len(data) > p.mtu {
		stats.OversizeDrops.Add(1)
		countDrop(f.dir)
		return
	}
	if lost(p, f.dir) {
		countDrop(f.dir)
		return
	}

	throttle(f.dir, len(data))

	var buf []byte
	if chance(p.corrupt) {
		buf = flipOneBit(data)
		stats.CorruptedPackets.Add(1)
	} else {
		buf = append([]byte(nil), data...)
	}
	dup := chance(p.dup)
	if dup {
		stats.DuplicatedPackets.Add(1)
	}
	delay := oneWayDelay(p, f.dir)
	if delay > 0 {
		stats.DelayedPackets.Add(1)
	}
	fn := deliver(buf, dup)

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.held == nil && chance(p.reorder) {
		// hold this datagram and release it right after the next one
		h := &heldPacket{due: time.Now().Add(delay), fn: fn}
		h.timer = time.AfterFunc(reorderHoldTimeout, func() { f.flushHeld(h) })
		f.held = h
		return
	}

	f.line.schedule(delay, 0, len(buf), fn)
	if h := f.held; h != nil {
		f.held = nil
		h.timer.Stop()
		f.line.schedule(0, 0, 0, h.fn) // due right after the packet above
		stats.ReorderedPackets.Add(1)
	}
}

// flushHeld releases a held datagram in order when no later one showed up.
func (f *UDPFlow) flushHeld(h *heldPacket) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held != h {
		return
	}
	f.held = nil
	f.line.schedule(time.Until(h.due), 0, 0, h.fn)
}

var upstreamFlows sync.Map // key -> *UDPFlow

// UpstreamFlow returns the upload flow bound to key (normally the remote
// PacketConn). It returns nil when net-sim is off and no flow exists yet.
func UpstreamFlow(key any) *UDPFlow {
	if key == nil || !reflect.TypeOf(key).Comparable() {
		if Enabled() {
			return NewUDPFlow(DirectionUpload)
		}
		return nil
	}
	if v, ok := upstreamFlows.Load(key); ok {
		return v.(*UDPFlow)
	}
	if !Enabled() {
		return nil
	}
	v, _ := upstreamFlows.LoadOrStore(key, NewUDPFlow(DirectionUpload))
	return v.(*UDPFlow)
}

// ReleaseFlow forgets the upload flow bound to key.
func ReleaseFlow(key any) {
	if key == nil || !reflect.TypeOf(key).Comparable() {
		return
	}
	upstreamFlows.Delete(key)
}
