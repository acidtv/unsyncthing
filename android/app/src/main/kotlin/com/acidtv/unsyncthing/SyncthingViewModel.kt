package com.acidtv.unsyncthing

import android.app.Application
import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.net.wifi.WifiManager
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.LiveData
import androidx.lifecycle.MutableLiveData
import androidx.lifecycle.viewModelScope
import com.acidtv.unsyncthing.stclient.Client
import com.acidtv.unsyncthing.stclient.ConnectStatus
import com.acidtv.unsyncthing.stclient.FetchProgress
import com.acidtv.unsyncthing.stclient.Stclient
import com.google.gson.Gson
import com.google.gson.annotations.SerializedName
import com.google.gson.reflect.TypeToken
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import java.io.File

// Mirrors the Go FileEntry struct. encoding/json emits the Go field names
// verbatim (PascalCase), so @SerializedName is required — Gson is
// case-sensitive by default.
data class FileEntry(
    @SerializedName("Name")     val name: String,
    @SerializedName("Path")     val path: String,
    @SerializedName("Size")     val size: Long,
    @SerializedName("Modified") val modified: Long,
    @SerializedName("IsDir")    val isDir: Boolean,
)

sealed class UiState {
    object Idle : UiState()
    data class Connecting(val status: String) : UiState()
    data class FileList(
        val folderID: String,
        val allEntries: List<FileEntry>,
        val currentDir: String = "",
        val bookmarkName: String? = null,
        // The device we actually connected to. May differ from the bookmark's
        // primary peer when it was offline and we failed over to another host.
        val connectedPeerID: String? = null,
    ) : UiState() {
        val entries: List<FileEntry> get() {
            val prefix = if (currentDir.isEmpty()) "" else "$currentDir/"
            // Use a map keyed by the immediate child name so each name appears once.
            val seen = mutableMapOf<String, FileEntry>()
            for (entry in allEntries) {
                val relative = if (prefix.isEmpty()) entry.path
                               else if (entry.path.startsWith(prefix)) entry.path.removePrefix(prefix)
                               else continue
                val slash = relative.indexOf('/')
                if (slash == -1) {
                    // Direct child at this level — use its real entry.
                    seen.getOrPut(relative) { entry }
                } else {
                    // File lives deeper: ensure the immediate subdirectory is visible.
                    // Syncthing doesn't always send explicit directory FileInfo entries,
                    // so synthesize one from the path if we haven't seen a real one yet.
                    val dirName = relative.substring(0, slash)
                    val dirPath = "$prefix$dirName"
                    seen.getOrPut(dirName) {
                        allEntries.find { it.path == dirPath && it.isDir }
                            ?: FileEntry(dirName, dirPath, 0L, 0L, true)
                    }
                }
            }
            return seen.values.sortedWith(compareByDescending<FileEntry> { it.isDir }.thenBy { it.name })
        }
    }
    data class Error(val message: String) : UiState()
}

data class DownloadProgress(val path: String, val downloaded: Long, val total: Long)

data class DownloadCompleted(val displayName: String, val uri: Uri, val mimeType: String)

// One-shot signal that a previewed file finished fetching and is ready to open.
// Preview progress and errors flow through the shared `_download`/`_errorEvent`
// channels so the existing bottom footer (and its cancel button) drive previews
// exactly like a regular download.
data class PreviewReady(
    val name: String,
    val type: PreviewType,
    val file: File,
)

// CachedAddr holds a diallable address URL and the time it last connected
// successfully. Used to skip discovery on reconnect.
data class CachedAddr(
    val addr: String,
    val lastSeen: Long = 0L,
)

