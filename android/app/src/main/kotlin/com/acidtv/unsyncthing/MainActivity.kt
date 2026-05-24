package com.acidtv.unsyncthing

import android.os.Bundle
import android.view.View
import android.widget.Toast
import androidx.activity.viewModels
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.DividerItemDecoration
import androidx.recyclerview.widget.LinearLayoutManager
import com.acidtv.unsyncthing.databinding.ActivityMainBinding

class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private val vm: SyncthingViewModel by viewModels()
    private lateinit var adapter: FileListAdapter

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
                vm.fetchFile(state.folderID, entry.path)
            }
        }

        binding.recycler.apply {
            layoutManager = LinearLayoutManager(this@MainActivity)
            addItemDecoration(DividerItemDecoration(this@MainActivity, DividerItemDecoration.VERTICAL))
            adapter = this@MainActivity.adapter
        }

        binding.btnConnect.setOnClickListener {
            val addr = binding.etAddr.text.toString().trim()
            val peerID = binding.etPeerID.text.toString().trim()
            val folder = binding.etFolder.text.toString().trim()
            if (addr.isBlank() || peerID.isBlank() || folder.isBlank()) {
                Toast.makeText(this, "Fill in all fields", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            vm.connect(addr, peerID, folder)
        }

        vm.deviceID.observe(this) { id ->
            binding.tvDeviceID.text = if (id != null) "My device ID:\n$id" else "Generating identity…"
            refreshConnectButton()
        }

        vm.state.observe(this) { state ->
            when (state) {
                is UiState.Idle -> {
                    binding.connectForm.visibility = View.VISIBLE
                    refreshConnectButton()
                    if (vm.download.value == null) binding.tvStatus.text = ""
                }
                is UiState.Connecting -> {
                    binding.connectForm.visibility = View.VISIBLE
                    binding.btnConnect.isEnabled = false
                    binding.tvStatus.text = "Connecting…"
                }
                is UiState.FileList -> {
                    binding.connectForm.visibility = View.GONE
                    adapter.submitList(state.entries)
                    if (vm.download.value == null) {
                        binding.tvStatus.text = statusText(state)
                    }
                }
                is UiState.Error -> {
                    binding.connectForm.visibility = View.VISIBLE
                    refreshConnectButton()
                    binding.tvStatus.text = ""
                    Toast.makeText(this, state.message, Toast.LENGTH_LONG).show()
                }
            }
        }

        vm.download.observe(this) { dl ->
            if (dl != null) {
                val pct = if (dl.total > 0) (dl.downloaded * 100 / dl.total) else 0
                binding.tvStatus.text = "Downloading ${dl.path} — $pct%"
            } else {
                val state = vm.state.value
                if (state is UiState.FileList) {
                    binding.tvStatus.text = statusText(state)
                }
            }
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
        val path = if (state.currentDir.isEmpty()) state.folderID
                   else "${state.folderID}/${state.currentDir}"
        return "$path  (${state.entries.size} items)"
    }
}
