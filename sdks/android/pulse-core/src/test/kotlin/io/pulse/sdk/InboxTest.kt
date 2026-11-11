package io.pulse.sdk

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class InboxTest {
    private val page = """
        {"messages":[
          {"id":12,"message_id":"m-12","title":"Hello","body":"World","image_url":"https://img/x.png",
           "url":"app://offer","category":"promo","data":{"k":"v","n":3},
           "created_at":"2024-05-01T10:00:00.123456Z","read_at":null,"expires_at":"2024-06-01T10:00:00+05:30"},
          {"id":11,"message_id":"m-11","title":"","body":"Old","created_at":"2024-04-01T10:00:00Z",
           "read_at":"2024-04-02T10:00:00Z","expires_at":"2024-05-01T10:00:00Z"}
        ],"unread":5}
    """.trimIndent()

    @Test
    fun listParsesMessagesAndUnreadCount() {
        val t = TestEnv()
        t.http.handler = { HttpResponse(200, page) }
        val p = t.client()
        val counts = ArrayList<Int>()
        p.inbox.addUnreadListener { counts.add(it) }
        val res = p.inbox.listBlocking()
        val req = t.http.requests.last()
        assertEquals("GET", req.method)
        assertEquals("https://pulse.example.com/v1/web/inbox?id=${p.currentId}", req.url)
        assertEquals("sk_test", req.headers["X-Pulse-Site-Key"])

        assertEquals(5, res.unread)
        assertEquals(5, p.inbox.unreadCount)
        assertEquals(listOf(5), counts)
        assertFalse(res.hasMore)
        val m = res.messages[0]
        assertEquals(12L, m.id)
        assertEquals("m-12", m.messageId)
        assertEquals("Hello", m.title)
        assertEquals("https://img/x.png", m.imageUrl)
        assertEquals("app://offer", m.url)
        assertEquals("promo", m.category)
        assertEquals(mapOf("k" to "v", "n" to "3"), m.data)
        assertEquals("2024-05-01T10:00:00.123Z", Json.isoUtc(m.createdAt))
        assertEquals("2024-06-01T04:30:00.000Z", Json.isoUtc(m.expiresAt))
        assertFalse(m.isRead)
        assertTrue(res.messages[1].isRead)
        assertNull(res.messages[1].imageUrl)

        p.inbox.listBlocking(before = 11)
        assertTrue(t.http.requests.last().url.endsWith("&before=11"))
    }

    @Test
    fun signedInUserNeedsUserHash() {
        val t = TestEnv()
        t.http.handler = { HttpResponse(200, page) }
        val p = t.client()
        p.identify("u 1")
        t.http.requests.clear()
        assertFailsWith<PulseException> { p.inbox.listBlocking() }
        assertFailsWith<PulseException> { p.inbox.markReadBlocking(listOf(1)) }
        assertEquals(0, t.http.requests.size, "refused before sending")

        p.identify("u 1", userHash = "abc")
        t.http.handler = { HttpResponse(200, page) }
        p.inbox.listBlocking()
        assertEquals("https://pulse.example.com/v1/web/inbox?id=u%201&user_hash=abc", t.http.requests.last().url)

        t.http.handler = { HttpResponse(403, "{\"error\":\"forbidden\"}") }
        val e = assertFailsWith<PulseException> { p.inbox.listBlocking() }
        assertEquals(403, e.statusCode)

        var result: Result<PulseInboxPage>? = null
        p.inbox.list { result = it }
        assertTrue(result!!.exceptionOrNull() is PulseException)
    }

    @Test
    fun failuresReturnEmpty() {
        val t = TestEnv()
        val p = t.client()
        for (h in listOf<(HttpRequest) -> HttpResponse>({ HttpResponse(429) }, { HttpResponse(500) }, offline(), { HttpResponse(200, "not json") })) {
            t.http.handler = h
            assertEquals(PulseInboxPage.EMPTY, p.inbox.listBlocking())
            assertEquals(0, p.inbox.markReadBlocking(listOf(1)))
            assertFalse(p.inbox.clickBlocking(1))
        }
    }

    @Test
    fun actionsSendBodyAndAdjustUnread() {
        val t = TestEnv()
        t.http.handler = { HttpResponse(200, page) }
        val p = t.client()
        p.inbox.listBlocking()
        t.http.handler = { HttpResponse(200, "{\"changed\":1}") }
        assertEquals(1, p.inbox.markReadBlocking(listOf(12)))
        val body = Json.decode(t.http.requests.last().body!!) as Map<*, *>
        assertEquals(mapOf("id" to p.currentId, "action" to "read", "message_ids" to listOf(12L)), body)
        assertEquals(4, p.inbox.unreadCount)

        assertEquals(0, p.inbox.markReadBlocking(emptyList()))
        assertEquals(0, p.inbox.deleteBlocking(emptyList()))
        val n = t.http.requests.size
        assertTrue(p.inbox.clickBlocking(12))
        assertEquals("click", (Json.decode(t.http.requests.last().body!!) as Map<*, *>)["action"])
        assertEquals(n + 1, t.http.requests.size)

        // Deleting an unread message lowers the count; deleting a read one does not.
        t.http.handler = { HttpResponse(200, page) }
        p.inbox.listBlocking()
        t.http.handler = { HttpResponse(200, "{\"changed\":1}") }
        p.inbox.deleteBlocking(listOf(11))
        assertEquals(5, p.inbox.unreadCount)
        p.inbox.deleteBlocking(listOf(12))
        assertEquals(4, p.inbox.unreadCount)

        p.inbox.markReadBlocking()
        assertEquals(listOf<Long>(), (Json.decode(t.http.requests.last().body!!) as Map<*, *>)["message_ids"])
        assertEquals(0, p.inbox.unreadCount)
    }

    @Test
    fun identifyAndResetClearUnread() {
        val t = TestEnv()
        t.http.handler = { HttpResponse(200, page) }
        val p = t.client()
        p.inbox.listBlocking()
        assertEquals(5, p.inbox.unreadCount)
        p.identify("u1", userHash = "h")
        assertEquals(0, p.inbox.unreadCount)
    }
}
