# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project is

An Android app that lets you browse and open individual files from a Syncthing peer on demand — like Dropbox — without syncing whole folders. It is **not** a full Syncthing node. It connects as a read-only BEP client and fetches only the blocks it needs.

## Repository structure

```
stclient/   Go module — compiled to an Android AAR via gomobile
android/    Kotlin Android app — consumes the AAR
Makefile    Orchestrates both layers
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
make android           # build debug APK (requires AAR)
make install           # build + adb install to connected device
make tidy              # go mod tidy inside stclient/
make clean
```

**Go only (no NDK needed):**
```bash
cd stclient && go build ./...
cd stclient && go vet ./...
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

**BEP connection sequence:**
1. TLS dial → verify peer cert matches declared device ID
2. `protocol.NewConnection(peerID, r, w, closer, model, connInfo, CompressionMetadata, nil, nil)`
3. `conn.Start()` + `conn.ClusterConfig(...)` — advertise the desired folders so the peer sends its `Index`
4. `peerModel.Index()` fires → unblocks `WaitForIndex()`
5. `conn.Request(ctx, folder, name, blockNo, offset, size, hash, weakHash, false)` per block

### Android layer (`android/`)

Single-activity app. All Syncthing logic lives in `SyncthingViewModel`; the UI just observes `LiveData<UiState>`.

**gomobile binding conventions:** Go's `NewFoo(args) (*Foo, error)` becomes a Java constructor `new Foo(args) throws Exception`. Package-level functions land on `Stclient` (the capitalised package name class). Both `listFolder()` and `generateCert()` return `ByteArray` — always decode with `String(...)` before parsing as JSON.

**Cert lifecycle:** Generated once via `Stclient.generateCert()`, parsed with Gson using `@SerializedName` for Go's uppercase field names (`CertPEM`, `KeyPEM`, `DeviceID`), then stored in three separate SharedPrefs keys (`certPEM`, `keyPEM`, `deviceID`).

### Key dependency: `syncthing/lib/protocol`

The protocol package API is version-sensitive. As of `v1.27.4` (pinned in `go.mod`):
- `protocol.Model` methods take message structs (`*protocol.Index`, `*protocol.Request`, etc.), not flat arguments
- `protocol.Connection.Request` signature: `(ctx, folder, name, blockNo, offset, size, hash, weakHash, fromTemporary)`
- `protocol.NewConnection` takes separate `io.Reader`, `io.Writer`, `io.Closer` plus a `protocol.ConnectionInfo` interface and `*protocol.KeyGenerator`
- `FileInfo` validity is checked via methods: `IsDeleted()`, `IsInvalid()`, `IsDirectory()`

When upgrading syncthing, re-check all four of the above — they have changed between minor versions.
