package io.pulse.sdk

import java.io.IOException
import java.net.URLEncoder
import java.util.concurrent.atomic.AtomicBoolean

/**
 * The Pulse SDK core: identity, event queue, sessions, push tokens and push tracking, opt-out, the
 * app inbox, in-app campaigns and app content slots. It has no Android dependency; the
 * `pulse-android` module wraps it in the `Pulse` object with SharedPreferences storage, lifecycle
 * hooks and device details.
 *
 * People are anonymous (`anon-` + 24 hex characters) until [identify]. Events are queued, persisted,
 * and sent in batches every [PulseConfig.flushIntervalMillis], as soon as [PulseConfig.maxBatch]
 * events are waiting, and on [flush]. Public methods are thread-safe and never block on the network,
 * except the `...Blocking` ones.
 */
class PulseClient(val config: PulseConfig, internal val env: PulseEnvironment) {
    private val lock = Any()
    internal val keys = StorageKeys(config.siteKey)

    private var anonId: String = ""
    private var userId: String? = null
    private var userHash: String? = null
    private var push: Pair<String, PulsePlatform>? = null
    private var queue: MutableList<Map<String, Any?>> = ArrayList()
    private var sid: String? = null
    private var lastActivity: Long? = null
    private var optedOut = false
    private var warnedFull = false
    private val backoff = Backoff(env.random)
    private val flushing = AtomicBoolean(false)
    @Volatile
    internal var disposed = false
        private set
    private var timer: Cancellable? = null

    /** The current person's app inbox. */
    val inbox: PulseInbox = PulseInbox(this)

    /** In-app campaigns. */
    val inApp: PulseInApp = PulseInApp(this)

    /** App content slots. */
    val content: PulseContent = PulseContent(this)

    private val pushTracker = PushTracker(this)

    init {
        load()
        inApp.load()
        pushTracker.load()
        scheduleTimer()
        log("ready as $currentId with ${queue.size} queued events")
        if (config.inAppMessages && !optedOut) inApp.refresh()
    }

    /** The id events are recorded under: the identified user, else this install's anonymous id. */
    val currentId: String get() = synchronized(lock) { userId ?: anonId }

    /** This install's current anonymous id. */
    val anonymousId: String get() = synchronized(lock) { anonId }

    /** Whether a user is identified (since the last [reset]). */
    val isIdentified: Boolean get() = synchronized(lock) { userId != null }

    /** Number of events waiting to be sent. */
    val pendingEvents: Int get() = synchronized(lock) { queue.size }

    /** The id of the current session, or null before the first event (and after [reset]). */
    val sessionId: String? get() = synchronized(lock) { sid }

    /** Whether the person opted out of tracking with [optOut]. */
    val isOptedOut: Boolean get() = synchronized(lock) { optedOut }

    /** The device properties sent with identify, screen_view and session_start. */
    val deviceProperties: Map<String, String> get() = Device.buildProps(env.deviceInfo(), config)

    // The user hash, when a user is identified with one.
    internal val identifiedHash: String? get() = synchronized(lock) { if (userId == null) null else userHash }

    private fun load() {
        val s = env.storage
        var aid = s.read(keys.anonId)
        if (aid == null || !aid.startsWith("anon-")) {
            aid = newAnonId()
            s.write(keys.anonId, aid)
        }
        anonId = aid
        userId = s.read(keys.userId)
        userHash = if (userId == null) null else s.read(keys.userHash)
        optedOut = s.read(keys.optOut) == "1"
        queue = decodeQueue(s.read(keys.queue))
        sid = s.read(keys.sessionId)
        lastActivity = s.read(keys.sessionAt)?.toLongOrNull()
        val p = Json.decodeObject(s.read(keys.push))
        if (p != null) {
            val token = p.str("token")
            val platform = PulsePlatform.values().firstOrNull { it.wireName == p["platform"] }
            if (token != null && platform != null) push = token to platform else s.delete(keys.push)
        }
    }

    private fun decodeQueue(raw: String?): MutableList<Map<String, Any?>> {
        if (raw == null) return ArrayList()
        return try {
            Json.decode(raw).asList().map { it.asMap() }.filter { it.isNotEmpty() }.toMutableList()
        } catch (e: IllegalArgumentException) {
            log("stored queue is unreadable; starting empty", always = true)
            ArrayList()
        }
    }

