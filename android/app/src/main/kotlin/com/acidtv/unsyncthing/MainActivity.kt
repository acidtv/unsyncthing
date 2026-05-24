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
            val state = vm.state.value
            if (state is UiState.FileList) {
                if (!entry.isDir) vm.fetchFile(state.folderID, entry.path)
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

        binding.tvDeviceID.text = "My device ID:\n${vm.deviceID}"

        vm.state.observe(this) { state ->
            when (state) {
                is UiState.Idle -> showConnectForm(enabled = true)
                is UiState.Connecting -> {
                    showConnectForm(enabled = false)
                    binding.tvStatus.text = "Connecting…"
                }
                is UiState.Connected -> {
                    binding.tvStatus.text = "Connected — loading index…"
                }
                is UiState.FileList -> {
                    binding.connectForm.visibility = View.GONE
                    binding.tvStatus.text = "${state.folderID}  (${state.entries.size} items)"
                    adapter.submitList(state.entries)
                }
                is UiState.Downloading -> {
                    val pct = if (state.total > 0) (state.downloaded * 100 / state.total) else 0
                    binding.tvStatus.text = "Downloading ${state.path} — $pct%"
                }
                is UiState.Error -> {
                    showConnectForm(enabled = true)
                    Toast.makeText(this, state.message, Toast.LENGTH_LONG).show()
                }
            }
        }
    }

    private fun showConnectForm(enabled: Boolean) {
        binding.connectForm.visibility = View.VISIBLE
        binding.btnConnect.isEnabled = enabled
        binding.tvStatus.text = if (enabled) "" else "Connecting…"
    }
}
