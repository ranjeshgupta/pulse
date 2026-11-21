package io.pulse.sdk

import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList

/** The card of an app content slot: what `PulseSlotView` draws. Every field is optional. */
data class SlotCard(
    val title: String? = null,
    val body: String? = null,
    val imageUrl: String? = null,
    val buttonText: String? = null,
    /** Link or deep link to open when the card or its button is tapped. */
    val deepLink: String? = null,
    /** Custom key-value data set in Pulse. */
    val data: Map<String, String> = emptyMap(),
) {
    /** Whether there is anything to draw (title, body, image or button). */
    val isDrawable: Boolean get() = title != null || body != null || imageUrl != null || buttonText != null

    companion object {
        fun fromJson(j: Map<String, Any?>) = SlotCard(
            title = j.str("title"),
            body = j.str("body"),
            imageUrl = j.str("image_url"),
            buttonText = j.str("button_text"),
            deepLink = j.str("deep_link"),
            data = j["data"].asMap().entries.associate { (k, v) -> k to (v?.toString() ?: "") },
        )
    }
}

/** What the server chose for one app content slot for the current person. */
data class SlotContent(
    val card: SlotCard? = null,
    /** HTML set in Pulse; the SDK does not render it (use `PulseSlotView.setRenderer`). */
    val html: String? = null,
    /** The variant shown (recorded in `app_content_viewed` / `app_content_clicked`). */
    val variant: String? = null,
) {
    /** Whether there is nothing to show. */
    val isEmpty: Boolean get() = card == null && html == null

    companion object {
        fun fromJson(j: Map<String, Any?>) = SlotContent(
            card = j["card"].takeIf { it is Map<*, *> }?.let { SlotCard.fromJson(it.asMap()) },
            html = j.str("html"),
            variant = j.str("variant"),
        )
    }
}

/**
 * App content slots: dynamic content for named places in the app (the app twin of a website's
 * dynamic web content). Slots are created in Pulse under Website & app messages → Content slots
 * with "Where: App".
 *
 * [fetch] asks the server what the current person sees in the given slots and caches it. The cache
 * is cleared (and the slots fetched so far are fetched again) after identify, reset and opt-in, and
 * refreshed when the app comes to the foreground and the content is 15+ minutes old. On a network
 * error or a non-2xx answer the cached content is kept and returned; nothing throws. While opted out
 * nothing is requested.
 *
 * Callbacks and listeners run on the environment's callback executor (the main thread on Android).
 */
class PulseContent internal constructor(private val pulse: PulseClient) {
    private val lock = Any()
    private val cache = HashMap<String, SlotContent>()
    private val fetched = HashMap<String, Long>() // slot -> when it was last answered for this person
    private val known = LinkedHashSet<String>() // every slot asked for, fetched again after identify/reset
    private var generation = 0
    private val listeners = CopyOnWriteArrayList<(Map<String, SlotContent>) -> Unit>()

    /** The cached content of [slot], or null when the slot is empty or was not fetched yet. */
    fun cached(slot: String): SlotContent? = synchronized(lock) { cache[slot] }

    /** Everything cached, by slot id. */
    val all: Map<String, SlotContent> get() = synchronized(lock) { HashMap(cache) }

    /**
     * Calls [listener] with everything cached whenever the cache changes (a fetch answered, or it was
     * cleared after identify/reset). Returns a handle that removes it.
     */
    fun addListener(listener: (Map<String, SlotContent>) -> Unit): Cancellable {
        listeners.add(listener)
        return Cancellable { listeners.remove(listener) }
    }

    private fun notifyListeners() {
        if (listeners.isEmpty()) return
        val snapshot = all
        pulse.env.callbackExecutor.execute { listeners.forEach { it(snapshot) } }
    }

    /**
     * Fetches [slots] (1-50 ids of lowercase letters, digits, `-` and `_`; others are skipped) for
     * the current person and returns the content of the non-empty ones, by slot id. On failure the
     * cached content of those slots is returned. Runs on the calling thread (not the main thread).
     */
    fun fetchBlocking(slots: List<String>): Map<String, SlotContent> {
        val ids = clean(slots)
        if (ids.isEmpty()) return emptyMap()
        val gen = synchronized(lock) {
            known.addAll(ids)
            generation
        }
        if (pulse.isOptedOut || pulse.disposed) return pick(ids)
        val result = try {
            request(ids)
        } catch (e: Exception) {
            pulse.log("fetching app content failed: $e", always = true)
            null
        }
        if (result != null) {
            synchronized(lock) {
                if (gen != generation) return@synchronized // identify/reset meanwhile: answer was for someone else
                val now = pulse.env.clock()
                for (id in ids) {
                    val c = result[id]
                    if (c == null || c.isEmpty) cache.remove(id) else cache[id] = c
                    fetched[id] = now
                }
            }
            notifyListeners()
        }
        return pick(ids)
    }

