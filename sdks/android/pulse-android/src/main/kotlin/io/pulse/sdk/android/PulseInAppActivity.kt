package io.pulse.sdk.android

import android.app.Activity
import android.app.Application
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.ColorDrawable
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import io.pulse.sdk.InAppOutcome
import io.pulse.sdk.InAppPresenter
import io.pulse.sdk.PulseInAppButton
import io.pulse.sdk.PulseInAppMessage
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger

/** Shows in-app messages with [PulseInAppActivity] over the app's top activity. */
internal class ActivityInAppPresenter(
    private val app: Application,
    private val lifecycle: Pulse.Lifecycle,
) : InAppPresenter {
    private val main = Handler(Looper.getMainLooper())

    override fun canPresent(): Boolean = lifecycle.inForeground && lifecycle.top?.get() != null

    override fun present(message: PulseInAppMessage, done: (InAppOutcome) -> Unit) {
        val key = PulseInAppActivity.register(message, done)
        main.post {
            val from = lifecycle.top?.get()
            val intent = Intent(app, PulseInAppActivity::class.java).putExtra(PulseInAppActivity.EXTRA_KEY, key)
            try {
                if (from != null && !from.isFinishing) {
                    from.startActivity(intent)
                    from.overridePendingTransition(android.R.anim.fade_in, 0)
                } else {
                    app.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                }
            } catch (e: Exception) {
                PulseInAppActivity.report(key, InAppOutcome())
            }
        }
    }
}

/**
 * Draws one in-app message: `modal` (a card over a dimmed background), `banner` (top or bottom,
 * hides itself after 8 s, a tap follows its first url button) or `fullscreen`. Colours come from the
 * campaign, with light defaults.
 */
class PulseInAppActivity : Activity() {
    private var key = 0
    private var message: PulseInAppMessage? = null
    private val main = Handler(Looper.getMainLooper())
    private val expire = Runnable { close(InAppOutcome(expired = true)) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        key = intent.getIntExtra(EXTRA_KEY, 0)
        val m = pending[key]?.first
        if (m == null) {
            finish()
            return
        }
        message = m
        window.setBackgroundDrawable(ColorDrawable(if (m.layout == "banner") Color.TRANSPARENT else 0x99000000.toInt()))
        setContentView(build(m))
        if (m.layout == "banner") main.postDelayed(expire, BANNER_MILLIS)
    }

    private fun dp(v: Int): Int =
        TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

    private fun color(hex: String?, fallback: Int): Int = try {
        if (hex.isNullOrEmpty()) fallback else Color.parseColor(hex)
    } catch (_: IllegalArgumentException) {
        fallback
    }

