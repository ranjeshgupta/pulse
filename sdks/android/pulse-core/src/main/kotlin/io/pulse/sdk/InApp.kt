package io.pulse.sdk

import java.io.IOException
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** A button of an in-app message. */
data class PulseInAppButton(
    val text: String,
    /** `url` (open [value] through `PulseConfig.onInAppAction`) or `dismiss`. */
    val action: String,
    val value: String?,
) {
    /** Whether tapping the button opens a link. */
    val isUrl: Boolean get() = action == "url" && value != null

    companion object {
        fun fromJson(j: Map<String, Any?>) = PulseInAppButton(
            text = j.str("text") ?: "",
            action = j.str("action") ?: "dismiss",
            value = j.str("value"),
        )
    }
}

/** What an in-app message shows. Colours are `#rrggbb`. */
data class PulseInAppContent(
    val title: String = "",
    val body: String = "",
    val imageUrl: String? = null,
    val buttons: List<PulseInAppButton> = emptyList(),
    val background: String? = null,
    val textColor: String? = null,
    val buttonColor: String? = null,
    /** Banner position: `top` or `bottom` (default). */
    val position: String? = null,
) {
    companion object {
        fun fromJson(j: Map<String, Any?>) = PulseInAppContent(
            title = j.str("title") ?: "",
            body = j.str("body") ?: "",
            imageUrl = j.str("image_url"),
            buttons = j["buttons"].asList().map { PulseInAppButton.fromJson(it.asMap()) },
            background = j.str("background"),
            textColor = j.str("text_color"),
            buttonColor = j.str("button_color"),
            position = j.str("position"),
        )
    }
}

/** When an in-app message is shown. */
data class PulseInAppTrigger(
    /** The event that shows the message (e.g. `app_open`, `screen_view`, `add_to_cart`). */
    val event: String,
    /** With `screen_view`: only this screen. */
    val screen: String? = null,
    val delaySeconds: Int = 0,
    /** `once` (ever, on this device), `session` (once per app run), `daily` or `always`. */
    val frequency: String = "once",
    val platforms: List<String> = emptyList(),
) {
    /** Whether event [name] (with [screen] for `screen_view`) triggers this. */
    fun matches(name: String, screen: String?): Boolean =
        event == name && (this.screen == null || this.screen == screen)

    companion object {
        fun fromJson(j: Map<String, Any?>) = PulseInAppTrigger(
            event = j.str("trigger_event") ?: "",
            screen = j.str("screen"),
            delaySeconds = j["delay_seconds"].asLong()?.toInt() ?: 0,
            frequency = j.str("frequency") ?: "once",
            platforms = j["platforms"].asList().map { it.toString() },
        )
    }
}

/** An in-app campaign the server returned for the current person. */
data class PulseInAppMessage(
    /** Campaign id (`inapp_id` in the recorded events). */
    val id: Long,
    /** `modal`, `banner` or `fullscreen`. */
    val layout: String,
    val content: PulseInAppContent,
    val trigger: PulseInAppTrigger,
    val endAt: Date? = null,
) {
    companion object {
        fun fromJson(j: Map<String, Any?>) = PulseInAppMessage(
            id = j["id"].asLong() ?: throw IllegalArgumentException("in-app campaign without id"),
            layout = j.str("layout") ?: "modal",
            content = PulseInAppContent.fromJson(j["content"].asMap()),
            trigger = PulseInAppTrigger.fromJson(j["trigger"].asMap()),
            endAt = Json.parseTime(j.str("end_at")),
        )
    }
}

/** How a shown message was closed: by a [button] (null for the close button, back, or a tap outside), or [expired] (a banner timed out). */
data class InAppOutcome(val button: PulseInAppButton? = null, val expired: Boolean = false)

/** Shows in-app messages. The Android module has one that starts an Activity. */
interface InAppPresenter {
    /** Whether a message can be shown now (e.g. an activity is in the foreground). */
    fun canPresent(): Boolean