    /** [fetchBlocking] on the SDK's worker; [callback] runs on the callback executor. */
    fun fetch(slots: List<String>, callback: (Map<String, SlotContent>) -> Unit = {}) {
        pulse.env.dispatcher.execute {
            val r = fetchBlocking(slots)
            pulse.env.callbackExecutor.execute { callback(r) }
        }
    }

    /**
     * Returns the cached content of [slots] when every one of them was fetched for the current
     * person less than 15 minutes ago, and fetches them otherwise. [callback] runs on the callback
     * executor. `PulseSlotView` uses this when it is attached.
     */
    fun load(slots: List<String>, callback: (Map<String, SlotContent>) -> Unit = {}) {
        val ids = clean(slots)
        val fresh = synchronized(lock) {
            val now = pulse.env.clock()
            ids.all { id -> fetched[id]?.let { now - it < REFRESH_AFTER_MILLIS } == true }
        }
        if (fresh) {
            val r = pick(ids)
            pulse.env.callbackExecutor.execute { callback(r) }
        } else {
            fetch(ids, callback)
        }
    }

    /** Records `app_content_viewed` {slot, variant}. `PulseSlotView` calls it once per shown content. */
    fun trackViewed(slot: String, content: SlotContent) {
        pulse.track(EVENT_VIEWED, props(slot, content))
    }

    /**
     * Records `app_content_clicked` {slot, variant, link} and, when [open] is true and the card has a
     * deep link, opens it through `PulseConfig.onInAppAction` (like in-app buttons), on the callback
     * executor.
     */
    @JvmOverloads
    fun trackClicked(slot: String, content: SlotContent, open: Boolean = true) {
        val link = content.card?.deepLink
        val p = props(slot, content)
        p["link"] = link
        pulse.track(EVENT_CLICKED, p)
        val action = pulse.config.onInAppAction
        if (open && link != null && action != null) { // opened even while opted out: the person tapped it
            pulse.env.callbackExecutor.execute {
                try {
                    action(link)
                } catch (e: Exception) {
                    pulse.log("onInAppAction threw: $e", always = true)
                }
            }
        }
    }

    private fun props(slot: String, content: SlotContent): LinkedHashMap<String, Any?> {
        val p = LinkedHashMap<String, Any?>()
        p["slot"] = slot
        p["variant"] = content.variant
        return p
    }

    private fun pick(ids: List<String>): Map<String, SlotContent> = synchronized(lock) {
        val out = LinkedHashMap<String, SlotContent>()
        for (id in ids) cache[id]?.let { out[id] = it }
        out
    }

    private fun clean(slots: List<String>): List<String> {
        val ids = LinkedHashSet<String>()
        for (s in slots) {
            if (SLOT_ID.matches(s)) ids.add(s) else pulse.log("app content: skipping invalid slot id \"$s\"", always = true)
        }
        if (ids.size > MAX_SLOTS) pulse.log("app content: only the first $MAX_SLOTS slots are fetched", always = true)
        return ids.take(MAX_SLOTS)
    }

    private fun request(ids: List<String>): Map<String, SlotContent>? {
        val body = LinkedHashMap<String, Any?>()
        body["id"] = pulse.currentId
        pulse.identifiedHash?.let { body["user_hash"] = it }
        body["platform"] = pulse.env.platform.wireName
        body["slots"] = ids
        val res = try {
            pulse.post("/v1/web/content", body)
        } catch (e: IOException) {
            pulse.log("fetching app content failed: $e", always = true)
            return null
        }
        if (res.status !in 200..299) {
            pulse.log("app content: server answered ${res.status}: ${res.body}", always = true)
            return null
        }
        val j = Json.decodeObject(res.body) ?: run {
            pulse.log("app content answer is unreadable", always = true)
            return null
        }
        val out = HashMap<String, SlotContent>()
        for ((slot, v) in j["content"].asMap()) {
            if (v is Map<*, *>) out[slot] = SlotContent.fromJson(v.asMap())
        }
        pulse.log("app content for ${pulse.currentId}: ${out.keys.sorted()}")
        return out
    }

    // After identify, reset and opt-in: drop the previous person's content and fetch the known slots again.
    internal fun reload() {
        val ids = synchronized(lock) {
            generation++
            cache.clear()
            fetched.clear()
            known.toList()
        }
        notifyListeners()
        if (ids.isNotEmpty() && !pulse.isOptedOut) fetch(ids)
    }

    internal fun refreshIfStale() {
        val ids = synchronized(lock) {
            val now = pulse.env.clock()
            known.filter { id -> fetched[id]?.let { now - it >= REFRESH_AFTER_MILLIS } ?: true }
        }
        if (ids.isNotEmpty()) fetch(ids)
    }

    companion object {
        /** How old cached content may get before it is fetched again on foreground: 15 minutes. */
        const val REFRESH_AFTER_MILLIS: Long = 15 * 60 * 1000

        /** Slots per request. */
        const val MAX_SLOTS: Int = 50

        const val EVENT_VIEWED = "app_content_viewed"
        const val EVENT_CLICKED = "app_content_clicked"

        private val SLOT_ID = Regex("^[a-z0-9_-]+$")
    }
}
