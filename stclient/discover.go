package stclient

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/syncthing/syncthing/lib/discover"
	"github.com/syncthing/syncthing/lib/protocol"
	"golang.org/x/sys/unix"
)

const (
	globalDiscoveryURL = "https://discovery.syncthing.net/v2/"
	localDiscoveryPort = 21027

	globalDiscoveryTimeout = 15 * time.Second
)

// Discover resolves a Syncthing device ID into a dialable host:port, using
// both global discovery (HTTPS) and local LAN discovery (UDP broadcast)
// concurrently. Returns the first usable tcp:// address.
//
// myDeviceIDStr is our own device ID — used to seed an outgoing LAN-discovery
// announce so peers on the same Wi-Fi will respond with their own
// announcement immediately instead of waiting for their 30s broadcast cycle.
// (See syncthing/lib/discover/local.go:recvAnnouncements — peers force an
// immediate transmit when they see a previously-unknown device.)
//
// timeoutSecs bounds the whole operation; pick something like 8 — long
// enough for a WAN HTTPS round-trip and for a triggered LAN response.
func Discover(myDeviceIDStr, peerDeviceIDStr string, timeoutSecs int) (string, error) {
	myID, err := protocol.DeviceIDFromString(myDeviceIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid my device ID: %w", err)
	}
	peerID, err := protocol.DeviceIDFromString(peerDeviceIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid peer device ID: %w", err)
	}

	if timeoutSecs <= 0 {
		timeoutSecs = 8
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	type result struct {
		addrs []string
		err   error
		src   string
	}
	results := make(chan result, 2)

	go func() {
		addrs, err := lookupLocal(ctx, myID, peerID)
		results <- result{addrs: addrs, err: err, src: "local"}
	}()
	go func() {
		addrs, err := lookupGlobal(ctx, peerID)
		results <- result{addrs: addrs, err: err, src: "global"}
	}()

	var localErr, globalErr error
	var schemes []string
	for i := 0; i < 2; i++ {
		r := <-results
		assign := func(e error) {
			if r.src == "local" {
				localErr = e
			} else {
				globalErr = e
			}
		}
		if r.err != nil {
			assign(r.err)
			continue
		}
		addr, schemesSeen, err := pickTCP(r.addrs)
		if err == nil {
			return addr, nil
		}
		schemes = append(schemes, schemesSeen...)
		assign(err)
	}

	if len(schemes) > 0 {
		return "", fmt.Errorf("peer reachable only via %s, not supported", joinUnique(schemes))
	}
	// Global first: it's usually the actionable error. Local bind failures
	// (e.g. when the official Syncthing app already owns :21027) just mean
	// LAN discovery is unavailable on this device — not why the peer wasn't
	// found.
	return "", fmt.Errorf("global: %v; local: %v", globalErr, localErr)
}

// lookupGlobal queries the public Syncthing global discovery server.
func lookupGlobal(ctx context.Context, peerID protocol.DeviceID) ([]string, error) {
	u, err := url.Parse(globalDiscoveryURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("device", peerID.String())
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: globalDiscoveryTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("peer not announced to global discovery (is it online and discovery-enabled?)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var ann struct {
		Addresses []string `json:"addresses"`
	}
	if err := json.Unmarshal(body, &ann); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return ann.Addresses, nil
}

// lookupLocal listens for Syncthing LAN-discovery UDP broadcasts on port
// 21027 and returns when an announcement for peerID arrives. It also sends
// its own announce so peers respond immediately.
func lookupLocal(ctx context.Context, myID, peerID protocol.DeviceID) ([]string, error) {
	return lookupLocalOn(ctx, myID, peerID, localDiscoveryPort)
}

// lookupLocalOn is the testable variant of lookupLocal — accepts a port so
// tests can use ephemeral sockets.
func lookupLocalOn(ctx context.Context, myID, peerID protocol.DeviceID, port int) ([]string, error) {
	// SO_REUSEADDR + SO_REUSEPORT so we coexist with another process already
	// listening on :21027 (e.g. the official Syncthing app on the same phone).
	lc := net.ListenConfig{Control: setReusePort}
	pc, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("open UDP %d: %w", port, err)
	}
	conn := pc.(*net.UDPConn)
	defer conn.Close()

	enableBroadcast(conn)

	// Send our own announce so any peer on this LAN sees us as a new device
	// and immediately broadcasts back, instead of making us wait up to 30s
	// for their next scheduled announce.
	_ = sendAnnounce(conn, myID, port)

	go func() {
		<-ctx.Done()
		conn.SetReadDeadline(time.Now())
	}()

	buf := make([]byte, 65535)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if n < 4 {
			continue
		}
		if binary.BigEndian.Uint32(buf[:4]) != discover.Magic {
			continue
		}
		var ann discover.Announce
		if err := ann.Unmarshal(buf[4:n]); err != nil {
			continue
		}
		if ann.ID != peerID {
			continue
		}
		return rewriteUnspecified(ann.Addresses, src), nil
	}
}

// enableBroadcast sets SO_BROADCAST on the underlying socket so writes to
// 255.255.255.255 are permitted. Best-effort: failure is logged but not
// fatal, since subnet-broadcast targets often work without it.
func enableBroadcast(conn *net.UDPConn) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	})
}

// setReusePort is a ListenConfig.Control hook that enables SO_REUSEADDR and
// SO_REUSEPORT so multiple processes can bind to the same UDP port.
func setReusePort(_, _ string, c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
			sockErr = err
			return
		}
		sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	}); err != nil {
		return err
	}
	return sockErr
}

// sendAnnounce emits a single LAN-discovery announce packet for myID to the
// IPv4 broadcast address. Peers receiving it broadcast back immediately
// (see syncthing/lib/discover/local.go:recvAnnouncements force-tick path).
func sendAnnounce(conn *net.UDPConn, myID protocol.DeviceID, port int) error {
	ann := discover.Announce{
		ID:         myID,
		InstanceID: rand.Int63(),
	}
	body, err := ann.Marshal()
	if err != nil {
		return err
	}
	pkt := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(pkt[:4], discover.Magic)
	copy(pkt[4:], body)
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: port}
	_, err = conn.WriteToUDP(pkt, dst)
	return err
}

// rewriteUnspecified replaces 0.0.0.0 / :: hosts with the packet source IP,
// matching syncthing/lib/discover/local.go:registerDevice.
func rewriteUnspecified(addrs []string, src *net.UDPAddr) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		u, err := url.Parse(a)
		if err != nil {
			continue
		}
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			out = append(out, a)
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsUnspecified() {
			out = append(out, a)
			continue
		}
		u.Host = net.JoinHostPort(src.IP.String(), port)
		out = append(out, u.String())
	}
	return out
}

// pickTCP returns the first tcp:// address as host:port. If no tcp:// is
// present, returns the set of schemes seen so the caller can build an
// informative error.
func pickTCP(addresses []string) (string, []string, error) {
	var schemes []string
	for _, a := range addresses {
		u, err := url.Parse(a)
		if err != nil {
			continue
		}
		switch u.Scheme {
		case "tcp", "tcp4", "tcp6":
			if u.Host == "" {
				continue
			}
			return u.Host, nil, nil
		default:
			schemes = append(schemes, u.Scheme)
		}
	}
	if len(schemes) == 0 {
		return "", nil, errors.New("no addresses announced")
	}
	return "", schemes, fmt.Errorf("no tcp address (found: %s)", joinUnique(schemes))
}

func joinUnique(in []string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}
