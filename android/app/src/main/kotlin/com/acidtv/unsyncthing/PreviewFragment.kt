package com.acidtv.unsyncthing

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Matrix
import android.graphics.pdf.PdfRenderer
import android.media.ExifInterface
import android.os.Bundle
import android.os.ParcelFileDescriptor
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.appcompat.app.AppCompatActivity
import androidx.fragment.app.Fragment
import androidx.fragment.app.activityViewModels
import androidx.lifecycle.lifecycleScope
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.acidtv.unsyncthing.databinding.FragmentPreviewBinding
import com.google.android.material.snackbar.Snackbar
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException
import kotlin.math.max

// Full-screen preview of a single file. The file has already been fetched into
// the preview cache (progress/cancel are shown on the file list's bottom
// footer before this screen opens), so this fragment just renders it by kind
// (text for now). Saving to Downloads reuses the cached copy — no re-fetch.
class PreviewFragment : Fragment() {

    private val vm: SyncthingViewModel by activityViewModels()
    private var _binding: FragmentPreviewBinding? = null
    private val binding get() = _binding!!

    // PDF rendering owns native resources released in onDestroyView.
    private var pdfRenderer: PdfRenderer? = null
    private var pdfFd: ParcelFileDescriptor? = null

    private val name get() = requireArguments().getString(ARG_NAME, "")
    private val cachedPath get() = requireArguments().getString(ARG_FILE, "")
    private val type get() = PreviewType.valueOf(requireArguments().getString(ARG_TYPE, PreviewType.TEXT.name))

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
                vm.savePreviewToDownloads(cachedPath, name)
                true
            } else {
                false
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

        render()
    }

    private fun render() {
        when (type) {
            PreviewType.TEXT -> {
                binding.textScroll.visibility = View.VISIBLE
                viewLifecycleOwner.lifecycleScope.launch {
                    val text = withContext(Dispatchers.IO) { readText(File(cachedPath)) }
                    binding.tvContent.text = text
                }
            }
            PreviewType.IMAGE -> {
                binding.imageView.visibility = View.VISIBLE
                viewLifecycleOwner.lifecycleScope.launch {
                    val bmp = withContext(Dispatchers.IO) { decodeBounded(cachedPath) }
                    if (bmp != null) {
                        binding.imageView.setImageBitmap(bmp)
                        binding.imageView.resetForNewImage()
                    } else {
                        Snackbar.make(binding.root, "Could not load image", Snackbar.LENGTH_LONG).show()
                    }
                }
            }
            PreviewType.PDF -> renderPdf()
        }
    }

    // Open the cached PDF and feed its pages to a recycling adapter. Each page is
    // rendered to a bitmap on demand off the main thread (see PdfPageAdapter). No
    // size cap applies — the RecyclerView only holds the visible pages in memory.
    private fun renderPdf() {
        binding.pdfContainer.visibility = View.VISIBLE
        binding.pdfView.layoutManager = LinearLayoutManager(requireContext())
        try {
            val fd = ParcelFileDescriptor.open(File(cachedPath), ParcelFileDescriptor.MODE_READ_ONLY)
            val renderer = PdfRenderer(fd)
            pdfFd = fd
            pdfRenderer = renderer
            binding.pdfView.adapter = PdfPageAdapter(
                renderer = renderer,
                scope = viewLifecycleOwner.lifecycleScope,
                targetWidthProvider = {
                    binding.pdfView.width - binding.pdfView.paddingStart - binding.pdfView.paddingEnd
                },
            )
            // Live "page / total" readout that tracks scrolling. Posted once the
            // first layout pass has placed the pages.
            binding.pdfView.addOnScrollListener(object : RecyclerView.OnScrollListener() {
                override fun onScrolled(rv: RecyclerView, dx: Int, dy: Int) = updatePageIndicator()
            })
            binding.pdfView.post { updatePageIndicator() }
        } catch (e: IOException) {
            Snackbar.make(binding.root, "Could not open PDF", Snackbar.LENGTH_LONG).show()
        }
    }

    // Show the page nearest the vertical centre of the viewport, 1-based, over
    // the total page count. Based on the list's own (unzoomed) scroll position.
    private fun updatePageIndicator() {
        val binding = _binding ?: return
        val total = pdfRenderer?.pageCount ?: return
        val rv = binding.pdfView
        val centerY = rv.height / 2
        var page = (rv.layoutManager as? LinearLayoutManager)?.findFirstVisibleItemPosition() ?: 0
        for (i in 0 until rv.childCount) {
            val child = rv.getChildAt(i)
            if (child.top <= centerY && child.bottom >= centerY) {
                val pos = rv.getChildAdapterPosition(child)
                if (pos != RecyclerView.NO_POSITION) page = pos
                break
            }
        }
        binding.pageIndicator.text = "${page.coerceAtLeast(0) + 1} / $total"
    }

    private fun readText(file: File): String =
        try {
            file.readText(Charsets.UTF_8)
        } catch (e: Exception) {
            "Could not read file: ${e.message}"
        }

    // Decode without a byte cap (images skip the text previewer's 5 MB limit).
    // Pass 1 reads bounds only; pass 2 downsamples by a power-of-two so the
    // longest edge is <= maxDim, keeping the in-memory bitmap bounded no matter
    // how large the file is. ZoomableImageView magnifies via its matrix on zoom.
    private fun decodeBounded(path: String, maxDim: Int = 4096): Bitmap? {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(path, bounds)
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null

        var sample = 1
        val longest = max(bounds.outWidth, bounds.outHeight)
        while (longest / sample > maxDim) sample *= 2

        val opts = BitmapFactory.Options().apply { inSampleSize = sample }
        val bmp = BitmapFactory.decodeFile(path, opts) ?: return null
        return applyExifRotation(path, bmp)
    }

    // JPEGs may carry an EXIF orientation; rotate to display upright. A no-op
    // (try/catch) for formats without EXIF.
    private fun applyExifRotation(path: String, bmp: Bitmap): Bitmap {
        val degrees = try {
            when (ExifInterface(path).getAttributeInt(
                ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL,
            )) {
                ExifInterface.ORIENTATION_ROTATE_90 -> 90f
                ExifInterface.ORIENTATION_ROTATE_180 -> 180f
                ExifInterface.ORIENTATION_ROTATE_270 -> 270f
                else -> 0f
            }
        } catch (e: Exception) {
            0f
        }
        if (degrees == 0f) return bmp
        val m = Matrix().apply { postRotate(degrees) }
        val rotated = Bitmap.createBitmap(bmp, 0, 0, bmp.width, bmp.height, m, true)
        if (rotated != bmp) bmp.recycle()
        return rotated
    }

    override fun onDestroyView() {
        super.onDestroyView()
        (activity as? AppCompatActivity)?.supportActionBar?.show()
        // Release native PDF resources: stop binds, then close renderer, then fd.
        // In-flight render coroutines die with the view scope; PdfPageAdapter's
        // lock + try/catch absorb any close race.
        binding.pdfView.adapter = null
        pdfRenderer?.close()
        pdfRenderer = null
        pdfFd?.close()
        pdfFd = null
        _binding = null
    }

    companion object {
        private const val ARG_NAME = "name"
        private const val ARG_FILE = "file"
        private const val ARG_TYPE = "type"

        fun newInstance(name: String, cachedPath: String, type: PreviewType) =
            PreviewFragment().apply {
                arguments = Bundle().apply {
                    putString(ARG_NAME, name)
                    putString(ARG_FILE, cachedPath)
                    putString(ARG_TYPE, type.name)
                }
            }
    }
}
