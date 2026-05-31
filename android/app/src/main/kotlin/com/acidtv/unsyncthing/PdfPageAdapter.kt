package com.acidtv.unsyncthing

import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.pdf.PdfRenderer
import android.view.LayoutInflater
import android.view.ViewGroup
import androidx.recyclerview.widget.RecyclerView
import com.acidtv.unsyncthing.databinding.ItemPdfPageBinding
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

// Renders each page of a PdfRenderer to a bitmap on demand, one ImageView per
// page. RecyclerView recycles off-screen pages so memory stays bounded even for
// large documents (there's no preview size cap for PDFs).
//
// PdfRenderer is not thread-safe and only allows a single page open at a time,
// so every render runs under [lock]; the actual decode happens off the main
// thread. The owning fragment holds the PdfRenderer lifecycle and closes it.
class PdfPageAdapter(
    private val renderer: PdfRenderer,
    private val scope: CoroutineScope,
    private val targetWidthProvider: () -> Int,
) : RecyclerView.Adapter<PdfPageAdapter.VH>() {

    // Serialises openPage/render/close — PdfRenderer permits only one open page.
    private val lock = Mutex()

    inner class VH(val binding: ItemPdfPageBinding) : RecyclerView.ViewHolder(binding.root)

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int) = VH(
        ItemPdfPageBinding.inflate(LayoutInflater.from(parent.context), parent, false)
    )

    override fun getItemCount() = renderer.pageCount

    override fun onBindViewHolder(holder: VH, position: Int) {
        // Clear any recycled bitmap so the wrong page never flashes while the
        // new one renders.
        holder.binding.pageImage.setImageDrawable(null)
        val width = targetWidthProvider().coerceAtLeast(1)
        scope.launch {
            val bmp = withContext(Dispatchers.IO) { renderPage(position, width) }
            // Drop the result if this holder was rebound to another page.
            if (bmp != null && holder.bindingAdapterPosition == position) {
                holder.binding.pageImage.setImageBitmap(bmp)
            }
        }
    }

    // Render at a multiple of the view width so pinch-zoom magnifies a high-res
    // bitmap rather than upscaling a screen-width one. adjustViewBounds keeps the
    // ImageView laid out at view width regardless.
    private suspend fun renderPage(index: Int, viewWidth: Int): Bitmap? = lock.withLock {
        try {
            renderer.openPage(index).use { page ->
                val renderWidth = viewWidth * OVERSAMPLE
                val scale = renderWidth.toFloat() / page.width
                val height = (page.height * scale).toInt().coerceAtLeast(1)
                val bmp = Bitmap.createBitmap(renderWidth, height, Bitmap.Config.ARGB_8888)
                // PDF pages can be transparent; paint white so text is legible.
                bmp.eraseColor(Color.WHITE)
                page.render(bmp, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                bmp
            }
        } catch (e: Exception) {
            null
        }
    }

    companion object {
        private const val OVERSAMPLE = 2
    }
}
