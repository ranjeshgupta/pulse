package io.pulse.sdk

import java.io.IOException
import java.util.Date
import java.util.concurrent.CopyOnWriteArrayList

/** One message in the app inbox. */
data class PulseInboxMessage(
    /** The inbox entry's id: pass it to markRead, delete, click and list(before). */
    val id: Long,
    /** The id of the send this message came from. */
    val messageId: String,
    val title: String,
    val body: String,
    val imageUrl: String?,
    /** Link or deep link to open when the message is tapped. */
    val url: String?,
    /** Category set by the sender. */
    val category: String?,
    /** Custom key-value data set by the sender. */
    val data: Map<String, String>,
    val createdAt: Date,
    /** When the message was read, or null while unread. */
    val readAt: Date?,
    /** When the server removes the message. */
    val expiresAt: Date,
) {
    val isRead: Boolean get() = readAt != null

    companion object {
        /** Reads one entry of the server's `messages` list. */
        fun fromJson(j: Map<String, Any?>): PulseInboxMessage = PulseInboxMessage(
            id = j["id"].asLong() ?: throw IllegalArgumentException("inbox message without id"),
            messageId = j.str("message_id") ?: "",
            title = j.str("title") ?: "",
            body = j.str("body") ?: "",
            imageUrl = j.str("image_url"),
            url = j.str("url"),
            category = j.str("category"),
            data = j["data"].asMap().entries.associate { (k, v) -> k to (v?.toString() ?: "") },
            createdAt = Json.parseTime(j.str("created_at")) ?: Date(0),
            readAt = Json.parseTime(j.str("read_at")),
            expiresAt = Json.parseTime(j.str("expires_at")) ?: Date(0),
        )
    }
}

/** One page of the inbox, newest first. */
data class PulseInboxPage(val messages: List<PulseInboxMessage> = emptyList(), val unread: Int = 0) {
    /** Whether older messages may exist: call `list(before = messages.last().id)`. */
    val hasMore: Boolean get() = messages.size >= PulseInbox.PAGE_SIZE

    companion object {
        val EMPTY = PulseInboxPage()
    }
}

/**
 * The current person's app inbox.
 *
 * A signed-in user needs their user hash (`identify(userId, userHash = ...)`) even when the workspace
 * does not require identity verification for events, because inboxes are private: without it the
 * calls fail with [PulseException]. Anonymous people need none. Network errors, 429 and 5xx don't
 * throw: `list` returns an empty page and the actions return 0 / false. While opted out nothing is
 * requested.
 *
 * The `...Blocking` methods run on the calling thread (not the main thread); the others run on the
 * SDK's worker and call back on the environment's callback executor (the main thread on Android).
 */
class PulseInbox internal constructor(private val pulse: PulseClient) {
    private val lock = Any()
    private var unread = 0
    private val known = HashMap<Long, Boolean>()
    private val listeners = CopyOnWriteArrayList<(Int) -> Unit>()

    /** Unread messages, as of the last [list] (and lowered by [markRead] and [delete]). */
    val unreadCount: Int get() = synchronized(lock) { unread }

    /** Calls [listener] with the unread count whenever it changes. Returns a handle that removes it. */
    fun addUnreadListener(listener: (Int) -> Unit): Cancellable {
        listeners.add(listener)
        return Cancellable { listeners.remove(listener) }
    }

    private fun setUnread(n: Int) {
        val changed = synchronized(lock) {
            val c = unread != n
            unread = n
            c
        }
        if (changed) pulse.env.callbackExecutor.execute { listeners.forEach { it(n) } }
    }

    /** Returns the newest 50 messages, or the 50 before message id [before], and updates [unreadCount]. */
    @Throws(PulseException::class)
    fun listBlocking(before: Long? = null): PulseInboxPage {
        if (pulse.isOptedOut) return PulseInboxPage.EMPTY
        checkHash()
        val query = LinkedHashMap<String, String>()
        query["id"] = pulse.currentId
        pulse.identifiedHash?.let { query["user_hash"] = it }
        if (before != null) query["before"] = before.toString()
        val res = try {
            pulse.get("/v1/web/inbox", query)
        } catch (e: IOException) {
            pulse.log("inbox request failed: $e", always = true)
            return PulseInboxPage.EMPTY
        }
        if (!ok(res, "list")) return PulseInboxPage.EMPTY
        val j = Json.decodeObject(res.body) ?: run {
            pulse.log("inbox answer is unreadable", always = true)
            return PulseInboxPage.EMPTY
        }
        val messages = j["messages"].asList().mapNotNull {
            try {
                PulseInboxMessage.fromJson(it.asMap())
            } catch (e: IllegalArgumentException) {
                null
            }
        }
        val n = j["unread"].asLong()?.toInt() ?: 0
        synchronized(lock) {
            if (before == null) known.clear()
            for (m in messages) known[m.id] = !m.isRead
        }
        setUnread(n)
        return PulseInboxPage(messages, n)
    }

