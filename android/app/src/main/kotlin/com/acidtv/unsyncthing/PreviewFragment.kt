package com.acidtv.unsyncthing

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.appcompat.app.AppCompatActivity
import androidx.fragment.app.Fragment
import androidx.fragment.app.activityViewModels
import com.acidtv.unsyncthing.databinding.FragmentPreviewBinding
import com.google.android.material.snackbar.Snackbar
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import androidx.lifecycle.lifecycleScope
import java.io.File

// Full-screen preview of a single file. Tap-to-preview pushes this fragment;
// the file is fetched into the temporary cache and rendered by kind (text for
// now). Saving to Downloads remains available from the toolbar menu.
class PreviewFragment : Fragment() {

    private val vm: SyncthingViewModel by activityViewModels()
    private var _binding: FragmentPreviewBinding? = null
    private val binding get() = _binding!!

    private val folderID get() = requireArguments().getString(ARG_FOLDER, "")
    private val path get() = requireArguments().getString(ARG_PATH, "")
    private val name get() = requireArguments().getString(ARG_NAME, "")

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        _binding = FragmentPreviewBinding.inflate(inflater, container, false)
        return binding.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        // The preview screen carries its own MaterialToolbar; hide the Activity
        // ActionBar so they don't stack. Restored in onDestroyView.
        (activity as? AppCompatActivity)?.supportActionBar?.hide()

        binding.toolbar.title = name
        binding.toolbar.setNavigationOnClickListener { parentFragmentManager.popBackStack() }
        binding.toolbar.setOnMenuItemClickListener { item ->
            if (item.itemId == R.id.action_save) {
                if (!vm.fetchFile(folderID, path)) {
                    Snackbar.make(binding.root, "Busy — try again in a moment", Snackbar.LENGTH_SHORT).show()
                }
                true
            } else {
                false
            }
        }

        vm.preview.observe(viewLifecycleOwner) { state ->
            when (state) {
                is PreviewState.Loading -> {
                    binding.textScroll.visibility = View.GONE
                    // Set the mode before making the bar visible: the Material
                    // indicator throws if switched *to* indeterminate while shown.
                    if (state.total > 0) {
                        binding.progressPreview.isIndeterminate = false
                        binding.progressPreview.setProgressCompat(
                            (state.downloaded * 100 / state.total).toInt(), true,
                        )
                    } else {
                        binding.progressPreview.isIndeterminate = true
                    }
                    binding.progressPreview.visibility = View.VISIBLE
                }
                is PreviewState.Ready -> render(state)
                is PreviewState.Failed -> {
                    Snackbar.make(binding.root, state.message, Snackbar.LENGTH_LONG).show()
                    parentFragmentManager.popBackStack()
                }
                null -> {}
            }
        }

        // Feedback for the Save action. While this fragment is shown the file
        // list's view is destroyed, so it isn't also observing these.
        vm.completed.observe(viewLifecycleOwner) { done ->
            if (done == null) return@observe
            Snackbar.make(binding.root, "Saved to Downloads/${done.displayName}", Snackbar.LENGTH_LONG).show()
            vm.acknowledgeCompletion()
        }
        vm.errorEvent.observe(viewLifecycleOwner) { msg ->
            if (msg == null) return@observe
            Snackbar.make(binding.root, msg, Snackbar.LENGTH_LONG).show()
            vm.acknowledgeError()
        }
    }

    private fun render(state: PreviewState.Ready) {
        binding.progressPreview.visibility = View.GONE
        when (state.type) {
            PreviewType.TEXT -> {
                binding.textScroll.visibility = View.VISIBLE
                viewLifecycleOwner.lifecycleScope.launch {
                    val text = withContext(Dispatchers.IO) { readText(state.file) }
                    binding.tvContent.text = text
                }
            }
        }
    }

    private fun readText(file: File): String =
        try {
            file.readText(Charsets.UTF_8)
        } catch (e: Exception) {
            "Could not read file: ${e.message}"
        }

    override fun onDestroyView() {
        super.onDestroyView()
        (activity as? AppCompatActivity)?.supportActionBar?.show()
        vm.clearPreview()
        _binding = null
    }

    companion object {
        private const val ARG_FOLDER = "folder"
        private const val ARG_PATH = "path"
        private const val ARG_NAME = "name"

        fun newInstance(folderID: String, path: String, name: String) = PreviewFragment().apply {
            arguments = Bundle().apply {
                putString(ARG_FOLDER, folderID)
                putString(ARG_PATH, path)
                putString(ARG_NAME, name)
            }
        }
    }
}
