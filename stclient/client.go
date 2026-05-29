// Package stclient is a gomobile-compatible Syncthing BEP client.
// Build the Android AAR with: gomobile bind -target android -javapkg com.acidtv.unsyncthing -o stclient.aar .
package stclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

const (
	clientName       = "unsyncthing"
	clientVersion    = "v0.1.0"
	// Per-candidate dial budget. Discovery may return multiple peer
	// addresses (LAN IP + Docker bridge IP + public IP + ...); we try them
	// in order, so cap each so an unreachable IP doesn't stall for 30s.
	dialTimeout      = 5 * time.Second
	handshakeTimeout = 30 * time.Second
)

// Reason we attach when closing the BEP connection ourselves. Must be non-nil:
// syncthing's protocol.Connection.Close calls err.Error() unconditionally to
// embed the reason in the outgoing BEP Close message, so passing nil panics
// with a nil-pointer dereference.
var errClientClose = errors.New("closed by client")

// Client manages a single BEP connection to a Syncthing peer.
// Safe for concurrent use after Connect.
type Client struct {
	mu          sync.Mutex
	myID        protocol.DeviceID
	cert        tls.Certificate
	conn        protocol.Connection
	model       *peerModel
	fetchCancel context.CancelFunc

	// connectCancel aborts the dial loop of an in-flight Connect. It has its
	// own mutex (not mu) because Connect holds mu for the whole dial loop, so
	// CancelConnect must reach the cancel func without contending for mu.
	// connectGen identifies which Connect owns the slot so a stale Connect's
	// deferred cleanup doesn't clear a newer one's cancel func (func values
	// aren't comparable, so we tag them with a generation counter instead).
	connectMu     sync.Mutex
	connectCancel context.CancelFunc
	connectGen    uint64
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

// ConnectStatus receives callbacks during the Connect dial loop so the UI
// can show "Connecting to <addr>…" — handy when the peer announces several
// addresses and we walk through them in sequence. Pass nil if you don't
// need it. gomobile generates a Java interface from this.
type ConnectStatus interface {
	OnDialing(addr string)
}

// Connect resolves peerDeviceIDStr via global + LAN discovery, dials the
// peer, and establishes an authenticated BEP session.
// Idempotent: any previous connection on this Client is closed first.
func (c *Client) Connect(peerDeviceIDStr, folderIDs string, status ConnectStatus) error {
	peerID, err := protocol.DeviceIDFromString(peerDeviceIDStr)
	if err != nil {
		return fmt.Errorf("invalid peer device ID: %w", err)
	}

	// Cancellation context for the whole dial sequence (discovery + the
	// per-candidate dial loop) so CancelConnect can abort it promptly instead
	// of letting it walk through every remaining address.
	ctx, cancel := context.WithCancel(context.Background())
	c.connectMu.Lock()
	if c.connectCancel != nil {
		c.connectCancel() // abort any prior in-flight Connect on this client
	}
	c.connectGen++
	gen := c.connectGen
	c.connectCancel = cancel
	c.connectMu.Unlock()
	defer func() {
		c.connectMu.Lock()
		if c.connectGen == gen {
			c.connectCancel = nil
		}
		c.connectMu.Unlock()
		cancel()
	}()

	addrs, err := Discover(c.myID.String(), peerDeviceIDStr, 8)
	if err != nil {
		return fmt.Errorf("discover peer: %w", err)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("connect cancelled")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Close any prior connection before opening a new one.
	if c.conn != nil {
		c.conn.Close(errClientClose)
		c.conn = nil
		c.model = nil
	}

	tlsConf := &tls.Config{
		Certificates:       []tls.Certificate{c.cert},
		InsecureSkipVerify: true, // device-ID verification replaces hostname verification
		NextProtos:         []string{"bep/1.0"},
		MinVersion:         tls.VersionTLS12,
	}

	// Walk the candidate addresses in order. Discovery hands them to us in
	// scheme priority (TCP before relay) and source ordering. Dial or
	// handshake failures move on to the next candidate; only a device-ID
	// mismatch is fatal — that means we reached *something* and it produced
	// the wrong identity, which retrying won't fix.
	var tlsConn *tls.Conn
	var transport string
	var dialErrs []string
	var addr string
	for _, candidate := range addrs {
		// Bail before announcing the next candidate so a cancel mid-loop stops
		// the "Connecting to <addr>…" updates rather than walking the rest.
		if ctx.Err() != nil {
			return fmt.Errorf("connect cancelled")
		}
		if status != nil {
			status.OnDialing(displayAddr(candidate))
		}
		tc, scheme, derr := dialAndHandshake(ctx, candidate, peerID, c.cert, tlsConf)
		if derr != nil {
			dialErrs = append(dialErrs, fmt.Sprintf("%s: %v", candidate, derr))
			continue
		}
		if verr := verifyPeerDeviceID(tc, peerID); verr != nil {
			tc.Close()
			return verr
		}
		tlsConn, transport, addr = tc, scheme, candidate
		break
	}
	if tlsConn == nil {
		return fmt.Errorf("dial: tried %d address(es): %s", len(addrs), strings.Join(dialErrs, "; "))
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

	// Past the handshake phase — clear the deadline. (*tls.Conn.SetDeadline
	// delegates to the underlying net.Conn.)
	tlsConn.SetDeadline(time.Time{})

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
		&tlsConnInfo{conn: tlsConn, addr: addr, transport: transport, establishedAt: time.Now()},
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
		c.conn.Close(errClientClose)
		c.conn = nil
		c.model = nil
	}
}

// CancelFetch aborts the in-progress FetchFile, if any. No-op when idle.
// The in-flight conn.Request returns promptly with a context-cancelled error;
// FetchFile then removes the partial file and returns without reporting an
// error, so the UI can treat a cancel as a clean stop.
func (c *Client) CancelFetch() {
	c.mu.Lock()
	cancel := c.fetchCancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// CancelConnect aborts an in-progress Connect, if any. No-op when idle.
// The dial loop returns promptly with a "connect cancelled" error and any
// in-flight dial/handshake is torn down via its context, so the caller stops
// walking the remaining candidate addresses instead of churning through them.
func (c *Client) CancelConnect() {
	c.connectMu.Lock()
	cancel := c.connectCancel
	c.connectMu.Unlock()
	if cancel != nil {
		cancel()
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
// transport reflects how we reached the peer: "tcp" or "relay".
type tlsConnInfo struct {
	conn          *tls.Conn
	addr          string
	transport     string
	establishedAt time.Time
}

func (i *tlsConnInfo) Type() string             { return i.transport }
func (i *tlsConnInfo) Transport() string        { return i.transport }
func (i *tlsConnInfo) IsLocal() bool            { return false }
func (i *tlsConnInfo) RemoteAddr() net.Addr     { return i.conn.RemoteAddr() }
func (i *tlsConnInfo) Priority() int            { return 0 }
func (i *tlsConnInfo) String() string           { return i.addr }
func (i *tlsConnInfo) Crypto() string           { return "tls" }
func (i *tlsConnInfo) EstablishedAt() time.Time { return i.establishedAt }
func (i *tlsConnInfo) ConnectionID() string     { return i.addr }

// dialAndHandshake dials the given candidate URL according to its scheme,
// then performs the BEP TLS handshake. Returns the post-handshake *tls.Conn
// and the transport name ("tcp" or "relay"). The relay path tunnels the
// peer-to-peer TLS through a broker; the TLS config (bep/1.0 ALPN, our cert,
// no hostname verification) is identical to the direct TCP path because the
// peer's TLS endpoint behaves the same on either side of the tunnel.
//
// peerID is the device we're trying to reach — the relay broker needs it to
// route the session. The TCP path doesn't use it (the address already
// identifies the endpoint).
func dialAndHandshake(ctx context.Context, candidate string, peerID protocol.DeviceID, cert tls.Certificate, tlsCfg *tls.Config) (*tls.Conn, string, error) {
	u, err := url.Parse(candidate)
	if err != nil {
		return nil, "", fmt.Errorf("parse address: %w", err)
	}
	var nc net.Conn
	// isServer flips us into tls.Server mode. The relay protocol randomises
	// which side of the tunnel performs the active/passive TLS role, so we
	// have to honour what the invitation tells us. Direct TCP is always
	// client-side.
	var isServer bool
	switch u.Scheme {
	case "tcp", "tcp4", "tcp6":
		nc, err = (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", u.Host)
	case "relay":
		nc, isServer, err = dialRelay(ctx, u, peerID, cert)
	default:
		return nil, "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if err != nil {
		return nil, "", err
	}
	nc.SetDeadline(time.Now().Add(handshakeTimeout))
	var tc *tls.Conn
	if isServer {
		tc = tls.Server(nc, tlsCfg)
	} else {
		tc = tls.Client(nc, tlsCfg)
	}
	// HandshakeContext aborts the handshake the moment the connect is
	// cancelled, rather than blocking until the handshake deadline.
	if herr := tc.HandshakeContext(ctx); herr != nil {
		nc.Close()
		return nil, "", fmt.Errorf("TLS handshake: %w", herr)
	}
	return tc, schemeTransport(u.Scheme), nil
}

func schemeTransport(scheme string) string {
	switch scheme {
	case "tcp", "tcp4", "tcp6":
		return "tcp"
	case "relay":
		return "relay"
	}
	return scheme
}

// displayAddr trims an address for human-readable status updates. Relay URLs
// carry the peer's device ID as ?id=…; the user already knows it (they
// typed it) and showing the full URL clutters the "Connecting to …" line.
func displayAddr(candidate string) string {
	u, err := url.Parse(candidate)
	if err != nil {
		return candidate
	}
	if u.Host == "" {
		return candidate
	}
	return u.Scheme + "://" + u.Host
}
