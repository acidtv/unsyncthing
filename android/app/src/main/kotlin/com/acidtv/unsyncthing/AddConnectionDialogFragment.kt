package com.acidtv.unsyncthing

import android.app.Dialog
import android.os.Bundle
import android.widget.Toast
import androidx.fragment.app.DialogFragment
import androidx.fragment.app.activityViewModels
import com.acidtv.unsyncthing.databinding.DialogAddConnectionBinding
import com.google.android.material.dialog.MaterialAlertDialogBuilder

class AddConnectionDialogFragment : DialogFragment() {

    private val vm: SyncthingViewModel by activityViewModels()

    override fun onCreateDialog(savedInstanceState: Bundle?): Dialog {
        val binding = DialogAddConnectionBinding.inflate(layoutInflater)
        val dialog = MaterialAlertDialogBuilder(requireContext())
            .setTitle("Add connection")
            .setView(binding.root)
            .setPositiveButton("Connect", null)
            .setNegativeButton("Cancel") { d, _ -> d.dismiss() }
            .create()

        dialog.setOnShowListener {
            dialog.getButton(Dialog.BUTTON_POSITIVE).setOnClickListener {
                val name = binding.etName.text.toString().trim()
                val peerID = binding.etPeerID.text.toString().trim()
                val folder = binding.etFolder.text.toString().trim()
                if (peerID.isBlank() || folder.isBlank()) {
                    Toast.makeText(requireContext(), "Peer ID and folder are required", Toast.LENGTH_SHORT).show()
                    return@setOnClickListener
                }
                if (name.isNotBlank()) {
                    vm.saveBookmark(name, peerID, folder)
                }
                vm.connect(peerID, folder)
                dismiss()
            }
        }
        return dialog
    }

    companion object {
        const val TAG = "add_connection"
    }
}