// knownPeers are additional device IDs discovered (from the peer's ClusterConfig)
// as also sharing folderID. The primary peerID is the identity key and is tried
// first; knownPeers are fallback hosts so the bookmark survives one host being
// offline. Discovery only happens when introducer is true — i.e. the user has
// chosen to trust this peer to tell the app about the folder's other devices.
// Old stored bookmarks lack these fields — Gson leaves them null/default, so
// loadBookmarks() normalises them.
data class Bookmark(
    val name: String,
    val peerID: String,
    val folderID: String,
    val knownPeers: List<String> = emptyList(),
    val introducer: Boolean = false,
    val cachedAddrs: List<CachedAddr> = emptyList(),
)

private data class CertData(
    @SerializedName("CertPEM")  val certPEM: String,
    @SerializedName("KeyPEM")   val keyPEM: String,
    @SerializedName("DeviceID") val deviceID: String,
)

class SyncthingViewModel(app: Application) : AndroidViewModel(app) {

    private val prefs = app.getSharedPreferences("unsyncthing", Context.MODE_PRIVATE)
    private val gson = Gson()

    // Temporary, TTL-limited cache for previewed files (app cache dir).
    private val previewCache = PreviewCache(File(app.cacheDir, "preview"))

    // All client/cert mutation goes through `lock`.
    private val lock = Any()
    private var client: Client? = null
    // The client whose Connect dial loop is currently running, or null when no
    // connect is in flight. Held separately from `client` (which only points at
    // a fully-established connection) so a cancel can abort the in-progress
    // dial loop — the blocking native connect() ignores coroutine cancellation.
    private var connectingClient: Client? = null
    private var cert: CertData? = null
    private var downloadJob: Job? = null
    private var connectJob: Job? = null

    // Drops late OnDialing callbacks (a dial already dispatched from the Go
    // thread when Cancel landed) so they don't re-show the connecting dialog
    // after a cancel hid it. Re-armed at the start of each connect.
    private val connectGuard = ConnectCancelGuard()

    // Drops late OnProgress callbacks (the block already in flight when
    // CancelFetch landed) so they don't re-show the footer after a cancel hid
    // it. Re-armed at the start of each fetchFile.
    private val cancelGuard = DownloadCancelGuard()

    private val _state = MutableLiveData<UiState>(UiState.Idle)
    val state: LiveData<UiState> = _state

    // Active download progress, or null when idle. Kept separate from `state`
    // so the FileList isn't replaced during a download (which would prevent
    // the user from opening another file).
    private val _download = MutableLiveData<DownloadProgress?>(null)
    val download: LiveData<DownloadProgress?> = _download

    // Single-shot completion event. The Activity calls acknowledgeCompletion()
    // after consuming it so a config change doesn't re-fire the Snackbar.
    private val _completed = MutableLiveData<DownloadCompleted?>(null)
    val completed: LiveData<DownloadCompleted?> = _completed

    // Single-shot per-action error (e.g. a failed download). Distinct from
    // UiState.Error, which is reserved for screen-level failures that should
    // bounce the user back to the connect screen.
    private val _errorEvent = MutableLiveData<String?>(null)
    val errorEvent: LiveData<String?> = _errorEvent

    // Single-shot "download cancelled" confirmation. Set true when the user
    // cancels; the Activity calls acknowledgeCancelled() after showing the
    // Snackbar so a config change doesn't re-fire it.
    private val _cancelledEvent = MutableLiveData<Boolean?>(null)
    val cancelledEvent: LiveData<Boolean?> = _cancelledEvent

    // Null while the cert is being generated on first launch.
    private val _deviceID = MutableLiveData<String?>(null)
    val deviceID: LiveData<String?> = _deviceID

    private val _bookmarks = MutableLiveData<List<Bookmark>>(loadBookmarks())
    val bookmarks: LiveData<List<Bookmark>> = _bookmarks

    // Single-shot: emitted when a preview fetch completes so the UI can open the
    // preview screen. The Activity/fragment acknowledges it after navigating.
    private val _previewReady = MutableLiveData<PreviewReady?>(null)
    val previewReady: LiveData<PreviewReady?> = _previewReady

