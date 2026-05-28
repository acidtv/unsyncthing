package com.acidtv.unsyncthing

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.LayoutInflater
import android.view.Menu
import android.view.MenuInflater
import android.view.MenuItem
import android.view.View
import android.view.ViewGroup
import android.widget.Toast
import androidx.activity.OnBackPressedCallback
import androidx.core.view.MenuProvider
import androidx.fragment.app.Fragment
import androidx.fragment.app.activityViewModels
import androidx.lifecycle.Lifecycle
import androidx.recyclerview.widget.DividerItemDecoration
import androidx.recyclerview.widget.LinearLayoutManager
import com.acidtv.unsyncthing.databinding.FragmentFileListBinding
import com.google.android.material.snackbar.Snackbar

class FileListFragment : Fragment() {

    private val vm: SyncthingViewModel by activityViewModels()
    private var _binding: FragmentFileListBinding? = null
    private val binding get() = _binding!!
    private lateinit var adapter: FileListAdapter

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        _binding = FragmentFileListBinding.inflate(inflater, container, false)
        return binding.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        adapter = FileListAdapter { entry ->
            if (entry.isDir) {
                vm.navigateInto(entry.path)
                return@FileListAdapter
            }
            val state = vm.state.value
            if (state is UiState.FileList) {
                if (!vm.fetchFile(state.folderID, entry.path)) {
                    Toast.makeText(requireContext(), "Download already in progress", Toast.LENGTH_SHORT).show()
                }
            }
        }

        binding.recycler.apply {
            layoutManager = LinearLayoutManager(requireContext())
            addItemDecoration(DividerItemDecoration(requireContext(), DividerItemDecoration.VERTICAL))
            adapter = this@FileListFragment.adapter
        }

        requireActivity().addMenuProvider(object : MenuProvider {
            override fun onCreateMenu(menu: Menu, menuInflater: MenuInflater) {
                menuInflater.inflate(R.menu.menu_main, menu)
            }
            override fun onMenuItemSelected(item: MenuItem): Boolean {
                return when (item.itemId) {
                    R.id.action_refresh -> { vm.refreshListing(); true }
                    R.id.action_disconnect -> { vm.disconnect(); true }
                    else -> false
                }
            }
        }, viewLifecycleOwner, Lifecycle.State.RESUMED)

        requireActivity().onBackPressedDispatcher.addCallback(
            viewLifecycleOwner,
            object : OnBackPressedCallback(true) {
                override fun handleOnBackPressed() {
                    if (!vm.navigateUp()) vm.disconnect()
                }
            },
        )

        vm.state.observe(viewLifecycleOwner) { state ->
            if (state is UiState.FileList) {
                binding.tvFolderHeader.text = state.folderID
                binding.tvStatus.text = statusText(state)
                adapter.submitList(state.entries)
            }
        }

        vm.download.observe(viewLifecycleOwner) { dl ->
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
            }
        }

        vm.completed.observe(viewLifecycleOwner) { done ->
            if (done == null) return@observe
            Snackbar.make(binding.root, "Saved to Downloads/${done.displayName}", Snackbar.LENGTH_LONG)
                .setAction("Open") { openFile(done.uri, done.mimeType) }
                .show()
            vm.acknowledgeCompletion()
        }

        vm.errorEvent.observe(viewLifecycleOwner) { msg ->
            if (msg == null) return@observe
            Snackbar.make(binding.root, msg, Snackbar.LENGTH_LONG).show()
            android.util.Log.w("unsyncthing", "error: $msg")
            vm.acknowledgeError()
        }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        _binding = null
    }

    private fun openFile(uri: Uri, mimeType: String) {
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, mimeType)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        try {
            startActivity(Intent.createChooser(intent, "Open with…"))
        } catch (_: ActivityNotFoundException) {
            Toast.makeText(requireContext(), "No app can open this file", Toast.LENGTH_SHORT).show()
        }
    }

    private fun statusText(state: UiState.FileList): String {
        val count = "${state.entries.size} items"
        return if (state.currentDir.isEmpty()) count else "${state.currentDir}  ($count)"
    }
}
