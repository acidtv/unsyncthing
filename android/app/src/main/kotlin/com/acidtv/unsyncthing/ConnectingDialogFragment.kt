package com.acidtv.unsyncthing

import android.app.Dialog
import android.os.Bundle
import androidx.fragment.app.DialogFragment
import androidx.fragment.app.activityViewModels
import com.acidtv.unsyncthing.databinding.DialogConnectingBinding
import com.google.android.material.dialog.MaterialAlertDialogBuilder

class ConnectingDialogFragment : DialogFragment() {

    private val vm: SyncthingViewModel by activityViewModels()
    private var _binding: DialogConnectingBinding? = null

    override fun onCreateDialog(savedInstanceState: Bundle?): Dialog {
        _binding = DialogConnectingBinding.inflate(layoutInflater)
        isCancelable = false
        val dialog = MaterialAlertDialogBuilder(requireContext())
            .setTitle("Connecting")
            .setView(_binding!!.root)
            .setNegativeButton("Cancel") { _, _ -> vm.disconnect() }
            .create()

        vm.state.observe(this) { state ->
            when (state) {
                is UiState.Connecting -> _binding?.tvConnectingStatus?.text = state.status
                else -> dismissAllowingStateLoss()
            }
        }
        return dialog
    }

    override fun onDestroyView() {
        super.onDestroyView()
        _binding = null
    }

    companion object {
        const val TAG = "connecting"
    }
}
