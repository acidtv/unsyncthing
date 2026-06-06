// Package stclient is a gomobile-compatible Syncthing BEP client.
// Build the Android AAR with: gomobile bind -target android -javapkg com.acidtv.unsyncthing -o stclient.aar .
package stclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
	clientName    = "unsyncthing"
	clientVersion = "v0.1.0"
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
	// connectedPeerID is the device we're actually connected to. With multiple
	// candidate peers it may differ from the one a bookmark was created with
	// (failover); the Android layer surfaces it so a downed primary is visible.
	connectedPeerID protocol.DeviceID
	// connectedAddr is the raw URL dialled on the last successful Connect
	// (e.g. "tcp://192.168.1.55:22000"). Returned by ConnectedAddr so the
	// Android layer can persist it as a fast-path hint for the next connect.
	connectedAddr string
	// lastCloseErr records why the BEP session most recently dropped (the reason
	// the protocol package passes to Closed). Surfaced by WaitForIndex so a peer
	// that hangs up right after connect — typically because it hasn't accepted
	// this device or shared the folder — produces an actionable error instead of
	// a bare "not connected". Reset at the start of each Connect.
	lastCloseErr error

	// connectCancel aborts whichever step of the connect sequence is in
	// flight — the Connect dial loop or the subsequent WaitForIndex — so a
	// single CancelConnect stops the whole attempt promptly. It has its own
	// mutex (not mu) because Connect holds mu for the whole dial loop, so
	// CancelConnect must reach the cancel func without contending for mu.
	// connectGen identifies which step owns the slot so a finished step's
	// deferred cleanup doesn't clear a later step's cancel func (func values
	// aren't comparable, so we tag them with a generation counter instead).
	connectMu     sync.Mutex
	connectCancel context.CancelFunc
	connectGen    uint64
}

// beginCancellable registers cancel as the current cancellable connect step
// and returns a context plus a deregister func to defer. CancelConnect cancels
// whatever is currently registered, so chaining Connect → WaitForIndex through
// this lets one cancel abort either step. Registering aborts any prior step
// still holding the slot.
func (c *Client) beginCancellable() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	c.connectMu.Lock()
	if c.connectCancel != nil {
		c.connectCancel()
	}
	c.connectGen++
	gen := c.connectGen
	c.connectCancel = cancel
	c.connectMu.Unlock()
	return ctx, func() {
		c.connectMu.Lock()
		if c.connectGen == gen {
			c.connectCancel = nil
		}
		c.connectMu.Unlock()
		cancel()
	}
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

