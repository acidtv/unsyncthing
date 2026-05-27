package com.acidtv.unsyncthing

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Toast
import androidx.fragment.app.Fragment
import androidx.fragment.app.activityViewModels
import com.acidtv.unsyncthing.databinding.FragmentConnectBinding

class ConnectFragment : Fragment() {

    private val vm: SyncthingViewModel by activityViewModels()
    private var _binding: FragmentConnectBinding? = null
    private val binding get() = _binding!!

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        _binding = FragmentConnectBinding.inflate(inflater, container, false)
        return binding.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        vm.savedConnection()?.let { (peerID, folder) ->
            binding.etPeerID.setText(peerID)
            binding.etFolder.setText(folder)
        }

        binding.btnConnect.setOnClickListener {
            val peerID = binding.etPeerID.text.toString().trim()
            val folder = binding.etFolder.text.toString().trim()
            if (peerID.isBlank() || folder.isBlank()) {
                Toast.makeText(requireContext(), "Fill in all fields", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            vm.connect(peerID, folder)
        }

        vm.deviceID.observe(viewLifecycleOwner) { id ->
            binding.tvDeviceID.text = if (id != null) "My device ID:\n$id" else "Generating identity…"
            refreshConnectButton()
        }

        vm.state.observe(viewLifecycleOwner) { state ->
            when (state) {
                is UiState.Idle -> {
                    refreshConnectButton()
                    binding.tvStatus.text = ""
                }
                is UiState.Connecting -> {
                    binding.btnConnect.isEnabled = false
                    binding.tvStatus.text = state.status
                }
                is UiState.Error -> {
                    refreshConnectButton()
                    binding.tvStatus.text = state.message
                    android.util.Log.w("unsyncthing", "error: ${state.message}")
                }
                is UiState.FileList -> Unit // Activity will swap us out.
            }
        }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        _binding = null
    }

    private fun refreshConnectButton() {
        binding.btnConnect.isEnabled = vm.deviceID.value != null
    }
}
