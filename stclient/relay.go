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

// dialRelay opens a relayed connection to the peer identified by the ?id=
// query on the relay URL. It returns a plain net.Conn that the caller wraps
// in TLS — the relay only brokers raw bytes; the peer-to-peer BEP/TLS
// handshake happens inside the tunnel.
//
// The bool return is the TLS role the caller must adopt: true → tls.Server,
// false → tls.Client. The relay assigns sides randomly across the two
// session participants; we just do what we're told.
//
// See lib/connections/relay_dial.go in the syncthing source for the
// reference implementation.
func dialRelay(uri *url.URL, cert tls.Certificate) (net.Conn, bool, error) {
	// Relay URLs look like relay://relay.example.com:22067/?id=PEER-DEVICE-ID.
	// The relay broker uses ?id= to route the session to the listening peer;
	// without it the broker has nothing to match.
	peerIDStr := uri.Query().Get("id")
	if peerIDStr == "" {
		return nil, false, fmt.Errorf("relay URL missing ?id= peer device ID")
	}
	peerID, err := protocol.DeviceIDFromString(peerIDStr)
	if err != nil {
		return nil, false, fmt.Errorf("parse relay peer ID: %w", err)
	}

	// Step 1: ask the relay broker for a session invitation. Internally this
	// dials the relay over TCP and does a bep-relay ALPN TLS handshake using
	// our own client cert. The relay client library handles all of that —
	// the conn it returns from JoinSession is plain TCP again.
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