// Connect resolves peerDeviceIDsStr via global + LAN discovery, dials the
// peer, and establishes an authenticated BEP session.
//
// peerDeviceIDsStr may be a single device ID or a comma-separated list of
// candidate device IDs that share the folder. Each is tried in turn (its own
// discovery + address walk); the first that yields a verified connection wins,
// so a bookmark survives any one host being offline. ConnectedPeerID reports
// which one answered. Idempotent: any previous connection is closed first.
func (c *Client) Connect(peerDeviceIDsStr, folderIDs, hintAddrs string, status ConnectStatus) error {
	peerIDStrs := splitFolderIDs(peerDeviceIDsStr)
	if len(peerIDStrs) == 0 {
		return fmt.Errorf("no peer device ID provided")
	}

	// Cancellation context for the whole dial sequence (discovery + the
	// per-candidate dial loop) so CancelConnect can abort it promptly instead
	// of letting it walk through every remaining candidate.
	ctx, deregister := c.beginCancellable()
	defer deregister()

	// Close any prior connection up front, in a short critical section, so we
	// never hold c.mu across the slow discovery/dial work below (that would
	// block IsConnected/snapshot/Close for the duration).
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close(errClientClose)
		c.conn = nil
		c.model = nil
		c.connectedPeerID = protocol.DeviceID{}
		c.connectedAddr = ""
	}
	// Clear any stale close reason from a previous attempt so WaitForIndex can't
	// report an old failure against this fresh connect.
	c.lastCloseErr = nil
	c.mu.Unlock()

	tlsConf := &tls.Config{
		Certificates:       []tls.Certificate{c.cert},
		InsecureSkipVerify: true, // device-ID verification replaces hostname verification
		NextProtos:         []string{"bep/1.0"},
		MinVersion:         tls.VersionTLS12,
	}

	// Try each candidate device in turn: discover its addresses, then walk
	// them. The first verified handshake wins. Per-peer failures (bad ID,
	// discovery miss, every address unreachable, identity mismatch) are
	// recorded and we move on — only success or cancellation ends the loop.
	var tlsConn *tls.Conn
	var transport, addr string
	var peerID protocol.DeviceID
	var peerErrs []string

	// Fast path: try cached hint addresses against the primary peer with a
	// short timeout before running full discovery. A stale or unreachable hint
	// simply falls through to the discovery loop below. Pass nil for status so
	// the UI holds "Looking up peer…" rather than flashing stale addresses.
	if hintAddrs != "" {
		if primaryID, perr := protocol.DeviceIDFromString(peerIDStrs[0]); perr == nil {
			hints := splitFolderIDs(hintAddrs)
			func() {
				hintCtx, hintCancel := context.WithTimeout(ctx, 2*time.Second)
				defer hintCancel()
				tc, sc, a, herr := dialPeer(hintCtx, hints, primaryID, c.cert, tlsConf, nil)
				if herr == nil {
					tlsConn, transport, addr, peerID = tc, sc, a, primaryID
				} else {
					peerErrs = append(peerErrs, fmt.Sprintf("hint: %v", herr))
				}
			}()
		}
	}

	if tlsConn == nil {
		for _, idStr := range peerIDStrs {
			if ctx.Err() != nil {
				return fmt.Errorf("connect cancelled")
			}
			candidatePeer, err := protocol.DeviceIDFromString(idStr)
			if err != nil {
				peerErrs = append(peerErrs, fmt.Sprintf("%s: invalid device ID: %v", idStr, err))
				continue
			}
			addrs, derr := Discover(c.myID.String(), candidatePeer.String(), 8)
			if derr != nil {
				peerErrs = append(peerErrs, fmt.Sprintf("%s: discover: %v", candidatePeer.Short(), derr))
				continue
			}
			tc, scheme, a, walkErr := dialPeer(ctx, addrs, candidatePeer, c.cert, tlsConf, status)
			if walkErr != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("connect cancelled")
				}
				peerErrs = append(peerErrs, fmt.Sprintf("%s: %v", candidatePeer.Short(), walkErr))
				continue
			}
			tlsConn, transport, addr, peerID = tc, scheme, a, candidatePeer
			break
		}
	}
	if tlsConn == nil {
		return fmt.Errorf("could not reach any peer: tried %d, %s", len(peerIDStrs), strings.Join(peerErrs, "; "))
	}
	// Guard against a CancelConnect that arrived while the last dial was
	// in flight: if we proceed to BEP Hello with a cancelled ctx the
	// handshake runs for up to handshakeTimeout (30 s) before giving up.
	if ctx.Err() != nil {
		tlsConn.Close()
		return fmt.Errorf("connect cancelled")
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
	model.setOnClosed(func(err error) {
		go func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.model == model {
				c.conn = nil
				c.model = nil
				c.connectedPeerID = protocol.DeviceID{}
				c.connectedAddr = ""
				// Remember why the peer hung up so WaitForIndex can explain it.
				c.lastCloseErr = err
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
	// Install the connection BEFORE Start(). The protocol package only calls
	// Closed() after Start, so by publishing c.conn/c.model first we guarantee
	// the setOnClosed callback observes the assignment: if the peer drops during
	// the handshake/ClusterConfig window it sees c.model == model and clears it,
	// rather than no-opping and leaving a dead connection marked live.
	c.mu.Lock()
	c.conn = conn
	c.model = model
	c.connectedPeerID = peerID
	c.connectedAddr = addr
	c.mu.Unlock()

	conn.Start()
	// Advertise our cluster config so the peer sends its Index.
	// Folder.Devices MUST include both our ID and the peer's ID,
	// otherwise the peer rejects with errMissingLocalInClusterConfig.
	conn.ClusterConfig(buildClusterConfig(c.myID, peerID, folders))
	return nil
}

// WaitForIndex blocks until the file index for folderID has settled (no more
// updates for a short quiet period) or until timeoutSecs seconds elapse.
// Returns successfully with whatever partial index has arrived if the timeout
// is reached but at least some data was received.
func (c *Client) WaitForIndex(folderID string, timeoutSecs int) error {
	c.mu.Lock()
	model := c.model
	closeErr := c.lastCloseErr
	c.mu.Unlock()
	if model == nil {
		// A nil model right after a successful Connect means the peer accepted
		// the TLS handshake but then dropped the BEP session. For this app that
		// almost always means the remote hasn't added/accepted this device or
		// hasn't shared the folder with it — surface the peer's close reason so
		// the user can act on it instead of seeing a bare "not connected".
		if closeErr != nil {
			return fmt.Errorf("peer closed the connection before sending folder %q (%v) — "+
				"check that (1) this device is added and accepted in the peer's Syncthing, "+
				"(2) the folder is shared with this device, and (3) the folder ID matches exactly",
				folderID, closeErr)
		}
		return fmt.Errorf("not connected")
	}
	// Register under the same cancel slot as Connect so CancelConnect aborts a
	// cancel that lands while we're still waiting for the peer's index instead
	// of leaving this blocking call running for the full timeout.
	ctx, deregister := c.beginCancellable()
	defer deregister()
	return model.waitForIndex(ctx, folderID, time.Duration(timeoutSecs)*time.Second)
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
		c.connectedPeerID = protocol.DeviceID{}
		c.connectedAddr = ""
	}
}

// ConnectedAddr returns the address URL dialled on the last successful
// Connect (e.g. "tcp://192.168.1.55:22000"). Returns "" when not connected.
func (c *Client) ConnectedAddr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectedAddr
}

// ConnectedPeerID returns the device ID we're currently connected to, or ""
// when not connected. With a multi-candidate Connect this is the host that
// actually answered, which may differ from a bookmark's primary peer.
func (c *Client) ConnectedPeerID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connectedPeerID == (protocol.DeviceID{}) {
		return ""
	}
	return c.connectedPeerID.String()
}

