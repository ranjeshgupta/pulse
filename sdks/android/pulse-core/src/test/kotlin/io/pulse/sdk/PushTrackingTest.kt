package io.pulse.sdk

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PushTrackingTest {
    @Test
    fun buildsReportUrls() {
        val u = "https://pulse.example.com/t/p/TOKEN"
        assertEquals("$u?a=clicked&platform=android", PushTracking.reportUrl(u, PushTracking.Action.CLICKED, PulsePlatform.ANDROID))
        assertEquals("$u?a=dismissed&platform=ios", PushTracking.reportUrl(u, PushTracking.Action.DISMISSED, PulsePlatform.IOS))
        assertEquals("$u?x=1&a=clicked&platform=ios", PushTracking.reportUrl("$u?x=1#frag", PushTracking.Action.CLICKED, PulsePlatform.IOS))
        assertEquals("http://h/t/p/t?a=clicked&platform=android", PushTracking.reportUrl("http://h/t/p/t", PushTracking.Action.CLICKED, PulsePlatform.ANDROID))
        for (bad in listOf("javascript:alert(1)", "file:///etc/passwd", "ftp://h/t", "/t/p/x", "", "https://", "not a url")) {
            assertNull(PushTracking.reportUrl(bad, PushTracking.Action.CLICKED, PulsePlatform.ANDROID), bad)
        }
    }

    @Test
    fun extractsTrackUrlFromData() {
        assertEquals("https://h/t/p/x", PushTracking.trackUrl(mapOf("pulse_track" to "https://h/t/p/x", "title" to "Hi")))
        assertNull(PushTracking.trackUrl(mapOf("title" to "Hi")))
        assertNull(PushTracking.trackUrl(mapOf("pulse_track" to "javascript:x")))
        assertNull(PushTracking.trackUrl(mapOf("pulse_track" to 5)))
        assertNull(PushTracking.trackUrl(null))
    }

    @Test
    fun clickIsPostedWithoutBodyOrSiteKey() {
        val t = TestEnv()
        t.http.handler = { HttpResponse(204) }
        val p = t.client()
        assertTrue(p.trackPushClicked(mapOf("pulse_track" to "https://pulse.example.com/t/p/abc")))
        val req = t.http.requests.single()
        assertEquals("POST", req.method)
        assertEquals("https://pulse.example.com/t/p/abc?a=clicked&platform=android", req.url)
        assertNull(req.body)
        assertFalse("X-Pulse-Site-Key" in req.headers)
        assertEquals(0, p.pendingPushReports)

        assertTrue(p.trackPushDismissed(mapOf("pulse_track" to "https://pulse.example.com/t/p/def")))
        assertEquals("https://pulse.example.com/t/p/def?a=dismissed&platform=android", t.http.requests.last().url)

        assertFalse(p.trackPushClicked(mapOf("other" to "x")))
        assertFalse(p.trackPushClicked(mapOf("pulse_track" to "intent://evil")))
        assertEquals(2, t.http.requests.size)
    }

    @Test
    fun reportsAreRetriedAndSurviveRestart() {
        val t = TestEnv()
        t.http.handler = offline()
        val p = t.client()
        p.trackPushClicked(mapOf("pulse_track" to "https://pulse.example.com/t/p/abc"))
        assertEquals(1, p.pendingPushReports)
        assertTrue(t.storage.read("pulse.sk_test.ptrack")!!.contains("t/p/abc"))
        p.dispose()

        val again = t.client()
        assertEquals(1, again.pendingPushReports)
        t.http.handler = { HttpResponse(503) }
        again.flushBlocking()
        assertEquals(1, again.pendingPushReports, "5xx keeps it")

        t.http.handler = { HttpResponse(404) }
        t.advance(10_000)
        assertEquals(0, again.pendingPushReports, "404 (bad token) drops it")
        assertNull(t.storage.read("pulse.sk_test.ptrack"))
    }
}