    private fun randomHex(bytes: Int): String {
        val b = ByteArray(bytes)
        env.random.nextBytes(b)
        return b.joinToString("") { String.format("%02x", it.toInt() and 0xff) }
    }

    private fun newAnonId() = "anon-" + randomHex(12)

    // ---- tracking ----

    /**
     * Records event [name] with optional [props] for the current person.
     *
     * Throws [IllegalArgumentException] when the name is invalid (it must match
     * `^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`) or reserved. Does nothing while opted out. The event may show
     * an in-app campaign whose trigger is [name].
     */
    fun track(name: String, props: Map<String, Any?>? = null) {
        Validation.checkEventName(name)
        if (isOptedOut) return log("opted out: $name not recorded")
        enqueue(name, props)
        if (config.inAppMessages) {
            val screen = if (name == "screen_view") props?.get("screen") as? String else null
            inApp.onEvent(name, screen)
        }
    }

    /** Records a `screen_view` event with `props.screen` = [name], the session id and device props. */
    fun screen(name: String, props: Map<String, Any?>? = null) {
        require(name.isNotEmpty()) { "screen name is empty" }
        if (isOptedOut) return log("opted out: screen $name not recorded")
        val sid = session()
        val p = LinkedHashMap<String, Any?>()
        p.putAll(deviceProperties)
        p["session_id"] = sid
        props?.let { p.putAll(it) }
        p["screen"] = name
        add("screen_view", p)
        if (config.inAppMessages) inApp.onEvent("screen_view", name)
    }

    /**
     * Ties this install to your user id [userId] and updates their profile.
     *
     * When the person was anonymous, the event carries `props.anonymous_id` so the server merges the
     * anonymous visitor into the user. With identity verification on, pass [userHash]:
     * hex(HMAC-SHA256(identity secret, userId)) computed on your server. Throws
     * [IllegalArgumentException] for an invalid user id or attribute name.
     */
    fun identify(
        userId: String,
        attributes: Map<String, Any?>? = null,
        email: String? = null,
        userHash: String? = null,
    ) {
        Validation.checkUserId(userId)
        Validation.checkAttributes(attributes)
        val previous: String
        val wasAnonymous: Boolean
        val out: Boolean
        synchronized(lock) {
            previous = this.userId ?: anonId
            wasAnonymous = this.userId == null
            this.userId = userId
            this.userHash = userHash?.takeIf { it.isNotEmpty() }
            env.storage.write(keys.userId, userId)
            val h = this.userHash
            if (h != null) env.storage.write(keys.userHash, h) else env.storage.delete(keys.userHash)
            out = optedOut
        }
        if (previous != userId) inbox.forget()
        content.reload()
        if (out) return log("opted out: identify $userId stored locally, not sent")
        val props = LinkedHashMap<String, Any?>(deviceProperties)
        if (wasAnonymous) props["anonymous_id"] = previous
        enqueue("identify", props, attributes, email, flushWhenFull = false)
        flush()
        if (previous != userId) env.dispatcher.execute { registerStoredToken() }
        if (config.inAppMessages) inApp.refresh()
    }

    /**
     * Forgets the identified user (call it on sign-out): a new anonymous id is used from now on and
     * the session ends. Events already queued keep the id they were recorded under.
     */
    fun reset() {
        synchronized(lock) {
            userId = null
            userHash = null
            anonId = newAnonId()
            env.storage.delete(keys.userId)
            env.storage.delete(keys.userHash)
            env.storage.write(keys.anonId, anonId)
            sid = null
            env.storage.delete(keys.sessionId)
        }
        inbox.forget()
        content.reload()
        if (isOptedOut) return
        env.dispatcher.execute { registerStoredToken() }
        if (config.inAppMessages) inApp.refresh()
    }

    // ---- consent ----

    /**
     * Stops all tracking and sending on this device: queued events and push reports are deleted,
     * nothing is recorded or sent (events, push tokens, push reports, inbox and in-app requests)
     * until [optIn]. The choice is stored. Channel unsubscribes are separate and happen on the server.
     */
    fun optOut() {
        synchronized(lock) {
            optedOut = true
            env.storage.write(keys.optOut, "1")
            queue.clear()
            persistLocked()
        }
        pushTracker.clear()
        inApp.cancelPending()
        log("opted out")
    }

