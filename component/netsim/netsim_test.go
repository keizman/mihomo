package netsim

import (
	"bytes"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mnet "github.com/metacubex/mihomo/common/net"
)

func setCfg(t *testing.T, cfg Config) {
	t.Helper()
	cfg.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	UpdateConfig(&cfg)
	ResetStats()
	t.Cleanup(func() {
		UpdateConfig(DefaultConfig())
		ResetStats()
	})
}

// relayPair builds client <-> [SimConn relay] <-> server over loopback TCP,
// mirroring tunnel.handleSocket.
func relayPair(t *testing.T, server func(net.Conn)) net.Conn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go server(c)
		}
	}()
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proxyLn.Close() })
	go func() {
		for {
			in, err := proxyLn.Accept()
			if err != nil {
				return
			}
			out, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				in.Close()
				continue
			}
			go mnet.Relay(WrapConn(in, DirectionDownload), WrapConn(out, DirectionUpload))
		}
	}()
	c, err := net.Dial("tcp", proxyLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func echo(c net.Conn) { io.Copy(c, c); c.Close() }

func TestPerDirection(t *testing.T) {
	for _, rt := range []float64{0.5, 1, 10, 50} {
		p := perDirection(rt)
		got := (1 - (1-p)*(1-p)) * 100
		if math.Abs(got-rt) > 1e-9 {
			t.Fatalf("rt=%v got %v", rt, got)
		}
	}
}

func TestTCPLatencyEqualsConfiguredRTT(t *testing.T) {
	setCfg(t, Config{Latency: 200})
	c := relayPair(t, echo)
	buf := make([]byte, 4)
	c.Write([]byte("ping")) // warm up
	io.ReadFull(c, buf)
	var total time.Duration
	const n = 5
	for i := 0; i < n; i++ {
		start := time.Now()
		c.Write([]byte("ping"))
		if _, err := io.ReadFull(c, buf); err != nil {
			t.Fatal(err)
		}
		total += time.Since(start)
	}
	avg := total / n
	t.Logf("avg RTT %v", avg)
	if avg < 195*time.Millisecond || avg > 230*time.Millisecond {
		t.Fatalf("RTT %v, want ~200ms", avg)
	}
}

func TestTCPLatencyDoesNotCapThroughput(t *testing.T) {
	setCfg(t, Config{Latency: 200})
	const size = 32 << 20
	c := relayPair(t, func(c net.Conn) {
		c.Write(make([]byte, size))
		c.Close()
	})
	start := time.Now()
	n, _ := io.Copy(io.Discard, c)
	el := time.Since(start)
	t.Logf("%d bytes in %v", n, el)
	if n != size {
		t.Fatalf("got %d bytes", n)
	}
	if el > 3*time.Second {
		t.Fatalf("latency serialised the stream: %v", el)
	}
}

func TestTCPDownloadBandwidth(t *testing.T) {
	setCfg(t, Config{DownloadBandwidth: 8}) // 1,000,000 B/s
	const size = 2_000_000
	c := relayPair(t, func(c net.Conn) {
		c.Write(make([]byte, size))
		c.Close()
	})
	start := time.Now()
	n, _ := io.Copy(io.Discard, c)
	el := time.Since(start).Seconds()
	mbps := float64(n) * 8 / el / 1e6
	t.Logf("%d bytes in %.3fs = %.2f Mbps", n, el, mbps)
	if mbps < 7.5 || mbps > 8.5 {
		t.Fatalf("got %.2f Mbps want 8", mbps)
	}
}

func TestTCPUploadBandwidth(t *testing.T) {
	setCfg(t, Config{Bandwidth: 50, UploadBandwidth: 4})
	const size = 1_000_000
	done := make(chan time.Time, 1)
	c := relayPair(t, func(c net.Conn) {
		io.CopyN(io.Discard, c, size)
		done <- time.Now()
		c.Close()
	})
	start := time.Now()
	c.Write(make([]byte, size))
	el := (<-done).Sub(start).Seconds()
	mbps := size * 8 / el / 1e6
	t.Logf("upload %.2f Mbps", mbps)
	if mbps < 3.7 || mbps > 4.3 {
		t.Fatalf("got %.2f Mbps want 4", mbps)
	}
}

func TestTCPLossKeepsDataIntact(t *testing.T) {
	setCfg(t, Config{Loss: 5, Corruption: 5, Duplication: 50, Reordering: 50})
	payload := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1MB
	c := relayPair(t, func(c net.Conn) {
		c.Write(payload)
		c.Close()
	})
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("stream altered: got %d bytes", len(got))
	}
	if GetStats()["tcp-retransmits"] == 0 {
		t.Fatal("expected retransmission stalls")
	}
}

func TestTCPCloseDeliversQueuedData(t *testing.T) {
	setCfg(t, Config{Latency: 300})
	c := relayPair(t, func(c net.Conn) {
		c.Write([]byte("hello"))
		c.Close()
	})
	start := time.Now()
	got, _ := io.ReadAll(c)
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
	if el := time.Since(start); el < 140*time.Millisecond {
		t.Fatalf("not delayed: %v", el)
	}
}