    init {
        // Generate the cert off the main thread so the very first launch
        // doesn't ANR on ECDSA P-384 keygen.
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val c = ensureCert()
                _deviceID.postValue(c.deviceID)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.postValue(UiState.Error("Failed to initialise: ${e.message}"))
            }
        }
    }

    // Returns (candidatePeerIDs, folder) for a transparent reconnect. The first
    // element is the comma-separated candidate list (primary + known fallbacks),
    // which Client.connect splits and tries in turn — so reconnects fail over to
    // another host just like a fresh connect. Falls back to the legacy single
    // peerID key for connections saved before multi-host support.
    fun savedConnection(): Pair<String, String>? {
        val peers = prefs.getString("lastPeerIDs", null)
            ?: prefs.getString("lastPeerID", null)
            ?: return null
        val folder = prefs.getString("lastFolder", null) ?: return null
        return Pair(peers, folder)
    }

    fun saveBookmark(name: String, peerID: String, folderID: String, introducer: Boolean = false) {
        val current = _bookmarks.value ?: emptyList()
        // Preserve any already-discovered fallback hosts across a name edit
        // (peerID + folderID are the identity, so an edit keeping them is the
        // same bookmark). Drop them when introducer is turned off so a disabled
        // bookmark doesn't keep dialling hosts the user no longer trusts.
        val existing = current.firstOrNull { it.peerID == peerID && it.folderID == folderID }
        val keptPeers = if (introducer) existing?.knownPeers ?: emptyList() else emptyList()
        val keptCached = existing?.cachedAddrs ?: emptyList()
        val updated = upsertBookmark(current, Bookmark(name, peerID, folderID, keptPeers, introducer, keptCached))
        writeBookmarks(updated)
    }

    fun deleteBookmark(peerID: String, folderID: String) {
        val updated = removeBookmark(_bookmarks.value ?: emptyList(), peerID, folderID)
        writeBookmarks(updated)
    }

    private fun writeBookmarks(list: List<Bookmark>) {
        prefs.edit().putString("bookmarks", gson.toJson(list)).apply()
        _bookmarks.value = list
    }

    // Merge the device IDs discovered from the peer into the matching bookmark's
    // known-peers list. Called from the connect coroutine (IO thread) but hops to
    // the main thread so the read-modify-write of _bookmarks shares the same
    // thread as saveBookmark/deleteBookmark — otherwise a concurrent edit could be
    // lost and LiveData.value would be read off-thread. No-op when no bookmark
    // matches (an unsaved manual connection) or nothing changed.
    private fun updateKnownPeers(primaryPeerID: String, folderID: String, discovered: List<String>) {
        viewModelScope.launch(Dispatchers.Main) {
            val current = _bookmarks.value ?: emptyList()
            val existing = current.firstOrNull { it.peerID == primaryPeerID && it.folderID == folderID }
                ?: return@launch
            val merged = mergeKnownPeers(discovered, primaryPeerID)
            if (merged == existing.knownPeers) return@launch
            writeBookmarks(upsertBookmark(current, existing.copy(knownPeers = merged)))
        }
    }

    private fun updateCachedAddr(peerDeviceID: String, folderID: String, addr: String) {
        // Relay addresses require a sequential invite+join handshake that
        // typically exceeds the 2 s hint budget — caching them degrades
        // reconnect speed rather than improving it.
        if (!addr.startsWith("tcp://") && !addr.startsWith("tcp4://") && !addr.startsWith("tcp6://")) return
        viewModelScope.launch(Dispatchers.Main) {
            val current = _bookmarks.value ?: emptyList()
            val existing = current.firstOrNull { it.peerID == peerDeviceID && it.folderID == folderID }
                ?: return@launch
            val now = System.currentTimeMillis()
            val merged = (listOf(CachedAddr(addr, now)) + existing.cachedAddrs)
                .distinctBy { it.addr }
                .take(5)
            writeBookmarks(upsertBookmark(current, existing.copy(cachedAddrs = merged)))
        }
    }

    // Returns the cached hint addresses for the bookmark matching (primaryPeerID,
    // folderID) as a comma-separated string, most-recently-successful first.
    private fun savedHints(primaryPeerID: String, folderID: String): String =
        (_bookmarks.value ?: emptyList())
            .firstOrNull { it.peerID == primaryPeerID && it.folderID == folderID }
            ?.cachedAddrs
            ?.sortedByDescending { it.lastSeen }
            ?.joinToString(",") { it.addr }
            ?: ""

    private fun loadBookmarks(): List<Bookmark> {
        val raw = prefs.getString("bookmarks", null)
        if (raw != null) {
            val type = object : TypeToken<List<Bookmark>>() {}.type
            val parsed: List<Bookmark> = gson.fromJson(raw, type) ?: emptyList()
            // Gson bypasses Kotlin constructor defaults, so bookmarks saved
            // before knownPeers/cachedAddrs existed deserialise with null lists. Normalise.
            return parsed.map {
                it.copy(
                    knownPeers = it.knownPeers ?: emptyList(),
                    cachedAddrs = it.cachedAddrs ?: emptyList(),
                )
            }
        }
        // Migration: seed from the legacy single-connection prefs so users
        // upgrading don't lose their saved peer/folder.
        val peerID = prefs.getString("lastPeerID", null)
        val folderID = prefs.getString("lastFolder", null)
        if (peerID != null && folderID != null) {
            val seeded = listOf(Bookmark(folderID, peerID, folderID))
            prefs.edit().putString("bookmarks", gson.toJson(seeded)).apply()
            return seeded
        }
        return emptyList()
    }

    // introducer controls the multi-host behaviour: when true, fallbackPeers are
    // also tried and the peer's shared-device list is captured to refresh them.
    // When false the bookmark stays pinned to its single primary peer.
    fun connect(
        peerDeviceID: String,
        folderID: String,
        fallbackPeers: List<String> = emptyList(),
        introducer: Boolean = false,
        cachedAddrs: List<CachedAddr> = emptyList(),
    ) {
        // Try the primary first, then (only as an introducer) any known fallback
        // hosts. Client.connect splits this CSV and walks the candidates until
        // one answers.
        val candidates = (listOf(peerDeviceID) + if (introducer) fallbackPeers else emptyList()).distinct()
        val candidatesCsv = candidates.joinToString(",")
        prefs.edit()
            .putString("lastPeerID", peerDeviceID)
            .putString("lastPeerIDs", candidatesCsv)
            .putString("lastFolder", folderID)
            .apply()
        // Drop any unconsumed one-shot events from a previous session so the
        // new file list doesn't pop a Snackbar referencing the old download.
        _completed.value = null
        _errorEvent.value = null
        _cancelledEvent.value = null
        connectGuard.arm()
        _state.value = UiState.Connecting("Looking up peer…")
        connectJob = viewModelScope.launch(Dispatchers.IO) {
            var newClient: Client? = null
            try {
                val c = ensureCert()
                newClient = Client(c.certPEM, c.keyPEM)
                // Publish the in-flight client so cancelConnect() can abort its
                // dial loop; the blocking native connect() can't be stopped by
                // cancelling this coroutine.
                synchronized(lock) { connectingClient = newClient }
                val status = object : ConnectStatus {
                    override fun onDialing(addr: String) {
                        if (!connectGuard.allowed()) return
                        _state.postValue(UiState.Connecting("Connecting to $addr…"))
                    }
                }
                // Hold a MulticastLock while we wait for UDP broadcasts —
                // some Wi-Fi power-save implementations drop them otherwise.
                val hints = cachedAddrs
                    .sortedByDescending { it.lastSeen }
                    .joinToString(",") { it.addr }
                withMulticastLock {
                    newClient.connect(candidatesCsv, folderID, hints, status)
                }
                // Bail before the (separately cancellable) index wait if Cancel
                // already landed, closing the gap between connect() returning and
                // waitForIndex registering its own cancel slot.
                if (!connectGuard.allowed()) {
                    newClient.close()
                    return@launch
                }
                newClient.waitForIndex(folderID, 30)
                val connectedPeer = newClient.connectedPeerID()
                val connectedAddr = newClient.connectedAddr()
                // Only cache when we connected to the primary peer. A fallback
                // peer's address would fail device-ID verification in the hint
                // path (which verifies against the primary) and waste the 2 s
                // budget on every subsequent reconnect.
                if (connectedAddr.isNotEmpty() && (connectedPeer.isEmpty() || connectedPeer == peerDeviceID)) {
                    updateCachedAddr(peerDeviceID, folderID, connectedAddr)
                }

                val json = String(newClient.listFolder(folderID))
                val type = object : TypeToken<List<FileEntry>>() {}.type
                val entries: List<FileEntry> = gson.fromJson(json, type) ?: emptyList()

                // If the user hit Cancel mid-flight, disconnect() reset state to
                // Idle and aborted the dial; drop the freshly built client on the
                // floor rather than surfacing it as a FileList or Error.
                if (!connectGuard.allowed() || _state.value !is UiState.Connecting) {
                    newClient.close()
                    return@launch
                }

                synchronized(lock) {
                    client?.close()
                    client = newClient
                    connectingClient = null
                }

                // As an introducer, the peer's ClusterConfig (guaranteed to have
                // arrived before the index) names the other devices sharing this
                // folder. Remember them on the matching bookmark so future
                // connects can fail over. Skipped when introducer is off.
                if (introducer) {
                    val discovered = runCatching {
                        val devJson = String(newClient.folderDevices(folderID))
                        gson.fromJson<List<String>>(devJson, object : TypeToken<List<String>>() {}.type) ?: emptyList()
                    }.getOrDefault(emptyList())
                    if (discovered.isNotEmpty()) {
                        updateKnownPeers(peerDeviceID, folderID, discovered)
                    }
                }

                val bookmarkName = bookmarkNameFor(_bookmarks.value ?: emptyList(), peerDeviceID, folderID)
                _state.postValue(UiState.FileList(folderID, entries, bookmarkName = bookmarkName, connectedPeerID = connectedPeer.ifEmpty { null }))
            } catch (e: CancellationException) {
                newClient?.close()
                throw e
            } catch (e: Exception) {
                // A cancel aborts the dial loop with an error; swallow it (the
                // user asked to stop) and only surface genuine failures.
                if (connectGuard.allowed() && _state.value is UiState.Connecting) {
                    _state.postValue(UiState.Error(e.message ?: "Connection failed"))
                } else {
                    newClient?.close()
                }
            } finally {
                synchronized(lock) {
                    if (connectingClient === newClient) connectingClient = null
                }
            }
        }
    }

    fun fetchFile(folderID: String, filePath: String): Boolean {
        if (downloadJob?.isActive == true) return false
        val c = synchronized(lock) { client } ?: return false
        cancelGuard.arm()

        downloadJob = viewModelScope.launch(Dispatchers.IO) {
            _download.postValue(DownloadProgress(filePath, 0, -1))
            val cacheDir = getApplication<Application>().cacheDir
            val dest = File(cacheDir, sanitizeFilename(filePath))
            try {
                // Defence in depth against a peer-controlled filePath escaping cacheDir.
                if (!dest.canonicalPath.startsWith(cacheDir.canonicalPath + File.separator)) {
                    throw SecurityException("refusing to write outside cache directory")
                }
                // BEP connections can be dropped between downloads (peer idle timeout,
                // NAT churn). Reconnect transparently before fetching so the user
                // doesn't hit "connection closed" on the second tap.
                if (!c.isConnected) {
                    val saved = savedConnection()
                        ?: throw IllegalStateException("connection lost; please reconnect")
                    val (peerIDs, folder) = saved
                    val primaryPeerID = peerIDs.split(",").first()
                    val hints = savedHints(primaryPeerID, folder)
                    withMulticastLock {
                        c.connect(peerIDs, folder, hints, null)
                    }
                    c.waitForIndex(folder, 30)
                }
                c.fetchFile(folderID, filePath, dest.absolutePath, object : FetchProgress {
                    override fun onProgress(downloaded: Long, total: Long) {
                        if (!cancelGuard.progressAllowed()) return
                        _download.postValue(DownloadProgress(filePath, downloaded, total))
                    }
                    override fun onDone(localPath: String) {
                        try {
                            val result = copyToDownloads(File(localPath), sanitizeFilename(filePath))
                            _completed.postValue(result)
                        } catch (e: Exception) {
                            _errorEvent.postValue("Save to Downloads failed: ${e.message}")
                        } finally {
                            File(localPath).delete()
                            _download.postValue(null)
                        }
                    }
                    override fun onError(msg: String) {
                        _download.postValue(null)
                        _errorEvent.postValue(msg)
                    }
                })
                // FetchFile has returned, so no further progress callbacks can
                // fire (OnProgress runs synchronously inside the blocking call).
                // The cancel path fires neither OnDone nor OnError, so clear the
                // footer here to guarantee it hides; harmless on the success
                // path where OnDone already cleared it.
                _download.postValue(null)
            } catch (e: CancellationException) {
                _download.postValue(null)
                throw e
            } catch (e: Exception) {
                _download.postValue(null)
                _errorEvent.postValue(e.message ?: "Download failed")
            }
        }
        return true
    }

    // Abort the active download. Signals the Go layer to unblock the in-flight
    // block request; FetchFile then returns without firing OnError, so no error
    // event is surfaced. Hides the footer immediately for responsive feedback.
    fun cancelDownload() {
        if (downloadJob?.isActive != true) return
        val c = synchronized(lock) { client }
        cancelGuard.cancel()
        _download.postValue(null)
        _cancelledEvent.postValue(true)
        viewModelScope.launch(Dispatchers.IO) {
            c?.cancelFetch()
        }
    }

    // Fetch a file into the temporary preview cache, then signal the UI to open
    // the preview screen. A fresh cache hit skips the download. Progress and the
    // cancel button reuse the shared `_download` footer; errors reuse
    // `_errorEvent`. Shares `downloadJob`/`cancelGuard` with fetchFile so preview
    // and save never run concurrently (the Go client serves one fetch at a
    // time). Returns false if a download/preview is already in flight.
    fun startPreview(folderID: String, entry: FileEntry, type: PreviewType): Boolean {
        if (downloadJob?.isActive == true) return false
        val c = synchronized(lock) { client } ?: return false
        cancelGuard.arm()

        downloadJob = viewModelScope.launch(Dispatchers.IO) {
            try {
                previewCache.ensureDir()
                previewCache.sweep(PREVIEW_TTL_MS)
                val dest = previewCache.resolve(
                    previewCache.key(folderID, entry.path, entry.modified, entry.size),
                )

                if (previewCache.isFresh(dest, PREVIEW_TTL_MS)) {
                    previewCache.touch(dest)
                    _previewReady.postValue(PreviewReady(entry.name, type, dest))
                    return@launch
                }

                // Only now (an actual fetch) show the footer, so an instant
                // cache hit doesn't flash it.
                _download.postValue(DownloadProgress(entry.path, 0, -1))

                // BEP connections can drop between operations; reconnect
                // transparently so a preview doesn't fail on a stale connection.
                if (!c.isConnected) {
                    val saved = savedConnection()
                        ?: throw IllegalStateException("connection lost; please reconnect")
                    val (peerIDs, folder) = saved
                    val primaryPeerID = peerIDs.split(",").first()
                    val hints = savedHints(primaryPeerID, folder)
                    withMulticastLock { c.connect(peerIDs, folder, hints, null) }
                    c.waitForIndex(folder, 30)
                }

                // Download to a .part file and rename on success so a partial
                // transfer is never mistaken for a complete cache entry.
                val part = File(dest.absolutePath + ".part")
                c.fetchFile(folderID, entry.path, part.absolutePath, object : FetchProgress {
                    override fun onProgress(downloaded: Long, total: Long) {
                        if (!cancelGuard.progressAllowed()) return
                        _download.postValue(DownloadProgress(entry.path, downloaded, total))
                    }
                    override fun onDone(localPath: String) {
                        val src = File(localPath)
                        dest.delete() // clear any stale entry so renameTo can't fail on it
                        _download.postValue(null)
                        if (src.renameTo(dest)) {
                            _previewReady.postValue(PreviewReady(entry.name, type, dest))
                        } else {
                            src.delete()
                            _errorEvent.postValue("Could not cache preview")
                        }
                    }
                    override fun onError(msg: String) {
                        part.delete()
                        _download.postValue(null)
                        _errorEvent.postValue(msg)
                    }
                })
                // Cancel fires neither callback; clear the footer as a safety net.
                _download.postValue(null)
            } catch (e: CancellationException) {
                _download.postValue(null)
                throw e
            } catch (e: Exception) {
                _download.postValue(null)
                _errorEvent.postValue(e.message ?: "Preview failed")
            }
        }
        return true
    }

    fun acknowledgePreviewReady() {
        _previewReady.value = null
    }

    // Save an already-cached previewed file to Downloads without re-fetching it.
    fun savePreviewToDownloads(cachedPath: String, displayName: String) {
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val result = copyToDownloads(File(cachedPath), sanitizeFilename(displayName))
                _completed.postValue(result)
            } catch (e: Exception) {
                _errorEvent.postValue("Save to Downloads failed: ${e.message}")
            }
        }
    }

    fun refreshListing() {
        val current = _state.value as? UiState.FileList ?: return
        val c = synchronized(lock) { client } ?: return
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val json = String(c.listFolder(current.folderID))
                val type = object : TypeToken<List<FileEntry>>() {}.type
                val entries: List<FileEntry> = gson.fromJson(json, type) ?: emptyList()
                _state.postValue(current.copy(allEntries = entries))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.postValue(UiState.Error(e.message ?: "Refresh failed"))
            }
        }
    }

    fun navigateInto(dirPath: String) {
        val state = _state.value
        if (state is UiState.FileList) _state.value = state.copy(currentDir = dirPath)
    }

    fun navigateUp(): Boolean {
        val state = _state.value
        if (state is UiState.FileList && state.currentDir.isNotEmpty()) {
            val parent = state.currentDir.substringBeforeLast('/', "")
            _state.value = state.copy(currentDir = parent)
            return true
        }
        return false
    }

    fun disconnect() {
        // Suppress any late OnDialing posts before tearing things down, so a
        // callback already in flight can't re-show the connecting dialog.
        connectGuard.cancel()
        // Abort the in-flight dial loop. cancelConnect() is non-blocking; the
        // connect coroutine then unwinds and closes its own client. We don't
        // close it here to avoid blocking this (main) thread on connection
        // teardown.
        val connecting = synchronized(lock) { connectingClient }
        connecting?.cancelConnect()
        connectJob?.cancel()
        connectJob = null
        synchronized(lock) {
            // Abort any in-flight download first so it stops cleanly rather than
            // surfacing a "connection closed" error once we tear down the conn.
            client?.cancelFetch()
            client?.close()
            client = null
        }
        _state.value = UiState.Idle
        _download.value = null
        _completed.value = null
        _errorEvent.value = null
        _cancelledEvent.value = null
        _previewReady.value = null
    }

    override fun onCleared() {
        synchronized(lock) {
            client?.close()
            client = null
        }
    }

    private inline fun <T> withMulticastLock(block: () -> T): T {
        val wifi = getApplication<Application>()
            .applicationContext
            .getSystemService(Context.WIFI_SERVICE) as? WifiManager
        val lock = wifi?.createMulticastLock("unsyncthing-discovery")?.apply {
            setReferenceCounted(false)
            acquire()
        }
        try {
            return block()
        } finally {
            try { lock?.release() } catch (_: Throwable) {}
        }
    }

    private fun ensureCert(): CertData {
        synchronized(lock) {
            cert?.let { return it }
            val savedCert = prefs.getString("certPEM",  null)
            val savedKey  = prefs.getString("keyPEM",   null)
            val savedID   = prefs.getString("deviceID", null)
            if (savedCert != null && savedKey != null && savedID != null) {
                val c = CertData(savedCert, savedKey, savedID)
                cert = c
                return c
            }
            val result = gson.fromJson(String(Stclient.generateCert()), CertData::class.java)
            prefs.edit()
                .putString("certPEM",  result.certPEM)
                .putString("keyPEM",   result.keyPEM)
                .putString("deviceID", result.deviceID)
                .apply()
            cert = result
            return result
        }
    }

    fun acknowledgeCompletion() {
        _completed.value = null
    }

    fun acknowledgeError() {
        _errorEvent.value = null
    }

    fun acknowledgeCancelled() {
        _cancelledEvent.value = null
    }

    fun acknowledgeUiError() {
        if (_state.value is UiState.Error) _state.value = UiState.Idle
    }

    private fun copyToDownloads(src: File, displayName: String): DownloadCompleted {
        // Infer MIME from the display name's extension, not the source file:
        // preview cache files have a hashed, extension-less name.
        val mime = MimeTypeMap.getSingleton()
            .getMimeTypeFromExtension(displayName.substringAfterLast('.', "").lowercase())
            ?: "application/octet-stream"

        val resolver = getApplication<Application>().contentResolver
        val collection = MediaStore.Downloads.EXTERNAL_CONTENT_URI
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, displayName)
            put(MediaStore.Downloads.MIME_TYPE, mime)
            put(MediaStore.Downloads.IS_PENDING, 1)
        }

        val uri = resolver.insert(collection, values)
            ?: throw IllegalStateException("MediaStore refused the insert")

        try {
            resolver.openOutputStream(uri).use { out ->
                requireNotNull(out) { "openOutputStream returned null" }
                src.inputStream().use { it.copyTo(out) }
            }
            values.clear()
            values.put(MediaStore.Downloads.IS_PENDING, 0)
            resolver.update(uri, values, null, null)
        } catch (e: Exception) {
            // Roll back the pending row so it doesn't litter Downloads.
            runCatching { resolver.delete(uri, null, null) }
            throw e
        }

        return DownloadCompleted(displayName, uri, mime)
    }

}

