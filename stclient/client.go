// Package stclient is a gomobile-compatible Syncthing BEP client.
// Build the Android AAR with: gomobile bind -target android -javapkg com.acidtv.unsyncthing -o stclient.aar .
package stclient

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

const (
	clientName       = "unsyncthing"
	clientVersion    = "v0.1.0"
	dialTimeout      = 30 * time.Second
	handshakeTimeout = 30 * time.Second
)

// Client manages a single BEP connection to a Syncthing peer.
// Safe for concurrent use after Connect.
type Client struct {
	mu    sync.Mutex
	myID  protocol.DeviceID
	cert  tls.Certificate
	conn  protocol.Connection
	model *peerModel
}

// NewClient creates a Client from PEM-encoded certificate and private key.
// Use GenerateCert() to create them on first run, then persist to storage.
func NewClient(certPEM, keyPEM string) (*Client, error) {
	cert, err := loadCert(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse cert: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("certificate chain is empty")
	}
	return &Client{
		myID: protocol.NewDeviceID(cert.Certificate[0]),
		cert: cert,
	}, nil
}

// DeviceID returns our device ID. Share this string with the remote peer so
// it can authorise our connection in its Syncthing settings.
func (c *Client) DeviceID() string {
	return c.myID.String()
}

// Connect resolves peerDeviceIDStr via global + LAN discovery, dials the
// peer, and establishes an authenticated BEP session.
// Idempotent: any previous connection on this Client is closed first.
func (c *Client) Connect(peerDeviceIDStr, folderIDs string) error {
	peerID, err := protocol.DeviceIDFromString(peerDeviceIDStr)
	if err != nil {
		return fmt.Errorf("invalid peer device ID: %w", err)
	}

	addr, err := Discover(c.myID.String(), peerDeviceIDStr, 8)
	if err != nil {
		return fmt.Errorf("discover peer: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Close any prior connection before opening a new one.
	if c.conn != nil {
		c.conn.Close(nil)
		c.conn = nil
		c.model = nil
	}

	tlsConf := &tls.Config{
		Certificates:       []tls.Certificate{c.cert},
		InsecureSkipVerify: true, // device-ID verification replaces hostname verification
		NextProtos:         []string{"bep/1.0"},
		MinVersion:         tls.VersionTLS12,
	}

	netConn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	// Bound the TLS handshake + BEP hello with an absolute deadline.
	netConn.SetDeadline(time.Now().Add(handshakeTimeout))

	tlsConn := tls.Client(netConn, tlsConf)
	if err := tlsConn.Handshake(); err != nil {
		netConn.Close()
		return fmt.Errorf("TLS handshake: %w", err)
	}

	if err := verifyPeerDeviceID(tlsConn, peerID); err != nil {
		tlsConn.Close()
		return err
	}

	// BEP Hello exchange — protocol.NewConnection does NOT do this.
	// See syncthing/lib/protocol/hello.go.
	if _, err := protocol.ExchangeHello(tlsConn, protocol.Hello{
		DeviceName:    clientName,
		ClientName:    clientName,
		ClientVersion: clientVersion,
		Timestamp:     time.Now().UnixNano(),
	}); err != nil {
		tlsConn.Close()
		return fmt.Errorf("BEP hello: %w", err)
	}

	// Past the handshake phase — clear the deadline.
	netConn.SetDeadline(time.Time{})

	folders := splitFolderIDs(folderIDs)
	model := newPeerModel()
	// When the BEP connection dies (peer idle timeout, NAT churn, network blip)
	// the protocol package calls Closed() on the model. Clear our reference so
	// the next FetchFile sees IsConnected()==false and can trigger a reconnect
	// instead of trying to use a dead conn and surfacing "connection closed".
	// Done from a goroutine so we never deadlock if Closed fires while Close()
	// is being driven from a code path that already holds c.mu.
	model.setOnClosed(func(_ error) {
		go func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.model == model {
				c.conn = nil
				c.model = nil
			}
		}()
	})
	conn := protocol.NewConnection(
		peerID,
		tlsConn, tlsConn, tlsConn,
		model,
		&tlsConnInfo{conn: tlsConn, addr: addr, establishedAt: time.Now()},
		protocol.CompressionMetadata,
		nil, nil,
	)
	conn.Start()
	// Advertise our cluster config so the peer sends its Index.
	// Folder.Devices MUST include both our ID and the peer's ID,
	// otherwise the peer rejects with errMissingLocalInClusterConfig.
	conn.ClusterConfig(buildClusterConfig(c.myID, peerID, folders))

	c.conn = conn
	c.model = model
	return nil
}

// WaitForIndex blocks until the file index for folderID has settled (no more
// updates for a short quiet period) or until timeoutSecs seconds elapse.
// Returns successfully with whatever partial index has arrived if the timeout
// is reached but at least some data was received.
func (c *Client) WaitForIndex(folderID string, timeoutSecs int) error {
	_, model := c.snapshot()
	if model == nil {
		return fmt.Errorf("not connected")
	}
	return model.waitForIndex(folderID, time.Duration(timeoutSecs)*time.Second)
}

// IsConnected reports whether the BEP connection is currently live.
// Returns false after Close() or once the peer (or the network) has dropped us.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Close shuts down the connection. Safe to call multiple times.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close(nil)
		c.conn = nil
		c.model = nil
	}
}

// snapshot returns the current connection and model atomically.
func (c *Client) snapshot() (protocol.Connection, *peerModel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn, c.model
}

func verifyPeerDeviceID(conn *tls.Conn, expected protocol.DeviceID) error {
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fmt.Errorf("peer presented no certificate")
	}
	got := protocol.NewDeviceID(certs[0].Raw)
	if got != expected {
		return fmt.Errorf("device ID mismatch: got %s, want %s", got, expected)
	}
	return nil
}

func splitFolderIDs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if id := strings.TrimSpace(part); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func buildClusterConfig(myID, peerID protocol.DeviceID, folderIDs []string) protocol.ClusterConfig {
	folders := make([]protocol.Folder, len(folderIDs))
	for i, id := range folderIDs {
		folders[i] = protocol.Folder{
			ID: id,
			// Both devices must appear in Devices, or the peer rejects.
			Devices: []protocol.Device{
				{ID: myID},
				{ID: peerID},
			},
		}
	}
	return protocol.ClusterConfig{Folders: folders}
}

// tlsConnInfo implements protocol.ConnectionInfo for a raw TLS connection.
type tlsConnInfo struct {
	conn          *tls.Conn
	addr          string
	establishedAt time.Time
}

func (i *tlsConnInfo) Type() string             { return "tcp" }
func (i *tlsConnInfo) Transport() string        { return "tcp" }
func (i *tlsConnInfo) IsLocal() bool            { return false }
func (i *tlsConnInfo) RemoteAddr() net.Addr     { return i.conn.RemoteAddr() }
func (i *tlsConnInfo) Priority() int            { return 0 }
func (i *tlsConnInfo) String() string           { return i.addr }
func (i *tlsConnInfo) Crypto() string           { return "tls" }
func (i *tlsConnInfo) EstablishedAt() time.Time { return i.establishedAt }
func (i *tlsConnInfo) ConnectionID() string     { return i.addr }
