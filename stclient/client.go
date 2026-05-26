// Package stclient is a gomobile-compatible Syncthing BEP client.
// Build the Android AAR with: gomobile bind -target android -javapkg com.acidtv.unsyncthing -o stclient.aar .
package stclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
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

// Connect dials addr and establishes an authenticated BEP session.
// addr may be a bare host:port (TCP), or a URL with scheme tcp://, relay://, or quic://.
// Idempotent: any previous connection on this Client is closed first.
func (c *Client) Connect(addr, peerDeviceIDStr, folderIDs string) error {
	peerID, err := protocol.DeviceIDFromString(peerDeviceIDStr)
	if err != nil {
		return fmt.Errorf("invalid peer device ID: %w", err)
	}

	parsedURL, err := parseAddr(addr)
	if err != nil {
		return err
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

	ctx := context.Background()
	now := time.Now()
	hello := protocol.Hello{
		DeviceName:    clientName,
		ClientName:    clientName,
		ClientVersion: clientVersion,
		Timestamp:     now.UnixNano(),
	}

	var rwc io.ReadWriteCloser
	var connInfo protocol.ConnectionInfo

	switch parsedURL.Scheme {
	case "tcp":
		tc, netConn, err := dialTCP(parsedURL.Host, tlsConf)
		if err != nil {
			return err
		}
		if err := verifyPeerDeviceID(tc.ConnectionState().PeerCertificates, peerID); err != nil {
			tc.Close()
			return err
		}
		// BEP Hello exchange — protocol.NewConnection does NOT do this.
		if _, err := protocol.ExchangeHello(tc, hello); err != nil {
			tc.Close()
			return fmt.Errorf("BEP hello: %w", err)
		}
		netConn.SetDeadline(time.Time{})
		rwc = tc
		connInfo = &tlsConnInfo{conn: tc, addr: parsedURL.Host, establishedAt: now}

	case "relay":
		tc, err := dialRelay(ctx, parsedURL, peerID, c.cert, tlsConf)
		if err != nil {
			return err
		}
		if err := verifyPeerDeviceID(tc.ConnectionState().PeerCertificates, peerID); err != nil {
			tc.Close()
			return err
		}
		if _, err := protocol.ExchangeHello(tc, hello); err != nil {
			tc.Close()
			return fmt.Errorf("BEP hello: %w", err)
		}
		tc.SetDeadline(time.Time{})
		rwc = tc
		connInfo = &relayConnInfo{conn: tc, addr: parsedURL.String(), establishedAt: now}

	case "quic", "quic4", "quic6":
		qconn, certs, remoteAddr, err := dialQUIC(ctx, parsedURL.Host, tlsConf)
		if err != nil {
			return err
		}
		if err := verifyPeerDeviceID(certs, peerID); err != nil {
			qconn.Close()
			return err
		}
		// TLS is built into QUIC; set a deadline just for the BEP hello.
		qconn.SetDeadline(time.Now().Add(handshakeTimeout))
		if _, err := protocol.ExchangeHello(qconn, hello); err != nil {
			qconn.Close()
			return fmt.Errorf("BEP hello: %w", err)
		}
		qconn.SetDeadline(time.Time{})
		rwc = qconn
		connInfo = &quicConnInfo{remoteAddr: remoteAddr, addr: parsedURL.Host, establishedAt: now}

	default:
		return fmt.Errorf("unsupported scheme %q (use tcp://, relay://, or quic://)", parsedURL.Scheme)
	}

	folders := splitFolderIDs(folderIDs)
	model := newPeerModel()
	// When the BEP connection dies the protocol package calls Closed() on the model.
	// Clear our reference from a goroutine to avoid deadlock if Closed fires while
	// Close() is already holding c.mu.
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
		rwc, rwc, rwc,
		model,
		connInfo,
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

func verifyPeerDeviceID(certs []*x509.Certificate, expected protocol.DeviceID) error {
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
