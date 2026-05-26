
# TODO

 - [x] Add tests for the Go code (if that makes sense), and make them runnable with `make test`
 - [ ] Add tests for the android code, and make them runnable with `make test`
 - [x] There's an error about 16kb alignment of some lib on startup
 - [x] Connect to a folder without specifying a hostname, how to do discovery?
   - [ ] Relay/QUIC support — today we only dial `tcp://`. Real Syncthing falls back to `relay://` (via `lib/relay/client`) and increasingly uses `quic://`. Surface a clear error for now; add real support later.
   - [ ] Happy-eyeballs across announced addresses — currently we take the first `tcp://` hit, so a stale IP first fails the whole connect. Try candidates in parallel/staggered.
   - [ ] Discovery result caching — each `Connect` re-resolves from scratch (~200 ms WAN). Cache per device ID with a short TTL and invalidate on dial failure.
   - [ ] Verify `MulticastLock` scope is sufficient on OEM Wi-Fi power-save builds; extend hold / pre-warm the UDP socket if broadcast packets are being dropped before the listener sees them.
   - [ ] Discovery server fallback — no fallback to `discovery-v4.syncthing.net` / `discovery-v6.syncthing.net` if the primary is unreachable.
   - [ ] No IPv6-only smoke test in CI; verified manually only.
 - [x] Download progress bar in popup or bottom of screen, should already be visible even when we first need to make a new connection so the user has feedback that something is happening
 - [ ] Be able to 'bookmark' multiple folders with a name
 - [ ] Only display our device id on the start page
 - [x] Show the folder name as a header text, not as part of the current path
 - [x] Move the refresh button on the file listing to the/a menu bar

