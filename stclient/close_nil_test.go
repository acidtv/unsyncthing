package stclient

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// Regression coverage for the BEP-disconnect crash: syncthing's
// protocol.Connection.Close(err) calls err.Error() unconditionally to embed
// the reason in the outgoing BEP Close message, so passing nil panics with
// "invalid memory address or nil pointer dereference" and the process aborts
// (SIGABRT). The Android app hit this every time the user disconnected.

type noopModel struct{}

func (m *noopModel) Index(_ protocol.Connection, _ *protocol.Index) error             { return nil }
func (m *noopModel) IndexUpdate(_ protocol.Connection, _ *protocol.IndexUpdate) error { return nil }
func (m *noopModel) Request(_ protocol.Connection, _ *protocol.Request) (protocol.RequestResponse, error) {
	return nil, protocol.ErrNoSuchFile
}
func (m *noopModel) ClusterConfig(_ protocol.Connection, _ *protocol.ClusterConfig) error { return nil }
func (m *noopModel) Closed(_ protocol.Connection, _ error)                                {}
func (m *noopModel) DownloadProgress(_ protocol.Connection, _ *protocol.DownloadProgress) error {
	return nil
}

type noopConnInfo struct{}

func (noopConnInfo) Type() string             { return "tcp" }
func (noopConnInfo) Transport() string        { return "tcp" }
func (noopConnInfo) IsLocal() bool            { return false }
func (noopConnInfo) RemoteAddr() net.Addr     { return &net.TCPAddr{} }
func (noopConnInfo) Priority() int            { return 0 }
func (noopConnInfo) String() string           { return "noop" }
func (noopConnInfo) Crypto() string           { return "tls" }
func (noopConnInfo) EstablishedAt() time.Time { return time.Time{} }
func (noopConnInfo) ConnectionID() string     { return "noop" }

func newTestConn(t *testing.T) protocol.Connection {
	t.Helper()
	id := protocol.NewDeviceID([]byte{1, 2, 3})
	r, w := io.Pipe()
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	// Don't Start() — we just want to exercise the send-Close path.
	return protocol.NewConnection(id, r, w, w, &noopModel{}, noopConnInfo{}, protocol.CompressionMetadata, nil, nil)
}

// Pins the upstream behaviour. If this ever stops panicking (e.g. syncthing
// fixes the nil-deref), the comment on errClientClose can be relaxed.
func TestProtocolCloseNilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected Close(nil) to panic — syncthing protocol may have been fixed; relax errClientClose if so")
		}
	}()
	newTestConn(t).Close(nil)
}

// Verifies the sentinel we actually pass survives Close.
func TestProtocolCloseSentinelSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Close(errClientClose) panicked: %v", r)
		}
	}()
	newTestConn(t).Close(errClientClose)
}
