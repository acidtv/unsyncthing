package stclient

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/discover"
	"github.com/syncthing/syncthing/lib/protocol"
)

func TestPickTCP(t *testing.T) {
	tests := []struct {
		name     string
		in       []string
		want     string
		wantErr  bool
		wantSchm []string
	}{
		{
			name: "tcp first",
			in:   []string{"tcp://1.2.3.4:22000", "relay://r.example:22028"},
			want: "1.2.3.4:22000",
		},
		{
			name: "relay first then tcp",
			in:   []string{"relay://r.example:22028", "tcp://1.2.3.4:22000"},
			want: "1.2.3.4:22000",
		},
		{
			name:     "relay only",
			in:       []string{"relay://r.example:22028"},
			wantErr:  true,
			wantSchm: []string{"relay"},
		},
		{
			name:     "quic only",
			in:       []string{"quic://1.2.3.4:22000"},
			wantErr:  true,
			wantSchm: []string{"quic"},
		},
		{
			name:    "empty",
			in:      nil,
			wantErr: true,
		},
		{
			name: "tcp6 accepted",
			in:   []string{"tcp6://[2001:db8::1]:22000"},
			want: "[2001:db8::1]:22000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, schemes, err := pickTCP(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("addr: got %q want %q", got, tt.want)
			}
			if tt.wantSchm != nil && !sameSet(schemes, tt.wantSchm) {
				t.Errorf("schemes: got %v want %v", schemes, tt.wantSchm)
			}
		})
	}
}

func TestRewriteUnspecified(t *testing.T) {
	src := &net.UDPAddr{IP: net.ParseIP("192.168.1.42"), Port: 12345}
	got := rewriteUnspecified([]string{
		"tcp://0.0.0.0:22000",
		"tcp://1.2.3.4:22000",
		"tcp://[::]:22000",
	}, src)
	want := []string{
		"tcp://192.168.1.42:22000",
		"tcp://1.2.3.4:22000",
		"tcp://192.168.1.42:22000",
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestLookupLocal(t *testing.T) {
	// Pick a free UDP port for the listener; we'll send the announcement
	// to it from a separate UDP socket on loopback.
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()

	// Fake peer ID — synthesised from random bytes.
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i)
	}
	peerID := protocol.DeviceID(raw)

	ann := discover.Announce{
		ID:         peerID,
		Addresses:  []string{"tcp://0.0.0.0:22000", "tcp://10.0.0.1:22000"},
		InstanceID: 42,
	}
	body, err := ann.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	pkt := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(pkt[:4], discover.Magic)
	copy(pkt[4:], body)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resultCh := make(chan []string, 1)
	errCh := make(chan error, 1)
	go func() {
		addrs, err := lookupLocalOn(ctx, peerID, port)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- addrs
	}()

	// Give the listener time to bind, then send.
	time.Sleep(100 * time.Millisecond)
	sender, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if _, err := sender.Write(pkt); err != nil {
		t.Fatal(err)
	}

	select {
	case addrs := <-resultCh:
		// The unspecified address should be rewritten to 127.0.0.1 (the
		// loopback sender). The explicit one passes through.
		if len(addrs) != 2 {
			t.Fatalf("got %d addrs: %v", len(addrs), addrs)
		}
		if addrs[0] != "tcp://127.0.0.1:22000" {
			t.Errorf("addr[0]: got %q want tcp://127.0.0.1:22000", addrs[0])
		}
		if addrs[1] != "tcp://10.0.0.1:22000" {
			t.Errorf("addr[1]: got %q want tcp://10.0.0.1:22000", addrs[1])
		}
	case err := <-errCh:
		t.Fatalf("lookupLocal failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lookup")
	}
}

func TestLookupLocalIgnoresWrongMagic(t *testing.T) {
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()

	var raw [32]byte
	raw[0] = 1
	peerID := protocol.DeviceID(raw)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := lookupLocalOn(ctx, peerID, port)
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond)
	sender, _ := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	defer sender.Close()
	// Wrong magic bytes — should be ignored.
	sender.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00})

	select {
	case err := <-errCh:
		if err != context.DeadlineExceeded {
			t.Errorf("want DeadlineExceeded, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lookup didn't exit after ctx timeout")
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}