    /** Resumes tracking after [optOut]. Registers the stored push token again. */
    fun optIn() {
        synchronized(lock) {
            if (!optedOut) return
            optedOut = false
            env.storage.delete(keys.optOut)
        }
        log("opted in")
        env.dispatcher.execute { registerStoredToken() }
        if (config.inAppMessages) inApp.refresh()
        content.reload()
    }

    // ---- sessions and lifecycle ----

    // Returns the current session id, first starting a new one (queueing session_start) when there is
    // none or the last activity was 30+ minutes ago, and records now as the last activity.
    private fun session(): String {
        var start: String? = null
        val id = synchronized(lock) {
            val now = env.clock()
            val s = Device.nextSession(now, lastActivity, sid, { randomHex(12) })
            lastActivity = now
            env.storage.write(keys.sessionAt, now.toString())
            if (s.started) {
                sid = s.id
                env.storage.write(keys.sessionId, s.id)
                start = s.id
            }
            s.id
        }
        start?.let {
            log("session $it started")
            val p = LinkedHashMap<String, Any?>()
            p["session_id"] = it
            p.putAll(deviceProperties)
            add("session_start", p)
        }
        return id
    }

    /** Call when the app goes to the background (the Android module does). Flushes, and starts the 30-minute session clock. */
    fun onBackground() {
        synchronized(lock) {
            val now = env.clock()
            lastActivity = now
            env.storage.write(keys.sessionAt, now.toString())
        }
        if (config.flushOnBackground) flush()
    }

    /** Call when the app comes to the foreground (the Android module does). May start a new session and refresh in-app campaigns and app content. */
    fun onForeground() {
        if (isOptedOut) return
        session()
        if (config.inAppMessages) inApp.refreshIfStale()
        content.refreshIfStale()
    }

    // ---- queue ----

    internal fun enqueue(
        name: String,
        props: Map<String, Any?>? = null,
        attrs: Map<String, Any?>? = null,
        email: String? = null,
        flushWhenFull: Boolean = true,
    ) {
        session()
        add(name, props, attrs, email, flushWhenFull)
    }

    private fun add(
        name: String,
        props: Map<String, Any?>?,
        attrs: Map<String, Any?>? = null,
        email: String? = null,
        flushWhenFull: Boolean = true,
    ) {
        val full: Boolean
        synchronized(lock) {
            if (optedOut) return
            val p = LinkedHashMap<String, Any?>()
            props?.let { p.putAll(it) }
            val h = if (userId != null) userHash else null
            if (h != null) p["user_hash"] = h
            val ev = LinkedHashMap<String, Any?>()
            ev["id"] = randomHex(16)
            ev["external_id"] = userId ?: anonId
            ev["name"] = name
            ev["ts"] = Json.isoUtc(java.util.Date(env.clock()))
            if (p.isNotEmpty()) ev["props"] = Json.plain(p)
            if (!attrs.isNullOrEmpty()) ev["attrs"] = Json.plain(attrs)
            if (!email.isNullOrEmpty()) ev["email"] = email
            queue.add(ev)
            if (queue.size > config.maxQueue) {
                val n = queue.size - config.maxQueue
                queue.subList(0, n).clear()
                log("queue full (${config.maxQueue}): dropping the oldest events", always = !warnedFull)
                warnedFull = true
            }
            persistLocked()
            full = flushWhenFull && queue.size >= config.maxBatch && !backoff.isWaiting(env.clock())
        }
        log("queued $name")
        if (full) flush()
    }

    private fun persistLocked() {
        try {
            env.storage.write(keys.queue, Json.encode(queue))
        } catch (e: Exception) {
            log("could not save the queue: $e", always = true)
        }
    }

    private fun scheduleTimer() {
        if (disposed) return
        timer = env.dispatcher.schedule(config.flushIntervalMillis) {
            flushIfDue()
            scheduleTimer()
        }
    }

    private fun flushIfDue() {
        val due = synchronized(lock) { queue.isNotEmpty() && !backoff.isWaiting(env.clock()) }
        if (due) flushBlocking()
        pushTracker.sendIfDue()
    }

