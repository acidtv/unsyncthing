
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
 - [ ] Cancel download button
 - [ ] Be able to 'bookmark' multiple folders with a name
 - [ ] Only display our device id on the start page
 - [x] Show the folder name as a header text, not as part of the current path
 - [x] Move the refresh button on the file listing to the/a menu bar

## Claude generated

 - [ ] APK build not verified in remote container — outbound network to `dl.google.com` is blocked so `./gradlew assembleDebug` can't resolve the Android Gradle Plugin. Build & smoke-test the fragment split locally before shipping.
 - [ ] `refreshListing` failures still post `UiState.Error`, which after the screen split bounces the user from the file list back to the connect screen. Consider routing this through `_errorEvent` too so a transient refresh failure leaves the existing listing visible.
 - [ ] Bookmark rename — long-press only offers Delete. To rename, the user must delete and re-add. Consider an "Edit" entry in the long-press dialog.
 - [ ] Bookmark adapter doesn't highlight the bookmark that matches the currently-active connection, so after disconnecting it's not obvious which one you came from.
 - [x] Cancelling the connect dialog let the Go dial loop keep walking the peer's remaining addresses; each `OnDialing` callback re-posted `UiState.Connecting`, so the dialog popped back up "trying the next IP". Fixed: `Client.Connect` now runs under a cancellable context (`CancelConnect`, threaded through `dialAndHandshake`/`dialRelay`), `disconnect()` aborts the in-flight `connectingClient`, and `ConnectCancelGuard` drops late `OnDialing` posts.
 - [x] Cancel now also interrupts `waitForIndex` — `Connect` and `WaitForIndex` share one cancel slot (`beginCancellable`), `waitForIndex` takes a context and selects on `ctx.Done()`, so `CancelConnect` returns it within microseconds instead of waiting out the ≤30s timeout (`TestPeerModel_WaitForIndex_CancelReturnsPromptly`). The Android `connect()` also re-checks the guard before the index wait. `listFolder` is an in-memory read, so it doesn't block and needs no wiring.
 - [ ] The connect-cancel fix couldn't be exercised in the remote container — `make test-android` needs the Android Gradle Plugin (blocked host) and the new `cancelConnect()` binding needs a `make gomobile` AAR rebuild (no NDK here). Run `make gomobile && make test-android` locally before shipping; Go side is verified (`go build`/`vet`/`test` pass).
 - [ ] `make test-android` couldn't run in the remote container — the Android Gradle Plugin is fetched from `dl.google.com` which is blocked. The new `UpsertBookmarkTest` and the rest of the bookmark feature need a local `make test-android` pass before shipping.
 - [ ] Disconnect crash was caused by `c.conn.Close(nil)` — syncthing v1.27.4's `protocol.Connection.Close` calls `err.Error()` unconditionally and panics on nil. Fixed by passing a non-nil `errClientClose` sentinel and pinned by `TestProtocolCloseNilPanics`. If we ever bump syncthing past a version that nil-checks the error, drop the sentinel.