internal fun removeBookmark(existing: List<Bookmark>, peerID: String, folderID: String): List<Bookmark> =
    existing.filterNot { it.peerID == peerID && it.folderID == folderID }

internal fun bookmarkNameFor(bookmarks: List<Bookmark>, peerID: String, folderID: String): String? =
    bookmarks.firstOrNull { it.peerID == peerID && it.folderID == folderID }?.name

// mergeKnownPeers turns the device IDs the peer reported for a folder into the
// fallback set stored on a bookmark: drop the primary (it's the identity key,
// tried first and stored separately) and de-duplicate.
internal fun mergeKnownPeers(discovered: List<String>, primary: String): List<String> =
    discovered.filter { it != primary }.distinct()

internal fun upsertBookmark(existing: List<Bookmark>, new: Bookmark): List<Bookmark> {
    val idx = existing.indexOfFirst { it.peerID == new.peerID && it.folderID == new.folderID }
    return if (idx >= 0) existing.toMutableList().apply { set(idx, new) }
           else existing + new
}

internal fun sanitizeFilename(filePath: String): String {
    val basename = filePath.substringAfterLast('/').substringAfterLast('\\')
    val cleaned = basename.filter { it.isLetterOrDigit() || it in "._-" }
    return when {
        cleaned.isBlank() -> "download"
        cleaned == "." || cleaned == ".." -> "download"
        else -> cleaned
    }
}
