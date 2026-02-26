package netsim

import (
	"io"
	"net"
	"testing"
	"time"
)

type mockConn struct {
	readData []byte
	written  []byte
}

func (m *mockConn) Read(b []byte) (int, error) {
	if len(m.readData) == 0 {
		return 0, io.EOF
	}
	n := copy(b, m.readData)
	m.readData = m.readData[n:]
	return n, nil
}

func (m *mockConn) Write(b []byte) (int, error) {
	m.written = append(m.written, b...)
	return len(b), nil
}

func (m *mockConn) Close() error { return nil }

func (m *mockConn) LocalAddr() net.Addr { return mockAddr("local") }

func (m *mockConn) RemoteAddr() net.Addr { return mockAddr("remote") }

func (m *mockConn) SetDeadline(_ time.Time) error { return nil }

func (m *mockConn) SetReadDeadline(_ time.Time) error { return nil }

func (m *mockConn) SetWriteDeadline(_ time.Time) error { return nil }

type mockAddr string

func (a mockAddr) Network() string { return "mock" }

func (a mockAddr) String() string { return string(a) }

func TestDownloadBandwidthThrottleOnWrite(t *testing.T) {
	t.Cleanup(func() {
		UpdateConfig(DefaultConfig())
		ResetStats()
	})

	UpdateConfig(&Config{
		Enabled:           true,
		DownloadBandwidth: 1000,
	})
	ResetStats()

	conn := WrapConn(&mockConn{}, DirectionDownload)
	payload := make([]byte, 512)
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	stats := GetStats()
	if stats["throttled-bytes"] < int64(len(payload)) {
		t.Fatalf("expected throttled bytes >= %d, got %d", len(payload), stats["throttled-bytes"])
	}
}