    /**
     * Sends every queued event (and pending push reports) now, on the SDK's worker thread.
     * Call it when the app goes to the background; the Android module does that itself.
     */
    fun flush() {
        env.dispatcher.execute { flushBlocking() }
    }

    /**
     * Sends queued events now on the calling thread (not the main thread). Events the server could
     * not take for now (429, 5xx, network errors) stay queued and are retried with backoff; events it
     * rejected (400, 403) are dropped. Never throws.
     */
    fun flushBlocking() {
        if (!flushing.compareAndSet(false, true)) return
        try {
            drain()
        } finally {
            flushing.set(false)
        }
        pushTracker.sendBlocking()
    }

    private enum class Outcome { SENT, DROPPED, RETRY }

    private fun drain() {
        while (true) {
            val batch = synchronized(lock) {
                if (queue.isEmpty() || disposed || optedOut) null else ArrayList(queue.take(config.maxBatch))
            } ?: return
            val outcome = send(batch)
            synchronized(lock) {
                if (outcome == Outcome.RETRY) {
                    val delay = backoff.onFailure(env.clock())
                    log("will retry ${queue.size} events in ${delay / 1000}s")
                    return
                }
                backoff.onSuccess()
                warnedFull = false
                val ids = batch.map { it["id"] }.toSet()
                queue.removeAll { it["id"] in ids }
                persistLocked()
            }
        }
    }

    private fun send(batch: List<Map<String, Any?>>): Outcome {
        val res = try {
            post("/v1/web/events", mapOf("events" to batch))
        } catch (e: IOException) {
            log("sending ${batch.size} events failed: $e")
            return Outcome.RETRY
        }
        val code = res.status
        if (code in 200..299) {
            log("sent ${batch.size} events")
            return Outcome.SENT
        }
        if (code == 400 || code == 403) {
            log("server rejected ${batch.size} events ($code): ${res.body}; dropping them", always = true)
            return Outcome.DROPPED
        }
        // 429, 5xx, and anything unexpected (e.g. 401 while a site key is being rotated): keep.
        log("server answered $code for ${batch.size} events: ${res.body}", always = code == 401)
        return Outcome.RETRY
    }

    // ---- push ----

    /**
     * Registers a push [token] (the FCM registration token, on Android and on iOS) for the current
     * person. The token is stored and registered again after [identify], [reset] and [optIn].
     * [callback] gets whether the server accepted it. Not sent while opted out.
     */
    fun registerPushToken(
        token: String,
        platform: PulsePlatform = env.platform,
        callback: ((Boolean) -> Unit)? = null,
    ) {
        require(token.isNotEmpty()) { "token is empty" }
        synchronized(lock) {
            push = token to platform
            env.storage.write(keys.push, Json.encode(mapOf("token" to token, "platform" to platform.wireName)))
        }
        env.dispatcher.execute {
            val ok = registerStoredToken()
            if (callback != null) env.callbackExecutor.execute { callback(ok) }
        }
    }

    /** The stored push token, if any. */
    val pushToken: String? get() = synchronized(lock) { push?.first }

    internal fun registerStoredToken(): Boolean {
        val body = LinkedHashMap<String, Any?>()
        val p: Pair<String, PulsePlatform>
        synchronized(lock) {
            if (optedOut || disposed) return false
            p = push ?: return false
            body["external_id"] = userId ?: anonId
            body["token"] = p.first
            body["platform"] = p.second.wireName
            if (userId != null && userHash != null) body["user_hash"] = userHash
        }
        try {
            val res = post("/v1/web/devices", body)
            if (res.status in 200..299) {
                log("registered ${p.second.wireName} push token for ${body["external_id"]}")
                return true
            }
            log("push token rejected: ${res.status} ${res.body}", always = true)
        } catch (e: IOException) {
            log("push token registration failed: $e", always = true)
        }
        return false
    }

    /**
     * Reports that a Pulse push was tapped. [data] is the push's data (FCM `RemoteMessage.data`, or
     * the notification intent's extras); it must hold `pulse_track`. Returns false (and does nothing)
     * when it doesn't, when the URL is not http(s), or while opted out. The report is queued, stored
     * and retried.
     */
    fun trackPushClicked(data: Map<String, *>?): Boolean = pushTracker.track(data, PushTracking.Action.CLICKED)