// udpRoundTrip sends n datagrams through an upload flow, echoes them through
// a download flow and returns what came back.
func udpRoundTrip(n int, gap time.Duration) [][]byte {
	up, down := NewUDPFlow(DirectionUpload), NewUDPFlow(DirectionDownload)
	var mu sync.Mutex
	var back [][]byte
	for i := 0; i < n; i++ {
		pkt := []byte{byte(i >> 16), byte(i >> 8), byte(i), 0xAA, 0x55, 0xAA, 0x55, 0xAA}
		up.Send(pkt, func(b []byte) error {
			down.Send(b, func(b []byte) error {
				mu.Lock()
				back = append(back, append([]byte(nil), b...))
				mu.Unlock()
				return nil
			})
			return nil
		})
		if gap > 0 {
			time.Sleep(gap)
		}
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	return back
}

func within(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	t.Logf("%s: got %.3f%% want %.3f%%", name, got, want)
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %.3f%% want %.3f%% ±%.2f", name, got, want, tol)
	}
}

func TestUDPRoundTripLoss(t *testing.T) {
	setCfg(t, Config{Loss: 10})
	const n = 50000
	back := udpRoundTrip(n, 0)
	within(t, "loss", 100*float64(n-len(back))/n, 10, 0.6)
}

func TestUDPBurstLossAverage(t *testing.T) {
	setCfg(t, Config{Loss: 6, BurstLoss: 4})
	const n = 80000
	back := udpRoundTrip(n, 0)
	within(t, "burst loss", 100*float64(n-len(back))/n, 6, 0.8)
}

func TestUDPCorruption(t *testing.T) {
	setCfg(t, Config{Corruption: 4})
	const n = 50000
	back := udpRoundTrip(n, 0)
	want := []byte{byte(0), 0, 0, 0xAA, 0x55, 0xAA, 0x55, 0xAA}
	corrupted := 0
	for i, b := range back {
		_ = i
		seq := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
		want[0], want[1], want[2] = byte(seq>>16), byte(seq>>8), byte(seq)
		if !bytes.Equal(b[3:], want[3:]) || seq >= n {
			corrupted++
		}
	}
	// a bit flip in the 3 seq bytes is only caught when it leaves [0,n)
	t.Logf("corrupted-packets stat=%d", GetStats()["corrupted-packets"])
	if c := GetStats()["corrupted-packets"]; math.Abs(float64(c)/n*100-4) > 0.4 {
		t.Fatalf("corrupted stat %d", c)
	}
	if corrupted == 0 {
		t.Fatal("no corruption observed")
	}
}

func TestUDPDuplication(t *testing.T) {
	setCfg(t, Config{Duplication: 3})
	const n = 50000
	back := udpRoundTrip(n, 0)
	seen := map[int]int{}
	for _, b := range back {
		seen[int(b[0])<<16|int(b[1])<<8|int(b[2])]++
	}
	dups := 0
	for _, c := range seen {
		if c > 1 {
			dups++
		}
	}
	within(t, "duplication", 100*float64(dups)/n, 3, 0.4)
}

func TestUDPReordering(t *testing.T) {
	setCfg(t, Config{Reordering: 10})
	const n = 4000
	back := udpRoundTrip(n, 200*time.Microsecond)
	if len(back) != n {
		t.Fatalf("lost packets: %d/%d", len(back), n)
	}
	late, maxSeen := 0, -1
	for _, b := range back {
		seq := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
		if seq < maxSeen {
			late++
		} else {
			maxSeen = seq
		}
	}
	within(t, "reordering", 100*float64(late)/n, 10, 1.5)
}

func TestUDPLatencyAndJitter(t *testing.T) {
	setCfg(t, Config{Latency: 100, Jitter: 40})
	up, down := NewUDPFlow(DirectionUpload), NewUDPFlow(DirectionDownload)
	var mu sync.Mutex
	var rtts []time.Duration
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		start := time.Now()
		up.Send([]byte{1}, func(b []byte) error {
			down.Send(b, func([]byte) error {
				mu.Lock()
				rtts = append(rtts, time.Since(start))
				mu.Unlock()
				wg.Done()
				return nil
			})
			return nil
		})
		time.Sleep(150 * time.Millisecond) // one in flight at a time
	}
	wg.Wait()
	lo, hi, sum := time.Hour, time.Duration(0), time.Duration(0)
	for _, r := range rtts {
		sum += r
		if r < lo {
			lo = r
		}
		if r > hi {
			hi = r
		}
	}
	avg := sum / time.Duration(len(rtts))
	t.Logf("rtt avg=%v min=%v max=%v", avg, lo, hi)
	if avg < 85*time.Millisecond || avg > 115*time.Millisecond || lo < 58*time.Millisecond || hi > 145*time.Millisecond {
		t.Fatalf("rtt out of range avg=%v min=%v max=%v", avg, lo, hi)
	}
}

func TestUDPPacketSizeDropsOversize(t *testing.T) {
	setCfg(t, Config{PacketSize: 100})
	f := NewUDPFlow(DirectionUpload)
	var got atomic.Int32
	f.Send(make([]byte, 100), func([]byte) error { got.Add(1); return nil })
	f.Send(make([]byte, 101), func([]byte) error { got.Add(1); return nil })
	time.Sleep(50 * time.Millisecond)
	if got.Load() != 1 || GetStats()["oversize-drops"] != 1 {
		t.Fatalf("delivered=%d stats=%v", got.Load(), GetStats())
	}
}

func TestValidate(t *testing.T) {
	bad := []Config{{Loss: -1}, {Loss: 101}, {Latency: -5}, {QueueType: "x"}, {PacketSize: 10}}
	for _, c := range bad {
		if c.Validate() == nil {
			t.Fatalf("expected error for %+v", c)
		}
	}
}
