# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project is

An Android app that lets you browse and open individual files from a Syncthing peer on demand — like Dropbox — without syncing whole folders. It is **not** a full Syncthing node. It connects as a read-only BEP client and fetches only the blocks it needs.

## Repository structure

```
stclient/   Go module — compiled to an Android AAR via gomobile
android/    Kotlin Android app — consumes the AAR
Makefile    Orchestrates both layers
README.md   User-facing setup and pairing guide
TODO.md     Known issues and feature backlog
```

## Build commands

**Prerequisites (one-time setup):**
```bash
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init                  # requires ANDROID_NDK_HOME to be set
cd stclient && go mod tidy     # populates go.sum
```

**Day-to-day:**
```bash
make gomobile          # build stclient.aar → android/app/libs/
make test              # run Go unit tests
make android           # build debug APK (requires AAR)
make install           # build + adb install to connected device
make tidy              # go mod tidy inside stclient/
make clean
```

**Go only (no NDK needed):**
```bash
cd stclient && go build ./...
cd stclient && go vet ./...
cd stclient && go test ./...
# Check gomobile binding compatibility without building the AAR:
gobind -lang java -javapkg com.acidtv.unsyncthing .
```

**Android only:**
```bash
cd android && ./gradlew assembleDebug
cd android && ./gradlew installDebug
cd android && ./gradlew lint
```

## Architecture

### Data flow

```
Android UI  →  SyncthingViewModel  →  stclient AAR (JNI)  →  Go Client
                                                                   ↓
                                                     TLS + BEP over TCP
                                                                   ↓
                                                         Syncthing peer
```

### Go layer (`stclient/`)

The entire Go module is designed to be gomobile-compatible — no maps, channels, or complex types cross the JNI boundary. All structured data is passed as `[]byte` JSON.