    /** Shows [message] and calls [done] once when it is closed. Called on the SDK's worker thread. */
    fun present(message: PulseInAppMessage, done: (InAppOutcome) -> Unit)
}

/**
 * In-app campaigns for the current person.
 *
 * The SDK fetches the campaigns at start, after identify, reset and opt-in, and when the app comes to
 * the foreground (at most every 15 minutes). When the app calls track(name) or screen(name), the first
 * campaign whose trigger matches and whose frequency allows is shown after its delay. Showing,
 * tapping a url button and dismissing are recorded as `inapp_shown`, `inapp_clicked` and
 * `inapp_dismissed`.
 */
class PulseInApp internal constructor(private val pulse: PulseClient) {
    private val lock = Any()
    private var campaigns: List<PulseInAppMessage> = emptyList()
    private var fetchedAt: Long? = null
    private var fetching = false
    private var generation = 0
    private val waiting = ArrayList<Pair<String, String?>>() // triggers seen before the first fetch finished

    private val sessionShown = HashSet<Long>()
    private val pending = HashSet<Long>()
    private val timers = HashSet<Cancellable>()
    private var once = HashSet<Long>()
    private var daily = HashMap<Long, String>()
    private var showing = false

    /** The campaigns fetched last, in priority order. */
    val messages: List<PulseInAppMessage> get() = synchronized(lock) { campaigns }

    /** Whether an in-app message is on screen. */
    val isShowing: Boolean get() = synchronized(lock) { showing }

    internal fun load() {
        val j = Json.decodeObject(pulse.env.storage.read(pulse.keys.inApp)) ?: return
        once = j["once"].asList().mapNotNull { it.asLong() }.toHashSet()
        daily = HashMap(j["daily"].asMap().entries.mapNotNull { (k, v) ->
            val id = k.toLongOrNull() ?: return@mapNotNull null
            id to (v?.toString() ?: return@mapNotNull null)
        }.toMap())
    }

    private fun saveLocked() {
        pulse.env.storage.write(
            pulse.keys.inApp,
            Json.encode(mapOf("once" to once.toList(), "daily" to daily.mapKeys { it.key.toString() })),
        )
    }

    /** Fetches the current person's campaigns now (on the worker). On failure the previous ones are kept. */
    fun refresh() {
        val gen = synchronized(lock) {
            fetching = true
            ++generation
        }
        pulse.env.dispatcher.execute { fetch(gen) }
    }

    internal fun refreshIfStale() {
        val stale = synchronized(lock) {
            val at = fetchedAt
            !fetching && (at == null || pulse.env.clock() - at >= REFRESH_AFTER_MILLIS)
        }
        if (stale) refresh()
    }

    private fun fetch(gen: Int) {
        var result: List<PulseInAppMessage>? = null
        try {
            if (!pulse.isOptedOut && !pulse.disposed) result = request()
        } catch (e: Exception) {
            pulse.log("fetching in-app campaigns failed: $e", always = true)
        }
        val triggers: List<Pair<String, String?>>
        synchronized(lock) {
            if (gen != generation) return // a newer fetch (e.g. after identify) wins
            fetching = false
            if (result != null) {
                campaigns = result
                fetchedAt = pulse.env.clock()
            }
            triggers = ArrayList(waiting)
            waiting.clear()
        }
        for ((name, screen) in triggers) match(name, screen)
    }

    private fun request(): List<PulseInAppMessage>? {
        val body = LinkedHashMap<String, Any?>()
        body["id"] = pulse.currentId
        pulse.identifiedHash?.let { body["user_hash"] = it }
        body["platform"] = pulse.env.platform.wireName
        val res = try {
            pulse.post("/v1/web/inapp", body)
        } catch (e: IOException) {
            pulse.log("fetching in-app campaigns failed: $e", always = true)
            return null
        }
        if (res.status !in 200..299) {
            pulse.log("in-app campaigns: server answered ${res.status}: ${res.body}", always = true)
            return null
        }
        val j = Json.decodeObject(res.body) ?: return null
        val out = j["messages"].asList().mapNotNull {
            try {
                PulseInAppMessage.fromJson(it.asMap())
            } catch (e: IllegalArgumentException) {
                pulse.log("skipping an unreadable in-app campaign: $e", always = true)
                null
            }
        }
        pulse.log("${out.size} in-app campaigns for ${pulse.currentId}")
        return out
    }