// FolderDevices returns, as a JSON array of device-ID strings, the other
// devices the peer advertised as sharing folderID (from its ClusterConfig),
// excluding our own device. The caller stores these on a bookmark so it can
// fail over to another host when the primary is down. Returns "[]" for a
// folder we have no cluster config for; errors only when not connected.
func (c *Client) FolderDevices(folderID string) ([]byte, error) {
	_, model := c.snapshot()
	if model == nil {
		return nil, fmt.Errorf("not connected")
	}
	devs := model.devicesForFolder(folderID)
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		if d == c.myID {
			continue // exclude ourselves; keep the connected peer and others
		}
		out = append(out, d.String())
	}
	return json.Marshal(out)
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

// dialPeer walks the discovered addresses for one peer in order and returns
// the first that completes the TLS handshake AND presents the expected device
// ID. Dial/handshake failures and identity mismatches are per-address failures:
// they're recorded and skipped, so an overlapping or stale address belonging to
// a different device doesn't abort failover to the next candidate. Only running
// out of addresses (returns an aggregated error) or ctx cancellation ends the
// walk. Returns the post-handshake conn, its transport, and the address used.
func dialPeer(ctx context.Context, addrs []string, peerID protocol.DeviceID, cert tls.Certificate, tlsCfg *tls.Config, status ConnectStatus) (*tls.Conn, string, string, error) {
	var dialErrs []string
	for _, candidate := range addrs {
		// Bail before announcing the next candidate so a cancel mid-loop stops
		// the "Connecting to <addr>…" updates rather than walking the rest.
		if ctx.Err() != nil {
			return nil, "", "", fmt.Errorf("connect cancelled")
		}
		if status != nil {
			status.OnDialing(displayAddr(candidate))
		}
		tc, scheme, derr := dialAndHandshake(ctx, candidate, peerID, cert, tlsCfg)
		if derr != nil {
			dialErrs = append(dialErrs, fmt.Sprintf("%s: %v", candidate, derr))
			continue
		}
		if verr := verifyPeerDeviceID(tc, peerID); verr != nil {
			tc.Close()
			dialErrs = append(dialErrs, fmt.Sprintf("%s: %v", candidate, verr))
			continue
		}
		return tc, scheme, candidate, nil
	}
	return nil, "", "", fmt.Errorf("tried %d address(es): %s", len(addrs), strings.Join(dialErrs, "; "))
}

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