    /** Reports that a Pulse push was dismissed; see [trackPushClicked]. */
    fun trackPushDismissed(data: Map<String, *>?): Boolean = pushTracker.track(data, PushTracking.Action.DISMISSED)

    /** Push reports waiting to be sent. */
    val pendingPushReports: Int get() = pushTracker.size

    // ---- HTTP ----

    internal fun post(path: String, body: Map<String, Any?>): HttpResponse = env.http.execute(
        HttpRequest(
            "POST", config.host + path,
            mapOf("Content-Type" to "application/json", "X-Pulse-Site-Key" to config.siteKey),
            Json.encode(body), config.requestTimeoutMillis,
        )
    )

    internal fun get(path: String, query: Map<String, String>): HttpResponse {
        val q = query.entries.joinToString("&") { (k, v) -> enc(k) + "=" + enc(v) }
        val url = config.host + path + if (q.isEmpty()) "" else "?$q"
        return env.http.execute(
            HttpRequest("GET", url, mapOf("X-Pulse-Site-Key" to config.siteKey), null, config.requestTimeoutMillis)
        )
    }

    private fun enc(s: String) = URLEncoder.encode(s, "UTF-8").replace("+", "%20")

    /** Stops the flush timer and the worker. Queued events stay in storage for the next start. */
    fun dispose() {
        if (disposed) return
        disposed = true
        timer?.cancel()
        inApp.cancelPending()
        env.dispatcher.shutdown()
    }

    internal fun log(msg: String, always: Boolean = false) {
        if (config.debug || always) env.logger.log("[pulse] $msg")
    }
}

/** A small persisted queue of push click/dismiss report URLs, sent with backoff. */
internal class PushTracker(private val pulse: PulseClient) {
    private val lock = Any()
    private var urls: MutableList<String> = ArrayList()
    private val backoff = Backoff(pulse.env.random)
    private val sending = AtomicBoolean(false)

    val size: Int get() = synchronized(lock) { urls.size }

    fun load() {
        val raw = pulse.env.storage.read(pulse.keys.pushTracks) ?: return
        urls = try {
            Json.decode(raw).asList().filterIsInstance<String>().toMutableList()
        } catch (_: IllegalArgumentException) {
            ArrayList()
        }
    }

    private fun persistLocked() {
        if (urls.isEmpty()) pulse.env.storage.delete(pulse.keys.pushTracks)
        else pulse.env.storage.write(pulse.keys.pushTracks, Json.encode(urls))
    }

    fun track(data: Map<String, *>?, action: PushTracking.Action): Boolean {
        val track = PushTracking.trackUrl(data) ?: return false
        if (pulse.isOptedOut) return false
        val url = PushTracking.reportUrl(track, action, pulse.env.platform) ?: return false
        synchronized(lock) {
            if (url in urls) return true
            urls.add(url)
            while (urls.size > MAX) urls.removeAt(0)
            persistLocked()
        }
        pulse.log("push ${action.wireName} queued")
        pulse.env.dispatcher.execute { sendBlocking() }
        return true
    }

    fun clear() = synchronized(lock) {
        urls.clear()
        persistLocked()
    }

    fun sendIfDue() {
        val due = synchronized(lock) { urls.isNotEmpty() && !backoff.isWaiting(pulse.env.clock()) }
        if (due) sendBlocking()
    }

    fun sendBlocking() {
        if (!sending.compareAndSet(false, true)) return
        try {
            while (true) {
                val url = synchronized(lock) { if (pulse.isOptedOut) null else urls.firstOrNull() } ?: return
                val status = try {
                    pulse.env.http.execute(HttpRequest("POST", url, emptyMap(), null, pulse.config.requestTimeoutMillis)).status
                } catch (e: IOException) {
                    pulse.log("push report failed: $e")
                    -1
                }
                synchronized(lock) {
                    if (status == -1 || status == 429 || status >= 500) {
                        backoff.onFailure(pulse.env.clock())
                        return
                    }
                    backoff.onSuccess()
                    if (status !in 200..299) pulse.log("push report answered $status; dropping it", always = true)
                    urls.remove(url)
                    persistLocked()
                }
            }
        } finally {
            sending.set(false)
        }
    }

    companion object {
        const val MAX = 100
    }
}
