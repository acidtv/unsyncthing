package stclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	relayclient "github.com/syncthing/syncthing/lib/relay/client"
	"github.com/syncthing/syncthing/lib/protocol"
)

var quicCfg = &quic.Config{
	MaxIdleTimeout:  30 * time.Second,
	KeepAlivePeriod: 15 * time.Second,
}

// parseAddr normalises addr into a *url.URL. A bare host:port is treated as tcp.
func parseAddr(addr string) (*url.URL, error) {
	if !strings.Contains(addr, "://") {
		addr = "tcp://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	return u, nil
}

// dialTCP dials addr over TCP, performs the TLS handshake under a deadline, and
// returns the TLS conn and the underlying net.Conn (for deadline management).
func dialTCP(addr string, tlsConf *tls.Config) (*tls.Conn, net.Conn, error) {
	netConn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	netConn.SetDeadline(time.Now().Add(handshakeTimeout))
	tc := tls.Client(netConn, tlsConf)
	if err := tc.Handshake(); err != nil {
		netConn.Close()
		return nil, nil, fmt.Errorf("TLS handshake: %w", err)
	}
	return tc, netConn, nil
}

// dialRelay requests a session invitation from the relay server and joins it,
// then wraps the resulting channel in TLS. The role (client vs server) is
// determined by the invitation's ServerSocket flag.
func dialRelay(ctx context.Context, relayURL *url.URL, peerID protocol.DeviceID, cert tls.Certificate, tlsConf *tls.Config) (*tls.Conn, error) {
	inv, err := relayclient.GetInvitationFromRelay(ctx, relayURL, peerID, []tls.Certificate{cert}, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("relay invitation: %w", err)
	}
	conn, err := relayclient.JoinSession(ctx, inv)
	if err != nil {
		return nil, fmt.Errorf("relay join session: %w", err)
	}
	var tc *tls.Conn
	if inv.ServerSocket {
		tc = tls.Server(conn, tlsConf)
	} else {
		tc = tls.Client(conn, tlsConf)
	}
	tc.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := tc.Handshake(); err != nil {
		tc.Close()
		return nil, fmt.Errorf("relay TLS handshake: %w", err)
	}
	return tc, nil
}

// dialQUIC opens a QUIC connection to addr and returns a stream wrapper with
// the peer TLS certificates and remote address. TLS is built into QUIC so no
// separate handshake is needed.
func dialQUIC(ctx context.Context, addr string, tlsConf *tls.Config) (*quicRWC, []*x509.Certificate, net.Addr, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve %s: %w", addr, err)
	}
	packetConn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("UDP socket: %w", err)
	}
	transport := &quic.Transport{Conn: packetConn}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	session, err := transport.Dial(dialCtx, udpAddr, tlsConf, quicCfg)
	if err != nil {
		packetConn.Close()
		return nil, nil, nil, fmt.Errorf("QUIC dial: %w", err)
	}
	streamCtx, cancel2 := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel2()
	stream, err := session.OpenStreamSync(streamCtx)
	if err != nil {
		session.CloseWithError(1, err.Error())
		packetConn.Close()
		return nil, nil, nil, fmt.Errorf("QUIC stream: %w", err)
	}
	certs := session.ConnectionState().TLS.PeerCertificates
	rwc := &quicRWC{stream: stream, session: session, packetConn: packetConn}
	return rwc, certs, session.RemoteAddr(), nil
}

// quicRWC wraps a QUIC stream and its parent connection into an io.ReadWriteCloser
// so it can be passed to protocol.NewConnection.
type quicRWC struct {
	stream     quic.Stream
	session    quic.Connection
	packetConn net.PacketConn
}

func (q *quicRWC) Read(b []byte) (int, error)         { return q.stream.Read(b) }
func (q *quicRWC) Write(b []byte) (int, error)        { return q.stream.Write(b) }
func (q *quicRWC) SetDeadline(t time.Time) error      { return q.stream.SetDeadline(t) }

func (q *quicRWC) Close() error {
	q.stream.Close()
	q.session.CloseWithError(0, "closing")
	if q.packetConn != nil {
		q.packetConn.Close()
	}
	return nil
}

// relayConnInfo implements protocol.ConnectionInfo for a relay-tunnelled connection.
type relayConnInfo struct {
	conn          *tls.Conn
	addr          string
	establishedAt time.Time
}

func (i *relayConnInfo) Type() string             { return "relay" }
func (i *relayConnInfo) Transport() string        { return "relay" }
func (i *relayConnInfo) IsLocal() bool            { return false }
func (i *relayConnInfo) RemoteAddr() net.Addr     { return i.conn.RemoteAddr() }
func (i *relayConnInfo) Priority() int            { return 0 }
func (i *relayConnInfo) String() string           { return i.addr }
func (i *relayConnInfo) Crypto() string           { return "tls" }
func (i *relayConnInfo) EstablishedAt() time.Time { return i.establishedAt }
func (i *relayConnInfo) ConnectionID() string     { return i.addr }

// quicConnInfo implements protocol.ConnectionInfo for a QUIC connection.
type quicConnInfo struct {
	remoteAddr    net.Addr
	addr          string
	establishedAt time.Time
}

func (i *quicConnInfo) Type() string             { return "quic" }
func (i *quicConnInfo) Transport() string        { return "quic" }
func (i *quicConnInfo) IsLocal() bool            { return false }
func (i *quicConnInfo) RemoteAddr() net.Addr     { return i.remoteAddr }
func (i *quicConnInfo) Priority() int            { return 0 }
func (i *quicConnInfo) String() string           { return i.addr }
func (i *quicConnInfo) Crypto() string           { return "tls" }
func (i *quicConnInfo) EstablishedAt() time.Time { return i.establishedAt }
func (i *quicConnInfo) ConnectionID() string     { return i.addr }
