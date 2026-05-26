package stclient

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/syncthing/syncthing/lib/discover"
	"github.com/syncthing/syncthing/lib/protocol"
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
// timeoutSecs bounds the whole operation; pick something like 8 — long
// enough for an LAN broadcast cycle (peers announce every 30s but the cache
// usually has fresh entries) and a WAN HTTPS round-trip.
func Discover(peerDeviceIDStr string, timeoutSecs int) (string, error) {
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
		addrs, err := lookupLocal(ctx, peerID)
		results <- result{addrs: addrs, err: err, src: "local"}
	}()
	go func() {
		addrs, err := lookupGlobal(ctx, peerID)
		results <- result{addrs: addrs, err: err, src: "global"}
	}()

	var lastErr error
	var schemes []string
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			lastErr = fmt.Errorf("%s discovery: %w", r.src, r.err)
			continue
		}
		addr, schemesSeen, err := pickTCP(r.addrs)
		if err == nil {
			return addr, nil
		}
		schemes = append(schemes, schemesSeen...)
		lastErr = err
	}

	if len(schemes) > 0 {
		return "", fmt.Errorf("peer reachable only via %s, not supported", joinUnique(schemes))
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("peer not announced to discovery")
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
// 21027 and returns when an announcement for peerID arrives.
func lookupLocal(ctx context.Context, peerID protocol.DeviceID) ([]string, error) {
	return lookupLocalOn(ctx, peerID, localDiscoveryPort)
}

// lookupLocalOn is the testable variant of lookupLocal — accepts a port so
// tests can use ephemeral sockets.
func lookupLocalOn(ctx context.Context, peerID protocol.DeviceID, port int) ([]string, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if err != nil {
		return nil, fmt.Errorf("open UDP %d: %w", port, err)
	}
	defer conn.Close()

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
