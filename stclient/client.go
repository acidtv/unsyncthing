package stclient

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// Client manages a single BEP connection to a Syncthing peer.
// Create one per peer; safe to call from multiple goroutines after Connect.
type Client struct {
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

// Connect dials addr (host:port) and establishes an authenticated BEP session.
// peerDeviceID must match the remote peer's certificate.
// folderIDs is a comma-separated list of folder IDs to request from the peer
// (find them in the peer's Syncthing web UI under each folder's edit dialog).
func (c *Client) Connect(addr, peerDeviceIDStr, folderIDs string) error {
	peerID, err := protocol.DeviceIDFromString(peerDeviceIDStr)
	if err != nil {
		return fmt.Errorf("invalid peer device ID: %w", err)
	}

	tlsConf := &tls.Config{
		Certificates:       []tls.Certificate{c.cert},
		InsecureSkipVerify: true, // device-ID verification replaces hostname verification
		NextProtos:         []string{"bep/1.0"},
		MinVersion:         tls.VersionTLS12,
	}

	netConn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	tlsConn := tls.Client(netConn, tlsConf)
	if err := tlsConn.Handshake(); err != nil {
		netConn.Close()
		return fmt.Errorf("TLS handshake: %w", err)
	}

	if err := verifyPeerDeviceID(tlsConn, peerID); err != nil {
		tlsConn.Close()
		return err
	}

	folders := splitFolderIDs(folderIDs)
	c.model = newPeerModel(folders)

	// tls.Conn satisfies io.Reader, io.Writer, and io.Closer separately.
	c.conn = protocol.NewConnection(
		peerID,
		tlsConn,                   // io.Reader
		tlsConn,                   // io.Writer
		tlsConn,                   // io.Closer
		c.model,
		&tlsConnInfo{tlsConn, addr},
		protocol.CompressionMetadata,
		nil,  // folder passwords
		nil,  // key generator
	)
	c.conn.Start()

	// Advertise the same folders back so the peer sends us their index.
	c.conn.ClusterConfig(buildClusterConfig(c.myID, folders))
	return nil
}

// WaitForIndex blocks until the file index for folderID has been received from
// the peer, or until timeoutSecs seconds elapse. Call this before ListFolder.
func (c *Client) WaitForIndex(folderID string, timeoutSecs int) error {
	if c.model == nil {
		return fmt.Errorf("not connected")
	}
	return c.model.waitForIndex(folderID, time.Duration(timeoutSecs)*time.Second)
}

// Close shuts down the connection cleanly.
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close(nil)
	}
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

func buildClusterConfig(myID protocol.DeviceID, folderIDs []string) protocol.ClusterConfig {
	folders := make([]protocol.Folder, len(folderIDs))
	for i, id := range folderIDs {
		folders[i] = protocol.Folder{
			ID:      id,
			Devices: []protocol.Device{{ID: myID}},
		}
	}
	return protocol.ClusterConfig{Folders: folders}
}

// tlsConnInfo implements protocol.ConnectionInfo for a raw TLS connection.
type tlsConnInfo struct {
	conn *tls.Conn
	addr string
}

func (i *tlsConnInfo) Type() string            { return "tcp" }
func (i *tlsConnInfo) Transport() string       { return "tcp" }
func (i *tlsConnInfo) IsLocal() bool           { return false }
func (i *tlsConnInfo) RemoteAddr() net.Addr    { return i.conn.RemoteAddr() }
func (i *tlsConnInfo) Priority() int           { return 0 }
func (i *tlsConnInfo) String() string          { return i.addr }
func (i *tlsConnInfo) Crypto() string          { return "tls" }
func (i *tlsConnInfo) EstablishedAt() time.Time { return time.Now() }
func (i *tlsConnInfo) ConnectionID() string    { return i.addr }