    private fun today(): String =
        SimpleDateFormat("yyyy-MM-dd", Locale.US).format(Date(pulse.env.clock()))

    private fun allowsLocked(m: PulseInAppMessage): Boolean {
        if (m.id in pending) return false
        val end = m.endAt
        if (end != null && pulse.env.clock() > end.time) return false
        return when (m.trigger.frequency) {
            "always" -> true
            "session" -> m.id !in sessionShown
            "daily" -> daily[m.id] != today()
            else -> m.id !in once && m.id !in sessionShown // once, and anything unknown
        }
    }

    /** Whether campaign [m] may be shown now under its frequency rule. */
    fun allows(m: PulseInAppMessage): Boolean = synchronized(lock) { allowsLocked(m) }

    // Called for every tracked event and screen.
    internal fun onEvent(name: String, screen: String?) {
        if (pulse.disposed) return
        synchronized(lock) {
            if (fetching && fetchedAt == null) {
                // The first fetch is still running (e.g. app_open right after start): decide once it's done.
                waiting.add(name to screen)
                return
            }
        }
        match(name, screen)
    }

    private fun match(name: String, screen: String?) {
        synchronized(lock) {
            if (pulse.disposed || showing) return
            val m = campaigns.firstOrNull { it.trigger.matches(name, screen) && allowsLocked(it) } ?: return
            pending.add(m.id)
            var handle: Cancellable? = null
            handle = pulse.env.dispatcher.schedule(maxOf(0, m.trigger.delaySeconds) * 1000L) {
                synchronized(lock) {
                    handle?.let { timers.remove(it) }
                    pending.remove(m.id)
                }
                show(m)
            }
            timers.add(handle)
        }
    }

    private fun show(m: PulseInAppMessage) {
        val presenter = pulse.env.inAppPresenter
        if (pulse.disposed || pulse.isOptedOut) return
        if (presenter == null || !presenter.canPresent()) {
            pulse.log("in-app ${m.id} not shown: nothing to show it on", always = true)
            return
        }
        synchronized(lock) {
            if (showing) return
            showing = true
            sessionShown.add(m.id)
            when (m.trigger.frequency) {
                "daily" -> { daily[m.id] = today(); saveLocked() }
                "session", "always" -> {}
                else -> { once.add(m.id); saveLocked() }
            }
        }
        pulse.enqueue("inapp_shown", mapOf("inapp_id" to m.id))
        try {
            presenter.present(m) { outcome -> closed(m, outcome) }
        } catch (e: Exception) {
            pulse.log("could not show in-app ${m.id}: $e", always = true)
            synchronized(lock) { showing = false }
        }
    }

    private fun closed(m: PulseInAppMessage, outcome: InAppOutcome) {
        synchronized(lock) { showing = false }
        val b = outcome.button
        if (b != null && b.isUrl) {
            pulse.enqueue("inapp_clicked", mapOf("inapp_id" to m.id, "button" to b.text, "url" to b.value))
            val action = pulse.config.onInAppAction
            if (action != null) {
                pulse.env.callbackExecutor.execute {
                    try {
                        action(b.value!!)
                    } catch (e: Exception) {
                        pulse.log("onInAppAction threw: $e", always = true)
                    }
                }
            }
        } else if (!outcome.expired) {
            pulse.enqueue("inapp_dismissed", mapOf("inapp_id" to m.id))
        }
    }

    internal fun cancelPending() {
        synchronized(lock) {
            timers.forEach { it.cancel() }
            timers.clear()
            pending.clear()
            waiting.clear()
        }
    }

    companion object {
        /** How often campaigns are fetched again when the app comes back to the foreground: 15 minutes. */
        const val REFRESH_AFTER_MILLIS: Long = 15 * 60 * 1000
    }
}
