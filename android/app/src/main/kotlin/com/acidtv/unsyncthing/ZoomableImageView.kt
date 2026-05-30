package com.acidtv.unsyncthing

import android.content.Context
import android.graphics.Matrix
import android.graphics.RectF
import android.util.AttributeSet
import android.view.GestureDetector
import android.view.MotionEvent
import android.view.ScaleGestureDetector
import androidx.appcompat.widget.AppCompatImageView
import kotlin.math.min

// Pinch-zoom / pan / double-tap image view built on platform APIs only
// (Matrix + ScaleGestureDetector + GestureDetector). No third-party deps.
// The whole transform lives in a single Matrix applied via ScaleType.MATRIX.
// "scale" below is relative to the fit-center baseline: 1f == fits the view.
class ZoomableImageView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
    defStyle: Int = 0,
) : AppCompatImageView(context, attrs, defStyle) {

    private val matrix_ = Matrix()
    private val values = FloatArray(9)

    private var baseScale = 1f       // fit-center scale of the bitmap into the view
    private val minScale = 1f        // can't zoom out smaller than fit
    private val maxScale = 5f        // generous zoom-in (multiples of fit)
    private val doubleTapScale = 2.5f
    private var fitted = false       // base matrix computed for current drawable+size

    private val scaleDetector = ScaleGestureDetector(
        context,
        object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
            override fun onScale(d: ScaleGestureDetector): Boolean {
                val cur = currentScale()
                var factor = d.scaleFactor
                // Clamp so cur*factor stays within [minScale, maxScale].
                if (cur * factor < minScale) factor = minScale / cur
                if (cur * factor > maxScale) factor = maxScale / cur
                matrix_.postScale(factor, factor, d.focusX, d.focusY)
                fixTranslation()
                imageMatrix = matrix_
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
                matrix_.postTranslate(-dx, -dy)
                fixTranslation()
                imageMatrix = matrix_
                return true
            }

            override fun onDoubleTap(e: MotionEvent): Boolean {
                val target = if (currentScale() > minScale * 1.01f) minScale else doubleTapScale
                val factor = target / currentScale()
                matrix_.postScale(factor, factor, e.x, e.y)
                fixTranslation()
                imageMatrix = matrix_
                return true
            }
        },
    )

    init {
        scaleType = ScaleType.MATRIX
        isClickable = true
    }

    @Suppress("ClickableViewAccessibility")
    override fun onTouchEvent(event: MotionEvent): Boolean {
        scaleDetector.onTouchEvent(event)
        gestureDetector.onTouchEvent(event)
        return true
    }

    override fun onLayout(changed: Boolean, l: Int, t: Int, r: Int, b: Int) {
        super.onLayout(changed, l, t, r, b)
        if (!fitted) fitToView()
    }

    // Re-fit after setImageBitmap so a new image starts centred and fit.
    fun resetForNewImage() {
        fitted = false
        if (width > 0 && height > 0) fitToView()
    }

    private fun fitToView() {
        val d = drawable ?: return
        val vw = (width - paddingLeft - paddingRight).toFloat()
        val vh = (height - paddingTop - paddingBottom).toFloat()
        val dw = d.intrinsicWidth.toFloat()
        val dh = d.intrinsicHeight.toFloat()
        if (vw <= 0 || vh <= 0 || dw <= 0 || dh <= 0) return
        baseScale = min(vw / dw, vh / dh)
        matrix_.reset()
        matrix_.postScale(baseScale, baseScale)
        matrix_.postTranslate((vw - dw * baseScale) / 2f, (vh - dh * baseScale) / 2f)
        imageMatrix = matrix_
        fitted = true
    }

    // Current zoom relative to baseScale (1f == fit).
    private fun currentScale(): Float {
        matrix_.getValues(values)
        return if (baseScale == 0f) 1f else values[Matrix.MSCALE_X] / baseScale
    }

    // Keep the image inside the view: centre axes smaller than the view, and
    // clamp axes larger than the view so no blank gap appears at the edges.
    private fun fixTranslation() {
        val d = drawable ?: return
        val rect = RectF(0f, 0f, d.intrinsicWidth.toFloat(), d.intrinsicHeight.toFloat())
        matrix_.mapRect(rect)
        val vw = width.toFloat()
        val vh = height.toFloat()
        val dx = when {
            rect.width() <= vw -> (vw - rect.width()) / 2f - rect.left
            rect.left > 0 -> -rect.left
            rect.right < vw -> vw - rect.right
            else -> 0f
        }
        val dy = when {
            rect.height() <= vh -> (vh - rect.height()) / 2f - rect.top
            rect.top > 0 -> -rect.top
            rect.bottom < vh -> vh - rect.bottom
            else -> 0f
        }
        matrix_.postTranslate(dx, dy)
    }
}
