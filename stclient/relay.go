package stclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"

	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/relay/client"
)

// dialRelay opens a relayed connection to peerID via the relay server at uri.
// It returns a plain net.Conn that the caller wraps in TLS — the relay only
// brokers raw bytes; the peer-to-peer BEP/TLS handshake happens inside the
// tunnel.
//
// The bool return is the TLS role the caller must adopt: true → tls.Server,
// false → tls.Client. The relay assigns sides at session-creation time; we
// just do what we're told.
//
// The ?id= query on the URL (if present) is the *relay server's* own device
// ID — the relay client lib verifies the relay's TLS cert against it. It is
// not the peer's ID; that comes in as peerID, sourced from the user's
// configured peer.
//
// See lib/connections/relay_dial.go in the syncthing source for the
// reference implementation.
func dialRelay(uri *url.URL, peerID protocol.DeviceID, cert tls.Certificate) (net.Conn, bool, error) {
	// Step 1: ask the relay broker for a session invitation. Internally this
	// dials the relay over TCP and does a bep-relay ALPN TLS handshake using
	// our own client cert, verifying the relay's cert against ?id= on the
	// URL. The conn JoinSession returns is plain TCP again.
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	inv, err := client.GetInvitationFromRelay(ctx, uri, peerID, []tls.Certificate{cert}, dialTimeout)
	if err != nil {
		return nil, false, fmt.Errorf("relay invite: %w", err)
	}

	// Step 2: connect to the relay's session port. JoinSession dials over
	// TCP and exchanges the session key from the invitation; on success we
	// get a net.Conn that carries traffic to the other peer.
	joinCtx, joinCancel := context.WithTimeout(context.Background(), dialTimeout)
	defer joinCancel()
	conn, err := client.JoinSession(joinCtx, inv)
	if err != nil {
		return nil, false, fmt.Errorf("relay join: %w", err)
	}
	return conn, inv.ServerSocket, nil
}
