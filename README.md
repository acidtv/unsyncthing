# unsyncthing

An Android client that browses and opens individual files from a Syncthing peer on demand — like Dropbox — without syncing entire folders.

The project has two layers:

- **`stclient/`** — a Go module wrapping `syncthing/lib/protocol` (the BEP wire protocol). Built into an Android AAR via [gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile).
- **`android/`** — a Kotlin app that imports the AAR and provides the UI.

## Setting up a development environment

You need:

| Tool | Version | Notes |
|---|---|---|
| Go | 1.25+ | required by `golang.org/x/mobile` |
| JDK | 17+ | needed by Android Gradle Plugin 8.3 |
| Android SDK | platform-34, build-tools 34, platform-tools | install via `sdkmanager` or Android Studio |
| Android NDK | 26.x (or later) | required by `gomobile bind` |
| gomobile + gobind | latest | Go-side build tools |

### Linux setup, step by step

```bash
# 1. Go — install from https://go.dev/dl/, then:
go version    # should print 1.25 or newer

# 2. JDK 17
sudo apt install openjdk-17-jdk    # or your distro's equivalent

# 3. Android SDK command-line tools
#    Download from https://developer.android.com/studio#command-line-tools-only
mkdir -p ~/android-sdk/cmdline-tools
unzip commandlinetools-linux-*.zip -d ~/android-sdk/cmdline-tools
mv ~/android-sdk/cmdline-tools/cmdline-tools ~/android-sdk/cmdline-tools/latest

# Add to ~/.bashrc (or ~/.zshrc):
export ANDROID_HOME=$HOME/android-sdk
export PATH=$PATH:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools

# 4. SDK components + NDK
sdkmanager --licenses                # accept all
sdkmanager "platform-tools" \
           "platforms;android-34" \
           "build-tools;34.0.0" \
           "ndk;26.1.10909125"

# Add to ~/.bashrc:
export ANDROID_NDK_HOME=$ANDROID_HOME/ndk/26.1.10909125

# 5. gomobile (one-time)
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init                        # needs ANDROID_NDK_HOME

# 6. Resolve Go module dependencies (one-time per checkout)
cd stclient && go mod tidy && cd ..
```

### macOS

Replace step 3 with `brew install --cask android-commandlinetools`, otherwise the same.

## Building

The `Makefile` orchestrates both layers.

```bash
make gomobile     # builds stclient.aar → android/app/libs/
make android      # builds a debug APK using the AAR
```

Iterating on Go code only — useful if the NDK isn't around:

```bash
cd stclient
go build ./...
go vet ./...
```

Iterating on Android only (once the AAR exists):

```bash
cd android
./gradlew assembleDebug
./gradlew lint
```

The debug APK lands at `android/app/build/outputs/apk/debug/app-debug.apk`.

## Installing on a phone

1. Enable **Developer options** on the phone (tap *Build number* in *About* seven times), then turn on **USB debugging**.
2. Plug the phone in and authorise the host when prompted.
3. From the project root:

   ```bash
   adb devices       # confirm your phone is listed
   make install      # builds + adb install
   ```

To deploy without rebuilding:

```bash
adb install -r android/app/build/outputs/apk/debug/app-debug.apk
```

## First-run setup: pairing with a Syncthing peer

The app connects directly to one of your existing Syncthing nodes (a NAS, home server, or desktop). It does not run a Syncthing daemon itself.

1. Launch unsyncthing. The first launch generates an ECDSA P-384 identity in the background; the screen briefly shows *"Generating identity…"* before displaying **your device ID**.
2. On the **remote peer's** Syncthing web UI:
   - Click **Add Remote Device**, paste the device ID from the phone, give it a name.
   - On the **Sharing** tab of any folder you want to access from the phone, check the new device.
   - Find the folder's **Folder ID** in the folder's *Edit* dialog (top of the General tab).
3. Back in unsyncthing, fill in:
   - **Peer address** — `host:port`, typically port `22000` (e.g. `192.168.1.10:22000`)
   - **Peer device ID** — found under *Actions → Show ID* on the peer
   - **Folder ID** — from step 2
4. Tap **Connect**. The file list appears once the index arrives (a few seconds for small folders). Tap any file to download it on demand to the app's cache.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `gomobile: command not found` | `$GOPATH/bin` not on PATH — add `export PATH=$PATH:$(go env GOPATH)/bin` |
| `gomobile: ANDROID_NDK_HOME not set` | export it (see step 4 above) |
| `Plugin com.android.application … not found` | `maven.google.com` unreachable — check your network/proxy |
| `adb: no permissions` on Linux | missing udev rules — run `sudo apt install android-sdk-platform-tools` (ships pre-made rules for common vendors), then unplug and replug |
| App says *"timeout waiting for index"* | folder ID typo, or the peer hasn't shared that folder with this device yet |
| *"device ID mismatch"* | the peer device ID was mistyped, or you're talking to the wrong host |

## See also

[CLAUDE.md](./CLAUDE.md) — architecture notes and the version-sensitive `syncthing/lib/protocol` API surface, for when you're touching the Go side.
