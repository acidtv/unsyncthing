package com.acidtv.unsyncthing

import android.content.Context
import android.graphics.Canvas
import android.util.AttributeSet
import android.view.GestureDetector
import android.view.MotionEvent
import android.view.ScaleGestureDetector
import androidx.recyclerview.widget.RecyclerView

// A RecyclerView with pinch-zoom, pan and double-tap, built on platform APIs
// only (ScaleGestureDetector + GestureDetector + a canvas transform) — the same
// toolkit as ZoomableImageView, no third-party deps. Pages still recycle and
// scroll vertically; at 1x the list behaves normally, and once zoomed in a
// single-finger drag pans within the magnified viewport.
class ZoomableRecyclerView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
    defStyle: Int = 0,
) : RecyclerView(context, attrs, defStyle) {

    private var scaleFactor = 1f
    private val minScale = 1f
    private val maxScale = 5f
    private val doubleTapScale = 2.5f

    // Canvas translation applied before scaling. Always <= 0 and bounded so the
    // magnified content can't be panned past its own edges.
    private var transX = 0f
    private var transY = 0f

    private var lastFocusX = 0f
    private var lastFocusY = 0f

    private val scaleDetector = ScaleGestureDetector(
        context,
        object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
            override fun onScaleBegin(d: ScaleGestureDetector): Boolean {
                lastFocusX = d.focusX
                lastFocusY = d.focusY
                return true
            }

            override fun onScale(d: ScaleGestureDetector): Boolean {
                val newScale = (scaleFactor * d.scaleFactor).coerceIn(minScale, maxScale)
                val factor = newScale / scaleFactor
                scaleFactor = newScale
                // Keep the content under the focal point fixed, and follow the
                // focal point as the fingers move (two-finger pan).
                transX = d.focusX - (d.focusX - transX) * factor + (d.focusX - lastFocusX)
                transY = d.focusY - (d.focusY - transY) * factor + (d.focusY - lastFocusY)
                lastFocusX = d.focusX
                lastFocusY = d.focusY
                clampTranslation()
                invalidate()
                return true
            }
        },
    )

    private val gestureDetector = GestureDetector(
        context,
        object : GestureDetector.SimpleOnGestureListener() {
            override fun onScroll(
                e1: MotionEvent?,
                e2: MotionEvent,
                dx: Float,
                dy: Float,
            ): Boolean {
                // Only pan while zoomed; at 1x defer to normal list scrolling.
                if (scaleFactor <= minScale) return false
                transX -= dx
                transY -= dy
                clampTranslation()
                invalidate()
                return true
            }

            override fun onDoubleTap(e: MotionEvent): Boolean {
                if (scaleFactor > minScale * 1.01f) {
                    scaleFactor = minScale
                    transX = 0f
                    transY = 0f
                } else {
                    val factor = doubleTapScale / scaleFactor
                    scaleFactor = doubleTapScale
                    transX = e.x - (e.x - transX) * factor
                    transY = e.y - (e.y - transY) * factor
                    clampTranslation()
                }
                invalidate()
                return true
            }
        },
    )

    override fun dispatchDraw(canvas: Canvas) {
        canvas.save()
        canvas.translate(transX, transY)
        canvas.scale(scaleFactor, scaleFactor)
        super.dispatchDraw(canvas)
        canvas.restore()
    }

    // Intercept once a second finger lands (start of a pinch) or while zoomed in,
    // so our onTouchEvent drives zoom/pan instead of the list scrolling.
    override fun onInterceptTouchEvent(e: MotionEvent): Boolean {
        if (scaleFactor > minScale || e.pointerCount >= 2) return true
        return super.onInterceptTouchEvent(e)
    }

    @Suppress("ClickableViewAccessibility")
    override fun onTouchEvent(e: MotionEvent): Boolean {
        scaleDetector.onTouchEvent(e)
        gestureDetector.onTouchEvent(e)
        if (scaleDetector.isInProgress || scaleFactor > minScale) return true
        return super.onTouchEvent(e)
    }

    // The drawn content is the viewport scaled by scaleFactor, so the only valid
    // translations keep its edges from entering the viewport: [size*(1-s), 0].
    private fun clampTranslation() {
        val minX = width * (1f - scaleFactor)
        val minY = height * (1f - scaleFactor)
        transX = transX.coerceIn(minX.coerceAtMost(0f), 0f)
        transY = transY.coerceIn(minY.coerceAtMost(0f), 0f)
    }
}