    private fun build(m: PulseInAppMessage): View {
        val c = m.content
        val bg = color(c.background, Color.WHITE)
        val fg = color(c.textColor, Color.parseColor("#152238"))
        val accent = color(c.buttonColor, Color.parseColor("#2747D6"))

        val card = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(16), dp(20), dp(16))
            background = GradientDrawable().apply {
                setColor(bg)
                cornerRadius = if (m.layout == "fullscreen") 0f else dp(12).toFloat()
            }
            isClickable = true // taps on the card don't reach the dimmed background
        }

        val close = TextView(this).apply {
            text = "×"
            setTextColor(fg)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 24f)
            gravity = Gravity.END
            contentDescription = "Close"
            setOnClickListener { close(InAppOutcome()) }
        }
        card.addView(close, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))

        c.imageUrl?.let { url ->
            val image = ImageView(this).apply {
                adjustViewBounds = true
                scaleType = ImageView.ScaleType.FIT_CENTER
                visibility = View.GONE
            }
            card.addView(image, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                bottomMargin = dp(12)
            })
            loadImage(url) { bmp ->
                image.setImageBitmap(bmp)
                image.visibility = View.VISIBLE
            }
        }
        if (c.title.isNotEmpty()) {
            card.addView(TextView(this).apply {
                text = c.title
                setTextColor(fg)
                setTextSize(TypedValue.COMPLEX_UNIT_SP, if (m.layout == "banner") 16f else 20f)
                setTypeface(typeface, Typeface.BOLD)
            })
        }
        if (c.body.isNotEmpty()) {
            card.addView(TextView(this).apply {
                text = c.body
                setTextColor(fg)
                setTextSize(TypedValue.COMPLEX_UNIT_SP, if (m.layout == "banner") 14f else 16f)
                setPadding(0, dp(6), 0, dp(6))
            })
        }
        if (m.layout != "banner" && c.buttons.isNotEmpty()) {
            val row = LinearLayout(this).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = Gravity.END
                setPadding(0, dp(12), 0, 0)
            }
            c.buttons.forEachIndexed { i, b -> row.addView(button(b, i == 0, accent, fg)) }
            card.addView(row)
        }

        val root = FrameLayout(this)
        when (m.layout) {
            "banner" -> {
                card.setOnClickListener {
                    val first = c.buttons.firstOrNull { it.isUrl }
                    close(InAppOutcome(first))
                }
                val top = c.position == "top"
                root.addView(card, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT,
                    if (top) Gravity.TOP else Gravity.BOTTOM).apply { setMargins(dp(12), dp(24), dp(12), dp(24)) })
                // Touches outside the banner go nowhere; the banner closes by itself.
            }
            "fullscreen" -> {
                root.setBackgroundColor(bg)
                val scroll = ScrollView(this).apply { isFillViewport = true }
                card.gravity = Gravity.CENTER_VERTICAL
                scroll.addView(card, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
                root.addView(scroll, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            }
            else -> { // modal
                root.setOnClickListener { close(InAppOutcome()) } // tap outside
                val scroll = ScrollView(this)
                scroll.addView(card)
                root.addView(scroll, FrameLayout.LayoutParams(
                    minOf(resources.displayMetrics.widthPixels - dp(48), dp(420)),
                    ViewGroup.LayoutParams.WRAP_CONTENT, Gravity.CENTER,
                ))
            }
        }
        return root
    }

    private fun button(b: PulseInAppButton, primary: Boolean, accent: Int, fg: Int): Button = Button(this).apply {
        text = b.text
        isAllCaps = false
        if (primary) {
            setTextColor(Color.WHITE)
            background = GradientDrawable().apply { setColor(accent); cornerRadius = dp(6).toFloat() }
        } else {
            setTextColor(fg)
            background = GradientDrawable().apply { setColor(Color.TRANSPARENT); setStroke(dp(1), fg); cornerRadius = dp(6).toFloat() }
        }
        layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
            marginStart = dp(8)
        }
        setOnClickListener { close(InAppOutcome(if (b.isUrl) b else null)) }
    }

    private fun loadImage(url: String, onLoaded: (Bitmap) -> Unit) {
        if (!url.startsWith("https://") && !url.startsWith("http://")) return
        Thread {
            try {
                val conn = URL(url).openConnection() as HttpURLConnection
                conn.connectTimeout = 10_000
                conn.readTimeout = 10_000
                val bmp = conn.inputStream.use { BitmapFactory.decodeStream(it) }
                conn.disconnect()
                if (bmp != null) runOnUiThread { if (!isFinishing) onLoaded(bmp) }
            } catch (_: Exception) {
                // No image: the message shows without it.
            }
        }.start()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        close(InAppOutcome())
    }

    private fun close(outcome: InAppOutcome) {
        main.removeCallbacks(expire)
        report(key, outcome)
        if (!isFinishing) {
            finish()
            overridePendingTransition(0, android.R.anim.fade_out)
        }
    }

    override fun onDestroy() {
        main.removeCallbacks(expire)
        // Closed some other way (e.g. the task was removed): count it as dismissed.
        if (isFinishing) report(key, InAppOutcome())
        super.onDestroy()
    }

    companion object {
        internal const val EXTRA_KEY = "pulse_inapp_key"
        private const val BANNER_MILLIS = 8_000L
        private val ids = AtomicInteger(0)
        private val pending = ConcurrentHashMap<Int, Pair<PulseInAppMessage, (InAppOutcome) -> Unit>>()

        internal fun register(m: PulseInAppMessage, done: (InAppOutcome) -> Unit): Int {
            val k = ids.incrementAndGet()
            pending[k] = m to done
            return k
        }

        // Reports the outcome once; later calls for the same key do nothing.
        internal fun report(key: Int, outcome: InAppOutcome) {
            pending.remove(key)?.second?.invoke(outcome)
        }
    }
}
