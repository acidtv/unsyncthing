package com.acidtv.unsyncthing

import android.app.Dialog
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.PopupMenu
import android.widget.Toast
import androidx.fragment.app.Fragment
import androidx.fragment.app.activityViewModels
import androidx.recyclerview.widget.DividerItemDecoration
import androidx.recyclerview.widget.LinearLayoutManager
import com.acidtv.unsyncthing.databinding.DialogAddConnectionBinding
import com.acidtv.unsyncthing.databinding.FragmentConnectBinding
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.snackbar.Snackbar

class ConnectFragment : Fragment() {

    private val vm: SyncthingViewModel by activityViewModels()
    private var _binding: FragmentConnectBinding? = null
    private val binding get() = _binding!!
    private lateinit var adapter: BookmarkAdapter

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        _binding = FragmentConnectBinding.inflate(inflater, container, false)
        return binding.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        adapter = BookmarkAdapter(
            onTap = { bookmark -> vm.connect(bookmark.peerID, bookmark.folderID) },
            onMenuClick = { bookmark, anchor -> showBookmarkMenu(bookmark, anchor) },
        )

        binding.rvBookmarks.apply {
            layoutManager = LinearLayoutManager(requireContext())
            addItemDecoration(DividerItemDecoration(requireContext(), DividerItemDecoration.VERTICAL))
            adapter = this@ConnectFragment.adapter
        }

        binding.btnAddConnection.setOnClickListener {
            AddConnectionDialogFragment().show(parentFragmentManager, AddConnectionDialogFragment.TAG)
        }

        vm.deviceID.observe(viewLifecycleOwner) { id ->
            binding.tvDeviceID.text = if (id != null) "My device ID:\n$id" else "Generating identity…"
            binding.btnAddConnection.isEnabled = id != null
        }

        vm.bookmarks.observe(viewLifecycleOwner) { list ->
            adapter.submitList(list)
            binding.tvEmpty.visibility = if (list.isEmpty()) View.VISIBLE else View.GONE
        }

        vm.state.observe(viewLifecycleOwner) { state ->
            val fm = parentFragmentManager
            val existing = fm.findFragmentByTag(ConnectingDialogFragment.TAG)
            when (state) {
                is UiState.Connecting -> {
                    if (existing == null) {
                        ConnectingDialogFragment().show(fm, ConnectingDialogFragment.TAG)
                    }
                }
                is UiState.Error -> {
                    Snackbar.make(binding.root, state.message, Snackbar.LENGTH_LONG).show()
                    android.util.Log.w("unsyncthing", "error: ${state.message}")
                    vm.acknowledgeUiError()
                }
                else -> Unit
            }
        }
    }

    private fun showBookmarkMenu(bookmark: Bookmark, anchor: View) {
        val popup = PopupMenu(requireContext(), anchor)
        popup.menuInflater.inflate(R.menu.menu_bookmark, popup.menu)
        popup.setOnMenuItemClickListener { item ->
            when (item.itemId) {
                R.id.action_connect -> { vm.connect(bookmark.peerID, bookmark.folderID); true }
                R.id.action_edit    -> { showEditDialog(bookmark); true }
                R.id.action_delete  -> { confirmDelete(bookmark); true }
                else                -> false
            }
        }
        popup.show()
    }

    private fun showEditDialog(bookmark: Bookmark) {
        val dialogBinding = DialogAddConnectionBinding.inflate(layoutInflater)
        dialogBinding.etName.setText(bookmark.name)
        dialogBinding.etPeerID.setText(bookmark.peerID)
        dialogBinding.etFolder.setText(bookmark.folderID)
        dialogBinding.tvNameHint.visibility = View.GONE

        val dialog = MaterialAlertDialogBuilder(requireContext())
            .setTitle("Edit bookmark")
            .setView(dialogBinding.root)
            .setPositiveButton("Save", null)
            .setNegativeButton("Cancel") { d, _ -> d.dismiss() }
            .create()

        dialog.setOnShowListener {
            dialog.getButton(Dialog.BUTTON_POSITIVE).setOnClickListener {
                val name   = dialogBinding.etName.text.toString().trim()
                val peerID = dialogBinding.etPeerID.text.toString().trim()
                val folder = dialogBinding.etFolder.text.toString().trim()
                if (name.isBlank()) {
                    Toast.makeText(requireContext(), "Bookmark name is required", Toast.LENGTH_SHORT).show()
                    return@setOnClickListener
                }
                if (peerID.isBlank() || folder.isBlank()) {
                    Toast.makeText(requireContext(), "Peer ID and folder are required", Toast.LENGTH_SHORT).show()
                    return@setOnClickListener
                }
                vm.saveBookmark(name, peerID, folder)
                dialog.dismiss()
            }
        }
        dialog.show()
    }

    private fun confirmDelete(bookmark: Bookmark) {
        MaterialAlertDialogBuilder(requireContext())
            .setTitle("Delete bookmark?")
            .setMessage("Remove \"${bookmark.name}\"?")
            .setPositiveButton("Delete") { _, _ -> vm.deleteBookmark(bookmark.peerID, bookmark.folderID) }
            .setNegativeButton("Cancel", null)
            .show()
    }

    override fun onDestroyView() {
        super.onDestroyView()
        _binding = null
    }
}