| File | Responsibility |
|---|---|
| `cert.go` | Generate/load ECDSA self-signed cert; `GenerateCert()` returns JSON `{CertPEM, KeyPEM, DeviceID}` |
| `client.go` | `Client` type: TLS dial, device-ID verification, `protocol.NewConnection`, ClusterConfig handshake |
| `model.go` | `peerModel` implements `protocol.Model`; stores received `Index`/`IndexUpdate` messages; `Request()` returns `ErrNoSuchFile` (we don't serve files) |
| `index.go` | `ListFolder()` — walks `peerModel.folders`, skips deleted/invalid, returns JSON `[]FileEntry` |
| `fetch.go` | `FetchFile()` — iterates `FileInfo.Blocks`, calls `conn.Request()` per block with progress callbacks |
| `tools.go` | Blank-import `golang.org/x/mobile/bind` so gomobile includes the binding machinery |

**BEP connection sequence:**
1. TLS dial → verify peer cert matches declared device ID
2. `protocol.ExchangeHello(tlsConn, ...)` — BEP hello must be done manually before `NewConnection`
3. `protocol.NewConnection(peerID, r, w, closer, model, connInfo, CompressionMetadata, nil, nil)`
4. `conn.Start()` + `conn.ClusterConfig(...)` — advertise the desired folders so the peer sends its `Index`
5. `peerModel.Index()` fires → unblocks `WaitForIndex()`
6. `conn.Request(ctx, folder, name, blockNo, offset, size, hash, weakHash, false)` per block

**`waitForIndex` quiet-period logic:** The model polls every 100 ms looking for a 3-second quiet gap since the last `Index`/`IndexUpdate`. This handles Syncthing's pattern of sending one `Index` followed by many `IndexUpdate` batches. If the timeout fires but at least one update arrived, it returns nil (partial is better than error).

**ClusterConfig requirement:** Both our device ID and the peer's device ID must appear in `Folder.Devices`; omitting either causes the peer to reject with `errMissingLocalInClusterConfig`.

**Connection lifecycle:** When the BEP connection drops (peer idle timeout, NAT churn) the protocol package calls `peerModel.Closed()`. A goroutine clears `c.conn` and `c.model` so subsequent `IsConnected()` checks return false and the Android layer can reconnect.

### Android layer (`android/`)

Single-activity app. All Syncthing logic lives in `SyncthingViewModel`; the UI just observes `LiveData`.

**Source files:**

| File | Responsibility |
|---|---|
| `SyncthingViewModel.kt` | BEP client lifecycle, connection state, download management, cert storage |
| `MainActivity.kt` | UI binding, state observation, RecyclerView, menu, back-navigation |
| `FileListAdapter.kt` | `ListAdapter<FileEntry>` with DiffUtil; renders icon, name, size |

**UI state machine (`UiState` sealed class):**
- `Idle` — connection form visible, no client
- `Connecting` — form visible, connect button disabled, status "Connecting…"
- `FileList(folderID, allEntries, currentDir)` — form hidden, folder header visible, RecyclerView populated
- `Error(message)` — form visible, Toast shown

`FileList.entries` is a computed property: it filters `allEntries` to the current directory level, synthesises implicit directory entries for files whose parent dirs weren't in the index, and sorts directories first then alphabetically.

**Download flow:**
1. Tap file → `vm.fetchFile(folderID, filePath)`
2. ViewModel checks `downloadJob?.isActive` to prevent concurrent downloads
3. File written to `cacheDir` using a sanitised basename
4. Path-traversal defence: `dest.canonicalPath` must start with `cacheDir.canonicalPath + separator`
5. If `!client.isConnected`, transparently reconnects using `savedConnection()` prefs
6. `FetchProgress` callbacks post to `_download` LiveData (progress bar in footer)
7. On `onDone`, `copyToDownloads()` writes to MediaStore with `IS_PENDING=1`, then clears it
8. Snackbar shows "Saved to Downloads/…" with an "Open" action

**Download LiveData is separate from `state`** so the file list is not replaced mid-download — the user can keep scrolling.

**gomobile binding conventions:** Go's `NewFoo(args) (*Foo, error)` becomes a Java constructor `new Foo(args) throws Exception`. Package-level functions land on `Stclient` (the capitalised package name class). `listFolder()` and `generateCert()` return `ByteArray` — always decode with `String(...)` before parsing as JSON.

**Cert lifecycle:** Generated once via `Stclient.generateCert()`, parsed with Gson using `@SerializedName` for Go's uppercase field names (`CertPEM`, `KeyPEM`, `DeviceID`), then stored in three separate SharedPrefs keys (`certPEM`, `keyPEM`, `deviceID`). The ViewModel caches the `CertData` in memory to avoid re-reading prefs on every operation.

**SharedPrefs keys:**

| Key | Value |
|---|---|
| `certPEM` | PEM-encoded TLS cert |
| `keyPEM` | PEM-encoded private key |
| `deviceID` | Human-readable device ID (e.g. `XXXXXXX-…`) |
| `lastAddr` | Most recent peer address (`host:port`) |
| `lastPeerID` | Most recent peer device ID |
| `lastFolder` | Most recent folder ID |

### Key dependency: `syncthing/lib/protocol`

The protocol package API is version-sensitive. As of `v1.27.4` (pinned in `go.mod`):
- `protocol.Model` methods take message structs (`*protocol.Index`, `*protocol.Request`, etc.), not flat arguments
- `protocol.Connection.Request` signature: `(ctx, folder, name, blockNo, offset, size, hash, weakHash, fromTemporary)`
- `protocol.NewConnection` takes separate `io.Reader`, `io.Writer`, `io.Closer` plus a `protocol.ConnectionInfo` interface and `*protocol.KeyGenerator`
- `FileInfo` validity is checked via methods: `IsDeleted()`, `IsInvalid()`, `IsDirectory()`, `IsSymlink()`
- `protocol.ExchangeHello` must be called before `NewConnection`

When upgrading syncthing, re-check all five points above — they have changed between minor versions.

### Makefile details

The gomobile target compiles with `-androidapi 21` (the AAR minimum) and `-ldflags="-extldflags=-Wl,-z,max-page-size=16384"` (required for 16 kB page-size devices running Android 15+). The Android app's `minSdk` is 29 in `build.gradle.kts`.

## Issue tracking

Whenever you encounter known issues, bugs, limitations, or remaining TODOs during a task, append them to `TODO.md` under a `## Claude generated` header. Create the file if it doesn't exist. Do not remove or overwrite items already listed there.
