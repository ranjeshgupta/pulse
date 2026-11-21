package io.pulse.sdk.android

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Typeface
import android.util.AttributeSet
import android.util.TypedValue
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import io.pulse.sdk.Cancellable
import io.pulse.sdk.SlotContent
import java.net.HttpURLConnection
import java.net.URL

/**
 * Shows the content of one app content slot ([slotId]).
 *
 * When it is attached it shows the cached content and fetches the slot (unless it was fetched less
 * than 15 minutes ago), and it follows later changes (identify, reset, foreground refresh). A card
 * is drawn as image, title, body and button; a tap on the button (or on the card, when it has a deep
 * link) records `app_content_clicked` and opens the deep link through `PulseConfig.onInAppAction`.
 * The view is [View.GONE] while the slot is empty, before [Pulse.init], and when there is nothing it
 * can draw (HTML or data only, without a [setRenderer]). Each content shown records
 * `app_content_viewed` once per attach.
 *
 * In XML, set the slot id with `android:tag`:
 * `<io.pulse.sdk.android.PulseSlotView android:tag="home_banner" ... />`.
 */
class PulseSlotView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
    defStyleAttr: Int = 0,
) : FrameLayout(context, attrs, defStyleAttr) {
    private var renderer: ((SlotContent) -> View?)? = null
    private var listener: Cancellable? = null
    private var viewed: SlotContent? = null // content already recorded as viewed during this attach
    private var drawn = false

    /** The content shown now, or null when the view is empty. */
    var content: SlotContent? = null
        private set

    /** The slot this view shows (lowercase letters, digits, `-` and `_`). Defaults to `android:tag`. */
    var slotId: String? = null
        set(value) {
            if (field == value) return
            field = value
            viewed = null
            show(value?.let { if (Pulse.isInitialized) Pulse.content.cached(it) else null }, force = true)
            if (isAttachedToWindow) load()
        }

    init {
        visibility = View.GONE
        (tag as? String)?.takeIf { it.isNotEmpty() }?.let { slotId = it }
    }

    /**
     * Draws the content yourself: [renderer] returns the view to show for a slot's content (it gets
     * html, card and data), or null to hide the slot. Clicks inside it are yours: call [click] to
     * record `app_content_clicked` and open the card's deep link. Pass null to go back to the card.
     */
    fun setRenderer(renderer: ((SlotContent) -> View?)?) {
        this.renderer = renderer
        show(content, force = true)
    }

    /** Records `app_content_clicked` for the content shown and opens its deep link (if any). */
    fun click() {
        val id = slotId ?: return
        val c = content ?: return
        if (Pulse.isInitialized) Pulse.content.trackClicked(id, c)
    }

    override fun onAttachedToWindow() {
        super.onAttachedToWindow()
        viewed = null
        if (!Pulse.isInitialized) return
        listener?.cancel()
        listener = Pulse.content.addListener { all -> show(slotId?.let { all[it] }) }
        show(slotId?.let { Pulse.content.cached(it) })
        load()
    }

    override fun onDetachedFromWindow() {
        listener?.cancel()
        listener = null
        viewed = null
        super.onDetachedFromWindow()
    }

    private fun load() {
        val id = slotId ?: return
        if (!Pulse.isInitialized) return
        Pulse.content.load(listOf(id)) { r -> if (id == slotId && isAttachedToWindow) show(r[id]) }
    }

    private fun show(c: SlotContent?, force: Boolean = false) {
        if (!force && drawn && c == content) return
        content = c
        drawn = true
        removeAllViews()
        val child = when {
            c == null || c.isEmpty -> null
            renderer != null -> try {
                renderer?.invoke(c)
            } catch (e: Exception) {
                null
            }
            c.card?.isDrawable == true -> card(c)
            else -> null
        }
        if (child == null) {
            visibility = View.GONE
            viewed = null
            return
        }
        addView(child, LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        visibility = View.VISIBLE
        val id = slotId
        if (id != null && isAttachedToWindow && viewed != c && Pulse.isInitialized) {
            viewed = c
            Pulse.content.trackViewed(id, c!!)
        }
    }

    private fun dp(v: Int): Int =
        TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

    private fun card(c: SlotContent): View {
        val card = c.card!!
        val box = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(12), dp(16), dp(12))
        }
        card.imageUrl?.let { url ->
            val image = ImageView(context).apply {
                adjustViewBounds = true
                scaleType = ImageView.ScaleType.FIT_CENTER
                visibility = View.GONE
            }
            box.addView(image, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                bottomMargin = dp(8)
            })
            loadImage(url) { bmp ->
                if (content?.card?.imageUrl == url) {
                    image.setImageBitmap(bmp)
                    image.visibility = View.VISIBLE
                }
            }
        }
        card.title?.let {
            box.addView(TextView(context).apply {
                text = it
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 18f)
                setTypeface(typeface, Typeface.BOLD)
            })
        }
        card.body?.let {
            box.addView(TextView(context).apply {
                text = it
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 14f)
                setPadding(0, dp(4), 0, dp(4))
            })
        }
        card.buttonText?.let {
            box.addView(Button(context).apply {
                text = it
                isAllCaps = false
                setOnClickListener { click() }
            }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(8)
            })
        }
        if (card.deepLink != null) box.setOnClickListener { click() }
        return box
    }

    // Same approach as PulseInAppActivity: a plain thread and HttpURLConnection, no image library.
    private fun loadImage(url: String, onLoaded: (Bitmap) -> Unit) {
        if (!url.startsWith("https://") && !url.startsWith("http://")) return
        Thread {
            try {
                val conn = URL(url).openConnection() as HttpURLConnection
                conn.connectTimeout = 10_000
                conn.readTimeout = 10_000
                val bmp = conn.inputStream.use { BitmapFactory.decodeStream(it) }
                conn.disconnect()
                if (bmp != null) post { onLoaded(bmp) }
            } catch (_: Exception) {
                // No image: the card shows without it.
            }
        }.start()
    }
}
