
# TODO

 - [x] Add tests for the Go code (if that makes sense), and make them runnable with `make test`
 - [ ] Add tests for the android code, and make them runnable with `make test`
 - [x] There's an error about 16kb alignment of some lib on startup
 - [x] Connect to a folder without specifying a hostname, how to do discovery?
   - [ ] Relay/QUIC support — today we only dial `tcp://`. Real Syncthing falls back to `relay://` (via `lib/relay/client`) and increasingly uses `quic://`. Surface a clear error for now; add real support later.
   - [ ] Happy-eyeballs across announced addresses — we now try each TCP candidate sequentially with a 5s per-address timeout, so a stale IP costs 5s instead of failing connect. Real happy-eyeballs (staggered parallel dial, first success wins) would make this snappier.
   - [ ] Discovery result caching — each `Connect` re-resolves from scratch (~200 ms WAN). Cache per device ID with a short TTL and invalidate on dial failure.
   - [ ] Verify `MulticastLock` scope is sufficient on OEM Wi-Fi power-save builds; extend hold / pre-warm the UDP socket if broadcast packets are being dropped before the listener sees them.
   - [ ] Discovery server fallback — no fallback to `discovery-v4.syncthing.net` / `discovery-v6.syncthing.net` if the primary is unreachable.
   - [ ] No IPv6-only smoke test in CI; verified manually only.
   - [ ] Snackbar truncates long error messages — the combined `local: …; global: …` discovery error gets cut off so you can't see both reasons. Move long errors into a dialog or an expandable view.
 - [x] Download progress bar in popup or bottom of screen, should already be visible even when we first need to make a new connection so the user has feedback that something is happening
 - [ ] Be able to 'bookmark' multiple folders with a name
 - [ ] Only display our device id on the start page
 - [x] Show the folder name as a header text, not as part of the current path
 - [x] Move the refresh button on the file listing to the/a menu bar

## Claude generated

 - [ ] APK build not verified in remote container — outbound network to `dl.google.com` is blocked so `./gradlew assembleDebug` can't resolve the Android Gradle Plugin. Build & smoke-test the fragment split locally before shipping.
 - [ ] `refreshListing` failures still post `UiState.Error`, which after the screen split bounces the user from the file list back to the connect screen. Consider routing this through `_errorEvent` too so a transient refresh failure leaves the existing listing visible.
