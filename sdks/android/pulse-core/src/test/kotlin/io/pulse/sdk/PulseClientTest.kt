package io.pulse.sdk

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PulseClientTest {
    @Test
    fun anonymousIdIsPersistedAndNamespacedBySiteKey() {
        val t = TestEnv()
        val a = t.client(config(siteKey = "sk_a"))
        assertTrue(Regex("^anon-[0-9a-f]{24}$").matches(a.currentId))
        assertEquals(a.currentId, t.storage.read("pulse.sk_a.aid"))
        assertNull(t.storage.read("pulse.aid"))

        val again = t.client(config(siteKey = "sk_a"))
        assertEquals(a.currentId, again.currentId)

        val b = t.client(config(siteKey = "sk_b"))
        assertNotEquals(a.currentId, b.currentId)
        assertEquals(b.currentId, t.storage.read("pulse.sk_b.aid"))
    }

    @Test
    fun storageKeysAreNamespaced() {
        val k = StorageKeys("sk_1")
        assertEquals(
            listOf("pulse.sk_1.aid", "pulse.sk_1.uid", "pulse.sk_1.uh", "pulse.sk_1.queue", "pulse.sk_1.push",
                "pulse.sk_1.sid", "pulse.sk_1.sla", "pulse.sk_1.optout"),
            listOf(k.anonId, k.userId, k.userHash, k.queue, k.push, k.sessionId, k.sessionAt, k.optOut),
        )
    }

    @Test
    fun trackSendsEventWithSiteKeyHeader() {
        val t = TestEnv()
        val p = t.client()
        p.track("order_completed", mapOf("total" to 49.9, "items" to listOf(1, 2), "at" to java.util.Date(0)))
        p.flush()
        val req = t.http.to("/v1/web/events").single()
        assertEquals("https://pulse.example.com/v1/web/events", req.url)
        assertEquals("sk_test", req.headers["X-Pulse-Site-Key"])
        assertEquals("application/json", req.headers["Content-Type"])
        val ev = t.http.sentEvents().last()
        assertEquals("order_completed", ev["name"])
        assertEquals(p.currentId, ev["external_id"])
        assertEquals(49.9, ev.props()["total"])
        assertEquals("1970-01-01T00:00:00.000Z", ev.props()["at"])
        assertTrue((ev["id"] as String).matches(Regex("^[0-9a-f]{32}$")))
        assertEquals(listOf("session_start", "order_completed"), t.http.eventNames())
        assertEquals(0, p.pendingEvents)
    }

    @Test
    fun identifyMergesAnonymousIdOnlyTheFirstTime() {
        val t = TestEnv()
        val p = t.client()
        val anon = p.currentId
        p.identify("u1", attributes = mapOf("plan" to "pro"), email = "a@example.com", userHash = "h1")
        val ev = t.http.sentEvents().first { it["name"] == "identify" }
        assertEquals("u1", ev["external_id"])
        assertEquals(anon, ev.props()["anonymous_id"])
        assertEquals("h1", ev.props()["user_hash"])
        assertEquals("pro", (ev["attrs"] as Map<*, *>)["plan"])
        assertEquals("a@example.com", ev["email"])
        assertEquals("android", ev.props()["platform"])
        assertEquals("u1", t.storage.read("pulse.sk_test.uid"))
        assertEquals("h1", t.storage.read("pulse.sk_test.uh"))

        p.identify("u1", attributes = mapOf("plan" to "max"), userHash = "h1")
        val second = t.http.sentEvents().filter { it["name"] == "identify" }.last()
        assertNull(second.props()["anonymous_id"])

        p.track("x")
        p.flush()
        assertEquals("h1", t.http.sentEvents().last().props()["user_hash"])
    }

    @Test
    fun resetStartsNewAnonymousIdAndSession() {
        val t = TestEnv()
        val p = t.client()
        p.identify("u1")
        val sid = p.sessionId
        assertNotNull(sid)
        p.reset()
        assertFalse(p.isIdentified)
        assertTrue(p.currentId.startsWith("anon-"))
        assertNull(p.sessionId)
        assertNull(t.storage.read("pulse.sk_test.uid"))
        p.track("after")
        assertNotEquals(sid, p.sessionId)
    }

    @Test
    fun validationErrors() {
        val p = TestEnv().client()
        assertFailsWith<IllegalArgumentException> { p.track("1bad") }
        assertFailsWith<IllegalArgumentException> { p.track("has space") }
        assertFailsWith<IllegalArgumentException> { p.track("a".repeat(65)) }
        p.track("a".repeat(64))
        p.track("ok.name:with-dash_1")
        for (r in listOf("unsubscribed", "resubscribed", "tag_added", "tag_removed", "points_changed", "list_joined", "list_left")) {
            assertFailsWith<IllegalArgumentException> { p.track(r) }
        }
        assertFailsWith<IllegalArgumentException> { p.identify("") }
        assertFailsWith<IllegalArgumentException> { p.identify("anon-123") }
        assertFailsWith<IllegalArgumentException> { p.identify("é".repeat(128)) } // 256 bytes
        p.identify("é".repeat(127) + "a") // 255 bytes
        assertFailsWith<IllegalArgumentException> { p.identify("u", attributes = mapOf("bad-key" to 1)) }
        assertFailsWith<IllegalArgumentException> { p.identify("u", attributes = mapOf("1x" to 1)) }
        p.identify("u", attributes = mapOf("_ok" to 1))
        assertFailsWith<IllegalArgumentException> { p.screen("") }
        assertFailsWith<IllegalArgumentException> { PulseConfig("https://h", "sk", maxBatch = 51) }
        assertFailsWith<IllegalArgumentException> { PulseConfig("https://h", "sk", maxBatch = 0) }
        assertFailsWith<IllegalArgumentException> { PulseConfig("", "sk") }
    }

    @Test
    fun batchesAtMostMaxBatchEvents() {
        val t = TestEnv()
        t.http.handler = offline()
        val p = t.client(config(maxBatch = 3))
        repeat(7) { p.track("e$it") } // + session_start = 8 events
        assertEquals(8, p.pendingEvents)
        t.http.requests.clear()
        t.http.handler = { HttpResponse(202) }
        p.flushBlocking()
        val sizes = t.http.to("/v1/web/events").map { (Json.decode(it.body!!) as Map<*, *>)["events"] as List<*> }.map { it.size }
        assertEquals(listOf(3, 3, 2), sizes)
        assertEquals(0, p.pendingEvents)
    }

    @Test
    fun flushesAsSoonAsMaxBatchIsWaiting() {
        val t = TestEnv()
        val p = t.client(config(maxBatch = 2))
        p.track("a") // session_start + a = 2: flushed at once
        assertEquals(1, t.http.to("/v1/web/events").size)
        assertEquals(0, p.pendingEvents)
    }

    @Test
    fun flushTimerSendsQueuedEvents() {
        val t = TestEnv()
        val p = t.client()
        p.track("a")
        assertEquals(0, t.http.requests.size)
        t.advance(10_000)
        assertEquals(listOf("session_start", "a"), t.http.eventNames())
        assertEquals(0, p.pendingEvents)
    }

    @Test
    fun queueSurvivesRestart() {
        val t = TestEnv()
        t.http.handler = offline()
        val p = t.client()
        p.track("offline_1")
        p.track("offline_2")
        p.dispose()
        val again = t.client()
        assertEquals(3, again.pendingEvents)
        t.http.handler = { HttpResponse(202) }
        again.flushBlocking()
        assertEquals(listOf("session_start", "offline_1", "offline_2"), t.http.eventNames())
    }

    @Test
    fun maxQueueDropsOldest() {
        val t = TestEnv()
        t.http.handler = offline()
        val p = t.client(config(maxBatch = 5, maxQueue = 5))
        repeat(10) { p.track("e$it") }
        assertEquals(5, p.pendingEvents)
        t.http.handler = { HttpResponse(202) }
        p.flushBlocking()
        assertEquals(listOf("e5", "e6", "e7", "e8", "e9"), t.http.eventNames().takeLast(5))
    }

    @Test
    fun retriesWithBackoffOn429And5xxAndNetworkErrors() {
        for (failure in listOf<(HttpRequest) -> HttpResponse>({ HttpResponse(429) }, { HttpResponse(503) }, offline())) {
            val t = TestEnv()
            t.http.handler = failure
            val p = t.client()
            p.track("a")
            p.flushBlocking()
            assertEquals(2, p.pendingEvents, "kept for retry")
            val sent = t.http.requests.size

            // Backing off: the 10 s timer does not send before the retry time (2-3 s after the first failure, so it is due by 10 s).
            t.http.handler = { HttpResponse(202) }
            t.advance(10_000)
            assertEquals(sent + 1, t.http.requests.size)
            assertEquals(0, p.pendingEvents)
        }
    }

    @Test
    fun backoffDoublesToFiveMinutes() {
        assertEquals(listOf(2L, 4L, 8L, 16L, 32L, 64L, 128L, 256L, 300L, 300L),
            (1..10).map { Backoff.delaySeconds(it) })
        val t = TestEnv()
        t.http.handler = { HttpResponse(500) }
        val p = t.client()
        p.track("a")
        repeat(3) { p.flushBlocking() } // 3 failures: next retry in 8-9 s
        val before = t.http.requests.size
        t.advance(10_000) // first timer tick at +10 s: still within 8-9 s? no, 10 s > 9 s, so it retries
        assertEquals(before + 1, t.http.requests.size)
        repeat(3) { p.flushBlocking() } // 7 failures in all: 128 s
        val n = t.http.requests.size
        t.advance(10_000)
        t.advance(10_000)
        assertEquals(n, t.http.requests.size, "timer waits during backoff")
        p.flushBlocking() // an explicit flush always tries
        assertEquals(n + 1, t.http.requests.size)
    }

    @Test
    fun drops400And403Batches() {
        for (code in listOf(400, 403)) {
            val t = TestEnv()
            t.http.handler = { HttpResponse(code, "{\"error\":\"x\"}") }
            val p = t.client()
            p.track("a")
            p.flushBlocking()
            assertEquals(0, p.pendingEvents)
            assertTrue(t.logs.any { it.contains("dropping") })
        }
    }

    @Test
    fun optOutClearsQueueAndBlocksSending() {
        val t = TestEnv()
        t.http.handler = offline()
        val p = t.client()
        p.track("a")
        p.registerPushToken("tok")
        assertEquals(2, p.pendingEvents)
        p.optOut()
        assertTrue(p.isOptedOut)
        assertEquals(0, p.pendingEvents)
        assertEquals("1", t.storage.read("pulse.sk_test.optout"))

        t.http.handler = { HttpResponse(202, "{}") }
        t.http.requests.clear()
        p.track("b")
        p.screen("Home")
        p.identify("u1")
        p.registerPushToken("tok2")
        assertFalse(p.trackPushClicked(mapOf("pulse_track" to "https://pulse.example.com/t/p/abc")))
        assertEquals(0, p.inbox.listBlocking().messages.size)
        p.flushBlocking()
        t.advance(60_000)
        assertEquals(0, p.pendingEvents)
        assertEquals(emptyList(), t.http.requests.map { it.url })
        assertEquals("u1", p.currentId, "identify still sets the id locally")

        // The flag survives a restart.
        val again = t.client()
        assertTrue(again.isOptedOut)
        again.track("c")
        assertEquals(0, again.pendingEvents)

        again.optIn()
        assertFalse(again.isOptedOut)
        assertNull(t.storage.read("pulse.sk_test.optout"))
        assertEquals("tok2", (Json.decode(t.http.to("/v1/web/devices").last().body!!) as Map<*, *>)["token"])
        again.track("d")
        again.flushBlocking()
        assertTrue("d" in t.http.eventNames())
    }

    @Test
    fun pushTokenRegistrationAndReRegistration() {
        val t = TestEnv()
        val p = t.client()
        var ok: Boolean? = null
        p.registerPushToken("tok", PulsePlatform.ANDROID) { ok = it }
        assertEquals(true, ok)
        var body = Json.decode(t.http.to("/v1/web/devices").last().body!!) as Map<*, *>
        assertEquals(mapOf("external_id" to p.currentId, "token" to "tok", "platform" to "android"), body)

        p.identify("u1", userHash = "h")
        body = Json.decode(t.http.to("/v1/web/devices").last().body!!) as Map<*, *>
        assertEquals("u1", body["external_id"])
        assertEquals("h", body["user_hash"])

        p.reset()
        body = Json.decode(t.http.to("/v1/web/devices").last().body!!) as Map<*, *>
        assertTrue((body["external_id"] as String).startsWith("anon-"))
        assertEquals(3, t.http.to("/v1/web/devices").size)

        // iOS registers its FCM token with platform ios.
        val ios = TestEnv(platform = PulsePlatform.IOS)
        ios.client().registerPushToken("fcm-ios")
        assertEquals("ios", (Json.decode(ios.http.to("/v1/web/devices").single().body!!) as Map<*, *>)["platform"])

        t.http.handler = { HttpResponse(400, "bad") }
        p.registerPushToken("tok3") { ok = it }
        assertEquals(false, ok)
    }
}
