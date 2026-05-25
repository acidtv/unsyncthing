package com.acidtv.unsyncthing

import android.app.Application
import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.LiveData
import androidx.lifecycle.MutableLiveData
import androidx.lifecycle.viewModelScope
import com.acidtv.unsyncthing.stclient.Client
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
    object Connecting : UiState()
    data class FileList(
        val folderID: String,
        val allEntries: List<FileEntry>,
        val currentDir: String = "",
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

private data class CertData(
    @SerializedName("CertPEM")  val certPEM: String,
    @SerializedName("KeyPEM")   val keyPEM: String,
    @SerializedName("DeviceID") val deviceID: String,
)

class SyncthingViewModel(app: Application) : AndroidViewModel(app) {

    private val prefs = app.getSharedPreferences("unsyncthing", Context.MODE_PRIVATE)
    private val gson = Gson()

    // All client/cert mutation goes through `lock`.
    private val lock = Any()
    private var client: Client? = null
    private var cert: CertData? = null
    private var downloadJob: Job? = null

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

    // Null while the cert is being generated on first launch.
    private val _deviceID = MutableLiveData<String?>(null)
    val deviceID: LiveData<String?> = _deviceID

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

    fun savedConnection(): Triple<String, String, String>? {
        val addr   = prefs.getString("lastAddr",   null) ?: return null
        val peerID = prefs.getString("lastPeerID", null) ?: return null
        val folder = prefs.getString("lastFolder", null) ?: return null
        return Triple(addr, peerID, folder)
    }

    fun connect(addr: String, peerDeviceID: String, folderID: String) {
        prefs.edit()
            .putString("lastAddr",   addr)
            .putString("lastPeerID", peerDeviceID)
            .putString("lastFolder", folderID)
            .apply()
        _state.value = UiState.Connecting
        viewModelScope.launch(Dispatchers.IO) {
            try {
                val c = ensureCert()
                val newClient = Client(c.certPEM, c.keyPEM)
                newClient.connect(addr, peerDeviceID, folderID)
                newClient.waitForIndex(folderID, 30)

                val json = String(newClient.listFolder(folderID))
                val type = object : TypeToken<List<FileEntry>>() {}.type
                val entries: List<FileEntry> = gson.fromJson(json, type) ?: emptyList()

                synchronized(lock) {
                    client?.close()
                    client = newClient
                }

                _state.postValue(UiState.FileList(folderID, entries))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.postValue(UiState.Error(e.message ?: "Connection failed"))
            }
        }
    }

    fun fetchFile(folderID: String, filePath: String) {
        // Ignore taps while a download is already running.
        if (downloadJob?.isActive == true) return
        val c = synchronized(lock) { client } ?: return

        downloadJob = viewModelScope.launch(Dispatchers.IO) {
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
                    val (addr, peerID, folder) = saved
                    c.connect(addr, peerID, folder)
                    c.waitForIndex(folder, 30)
                }
                c.fetchFile(folderID, filePath, dest.absolutePath, object : FetchProgress {
                    override fun onProgress(downloaded: Long, total: Long) {
                        _download.postValue(DownloadProgress(filePath, downloaded, total))
                    }
                    override fun onDone(localPath: String) {
                        try {
                            val result = copyToDownloads(File(localPath), sanitizeFilename(filePath))
                            _completed.postValue(result)
                        } catch (e: Exception) {
                            _state.postValue(UiState.Error("Save to Downloads failed: ${e.message}"))
                        } finally {
                            File(localPath).delete()
                            _download.postValue(null)
                        }
                    }
                    override fun onError(msg: String) {
                        _download.postValue(null)
                        _state.postValue(UiState.Error(msg))
                    }
                })
            } catch (e: CancellationException) {
                _download.postValue(null)
                throw e
            } catch (e: Exception) {
                _download.postValue(null)
                _state.postValue(UiState.Error(e.message ?: "Download failed"))
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
        synchronized(lock) {
            client?.close()
            client = null
        }
        _state.value = UiState.Idle
        _download.value = null
    }

    override fun onCleared() {
        synchronized(lock) {
            client?.close()
            client = null
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

    private fun copyToDownloads(src: File, displayName: String): DownloadCompleted {
        val mime = MimeTypeMap.getSingleton()
            .getMimeTypeFromExtension(src.extension.lowercase())
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

    private fun sanitizeFilename(filePath: String): String {
        val basename = filePath.substringAfterLast('/').substringAfterLast('\\')
        val cleaned = basename.filter { it.isLetterOrDigit() || it in "._-" }
        return when {
            cleaned.isBlank() -> "download"
            cleaned == "." || cleaned == ".." -> "download"
            else -> cleaned
        }
    }
}