    /** Marks messages [ids] read, or every message when [ids] is null. Returns how many changed. */
    @Throws(PulseException::class)
    fun markReadBlocking(ids: List<Long>? = null): Int {
        if (ids != null && ids.isEmpty()) return 0
        val changed = action("read", ids ?: emptyList())
        val n = synchronized(lock) {
            if (ids == null) {
                for (k in known.keys) known[k] = false
                0
            } else if (changed > 0) {
                for (id in ids) if (known.containsKey(id)) known[id] = false
                maxOf(0, unread - changed)
            } else unread
        }
        setUnread(n)
        return changed
    }

    /** Deletes messages [ids], or **every message** when [ids] is null. Returns how many were deleted. */
    @Throws(PulseException::class)
    fun deleteBlocking(ids: List<Long>? = null): Int {
        if (ids != null && ids.isEmpty()) return 0
        val changed = action("delete", ids ?: emptyList())
        val n = synchronized(lock) {
            if (ids == null) {
                known.clear()
                0
            } else if (changed > 0) {
                val unreadGone = ids.count { known.remove(it) == true }
                maxOf(0, unread - unreadGone)
            } else unread
        }
        setUnread(n)
        return changed
    }

    /** Records a tap on message [id] (a click in the sender's report). Open its `url` yourself. */
    @Throws(PulseException::class)
    fun clickBlocking(id: Long): Boolean = action("click", listOf(id)) > 0

    /** [listBlocking] on the worker; [callback] runs on the callback executor. */
    fun list(before: Long? = null, callback: (Result<PulseInboxPage>) -> Unit) = async(callback) { listBlocking(before) }

    /** [markReadBlocking] on the worker. */
    fun markRead(ids: List<Long>? = null, callback: (Result<Int>) -> Unit = {}) = async(callback) { markReadBlocking(ids) }

    /** [deleteBlocking] on the worker. */
    fun delete(ids: List<Long>? = null, callback: (Result<Int>) -> Unit = {}) = async(callback) { deleteBlocking(ids) }

    /** [clickBlocking] on the worker. */
    fun click(id: Long, callback: (Result<Boolean>) -> Unit = {}) = async(callback) { clickBlocking(id) }

    private fun <T> async(callback: (Result<T>) -> Unit, work: () -> T) {
        pulse.env.dispatcher.execute {
            val r = try {
                Result.success(work())
            } catch (e: PulseException) {
                Result.failure(e)
            }
            pulse.env.callbackExecutor.execute { callback(r) }
        }
    }

    private fun action(action: String, ids: List<Long>): Int {
        if (pulse.isOptedOut) return 0
        checkHash()
        val body = LinkedHashMap<String, Any?>()
        body["id"] = pulse.currentId
        pulse.identifiedHash?.let { body["user_hash"] = it }
        body["action"] = action
        body["message_ids"] = ids
        val res = try {
            pulse.post("/v1/web/inbox", body)
        } catch (e: IOException) {
            pulse.log("inbox $action failed: $e", always = true)
            return 0
        }
        if (!ok(res, action)) return 0
        return Json.decodeObject(res.body)?.get("changed").asLong()?.toInt() ?: 0
    }

    private fun checkHash() {
        if (pulse.isIdentified && pulse.identifiedHash == null) {
            throw PulseException(
                "the inbox of a signed-in user needs their user hash: call identify(userId, userHash = " +
                    "hex(HMAC-SHA256(identity secret, userId))) with the hash computed on your server"
            )
        }
    }

    private fun ok(res: HttpResponse, what: String): Boolean {
        val code = res.status
        if (code in 200..299) return true
        if (code == 403) {
            throw PulseException(
                "the server refused the inbox of ${pulse.currentId}: a signed-in user needs a valid user hash",
                403,
            )
        }
        pulse.log(
            if (code == 429) "inbox $what rate limited (429); try again later" else "inbox $what answered $code: ${res.body}",
            always = true,
        )
        return false
    }

    internal fun forget() {
        synchronized(lock) { known.clear() }
        setUnread(0)
    }

    companion object {
        /** Messages per page of `GET /v1/web/inbox`. */
        const val PAGE_SIZE: Int = 50
    }
}
