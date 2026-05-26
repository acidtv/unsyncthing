package com.acidtv.unsyncthing

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.Menu
import android.view.MenuItem
import android.view.View
import android.widget.Toast
import androidx.activity.viewModels
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.DividerItemDecoration
import androidx.recyclerview.widget.LinearLayoutManager
import com.acidtv.unsyncthing.databinding.ActivityMainBinding
import com.google.android.material.snackbar.Snackbar

class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private val vm: SyncthingViewModel by viewModels()
    private lateinit var adapter: FileListAdapter
    private var menuRefresh: MenuItem? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        adapter = FileListAdapter { entry ->
            if (entry.isDir) {
                vm.navigateInto(entry.path)
                return@FileListAdapter
            }
            val state = vm.state.value
            if (state is UiState.FileList) {
                if (!vm.fetchFile(state.folderID, entry.path)) {
                    Toast.makeText(this, "Download already in progress", Toast.LENGTH_SHORT).show()
                }
            }
        }

        binding.recycler.apply {
            layoutManager = LinearLayoutManager(this@MainActivity)
            addItemDecoration(DividerItemDecoration(this@MainActivity, DividerItemDecoration.VERTICAL))
            adapter = this@MainActivity.adapter
        }

        vm.savedConnection()?.let { (peerID, folder) ->
            binding.etPeerID.setText(peerID)
            binding.etFolder.setText(folder)
        }

        binding.btnConnect.setOnClickListener {
            val peerID = binding.etPeerID.text.toString().trim()
            val folder = binding.etFolder.text.toString().trim()
            if (peerID.isBlank() || folder.isBlank()) {
                Toast.makeText(this, "Fill in all fields", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            vm.connect(peerID, folder)
        }

        vm.deviceID.observe(this) { id ->
            binding.tvDeviceID.text = if (id != null) "My device ID:\n$id" else "Generating identity…"
            refreshConnectButton()
        }

        vm.state.observe(this) { state ->
            when (state) {
                is UiState.Idle -> {
                    binding.connectForm.visibility = View.VISIBLE
                    menuRefresh?.isVisible = false
                    binding.tvFolderHeader.visibility = View.GONE
                    refreshConnectButton()
                    if (vm.download.value == null) binding.tvStatus.text = ""
                }
                is UiState.Connecting -> {
                    binding.connectForm.visibility = View.VISIBLE
                    menuRefresh?.isVisible = false
                    binding.tvFolderHeader.visibility = View.GONE
                    binding.btnConnect.isEnabled = false
                    binding.tvStatus.text = state.status
                }
                is UiState.FileList -> {
                    binding.connectForm.visibility = View.GONE
                    menuRefresh?.isVisible = true
                    binding.tvFolderHeader.visibility = View.VISIBLE
                    binding.tvFolderHeader.text = state.folderID
                    adapter.submitList(state.entries)
                    if (vm.download.value == null) {
                        binding.tvStatus.text = statusText(state)
                    }
                }
                is UiState.Error -> {
                    binding.connectForm.visibility = View.VISIBLE
                    menuRefresh?.isVisible = false
                    binding.tvFolderHeader.visibility = View.GONE
                    refreshConnectButton()
                    // Show the full message in tvStatus (no truncation) so long
                    // combined errors like "local: …; global: …" are readable.
                    binding.tvStatus.text = state.message
                    android.util.Log.w("unsyncthing", "error: ${state.message}")
                }
            }
        }

        vm.download.observe(this) { dl ->
            if (dl != null) {
                binding.downloadFooter.visibility = View.VISIBLE
                val filename = dl.path.substringAfterLast('/')
                if (dl.total > 0) {
                    binding.progressDownload.isIndeterminate = false
                    val pct = (dl.downloaded * 100 / dl.total).toInt()
                    binding.progressDownload.setProgressCompat(pct, true)
                    binding.tvDownloadLabel.text = "Downloading $filename — $pct%"
                } else {
                    binding.progressDownload.isIndeterminate = true
                    binding.tvDownloadLabel.text = "Connecting… $filename"
                }
            } else {
                binding.downloadFooter.visibility = View.GONE
                binding.progressDownload.isIndeterminate = false
                binding.progressDownload.setProgressCompat(0, false)
                val state = vm.state.value
                if (state is UiState.FileList) {
                    binding.tvStatus.text = statusText(state)
                }
            }
        }

        vm.completed.observe(this) { done ->
            if (done == null) return@observe
            Snackbar.make(binding.root, "Saved to Downloads/${done.displayName}", Snackbar.LENGTH_LONG)
                .setAction("Open") { openFile(done.uri, done.mimeType) }
                .show()
            vm.acknowledgeCompletion()
        }
    }

    override fun onCreateOptionsMenu(menu: Menu): Boolean {
        menuInflater.inflate(R.menu.menu_main, menu)
        menuRefresh = menu.findItem(R.id.action_refresh)
        menuRefresh?.isVisible = vm.state.value is UiState.FileList
        return true
    }

    override fun onOptionsItemSelected(item: MenuItem): Boolean {
        if (item.itemId == R.id.action_refresh) {
            vm.refreshListing()
            return true
        }
        return super.onOptionsItemSelected(item)
    }

    private fun openFile(uri: Uri, mimeType: String) {
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, mimeType)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        try {
            startActivity(Intent.createChooser(intent, "Open with…"))
        } catch (_: ActivityNotFoundException) {
            Toast.makeText(this, "No app can open this file", Toast.LENGTH_SHORT).show()
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (!vm.navigateUp()) @Suppress("DEPRECATION") super.onBackPressed()
    }

    private fun refreshConnectButton() {
        binding.btnConnect.isEnabled = vm.deviceID.value != null
    }

    private fun statusText(state: UiState.FileList): String {
        val count = "${state.entries.size} items"
        return if (state.currentDir.isEmpty()) count else "${state.currentDir}  ($count)"
    }
}
