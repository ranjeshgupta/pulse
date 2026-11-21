package io.pulse.sdk

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ContentTest {
    private val banner = mapOf(
        "card" to mapOf(
            "title" to "Hi VIP", "body" to "20% off", "image_url" to "https://cdn.example.com/a.png",
            "button_text" to "Shop", "deep_link" to "app://shop", "data" to mapOf("color" to "gold", "n" to 3),
        ),
        "variant" to "vip",
    )

    private fun setup(
        content: Map<String, Any?> = mapOf("home_banner" to banner, "promo" to mapOf("html" to "<b>x</b>")),
        onAction: ((String) -> Unit)? = null,
    ): Pair<TestEnv, PulseClient> {
        val t = TestEnv()
        val body = Json.encode(mapOf("content" to content))
        t.http.handler = { if (it.url.endsWith("/v1/web/content")) HttpResponse(200, body) else HttpResponse(202) }
        return t to t.client(config(onInAppAction = onAction))
    }

    private fun fetch(p: PulseClient, vararg slots: String): Map<String, SlotContent> {
        var got: Map<String, SlotContent>? = null
        p.content.fetch(slots.toList()) { got = it }
        return got!!
    }

    @Test
    fun requestBodyWithPlatformSlotsAndUserHash() {
        val (t, p) = setup()
        assertEquals(0, t.http.to("/v1/web/content").size, "nothing is fetched before the app asks")
        fetch(p, "home_banner", "promo", "home_banner")
        val req = t.http.to("/v1/web/content").single()
        assertEquals("POST", req.method)
        assertEquals("sk_test", req.headers["X-Pulse-Site-Key"])
        assertEquals(
            mapOf("id" to p.currentId, "platform" to "android", "slots" to listOf("home_banner", "promo")),
            Json.decode(req.body!!),
        )

        p.identify("u1", userHash = "h")
        assertEquals(
            mapOf("id" to "u1", "user_hash" to "h", "platform" to "android", "slots" to listOf("home_banner", "promo")),
            Json.decode(t.http.to("/v1/web/content").last().body!!),
            "identify fetches the known slots again with the hash",
        )
    }

    @Test
    fun invalidSlotIdsAreSkippedAndAtMost50Sent() {
        val (t, p) = setup()
        assertEquals(emptyMap(), fetch(p, "Home", "a b", ""))
        assertEquals(0, t.http.to("/v1/web/content").size)
        fetch(p, *Array(60) { "s$it" }, "Bad!")
        @Suppress("UNCHECKED_CAST")
        val sent = (Json.decode(t.http.to("/v1/web/content").single().body!!) as Map<String, Any?>)["slots"] as List<Any?>
        assertEquals(50, sent.size)
        assertEquals("s0", sent.first())
    }

    @Test
    fun parsesCardHtmlAndVariant() {
        val (_, p) = setup()
        val r = fetch(p, "home_banner", "promo", "missing")
        assertEquals(setOf("home_banner", "promo"), r.keys)
        val c = r.getValue("home_banner")
        assertEquals("vip", c.variant)
        assertEquals(
            SlotCard("Hi VIP", "20% off", "https://cdn.example.com/a.png", "Shop", "app://shop", mapOf("color" to "gold", "n" to "3")),
            c.card,
        )
        assertTrue(c.card!!.isDrawable)
        val promo = r.getValue("promo")
        assertEquals("<b>x</b>", promo.html)
        assertNull(promo.card)
        assertNull(promo.variant)

        val lenient = SlotContent.fromJson(Json.decode("""{"card":{},"html":"","variant":7}""").asMap())
        assertEquals(SlotCard(), lenient.card)
        assertTrue(!lenient.card!!.isDrawable)
        assertNull(lenient.html)
        assertTrue(SlotContent().isEmpty)
    }

    @Test
    fun cacheAndMissingSlotMeansEmpty() {
        val t = TestEnv()
        var content: Map<String, Any?> = mapOf("home_banner" to banner)
        t.http.handler = {
            if (it.url.endsWith("/v1/web/content")) HttpResponse(200, Json.encode(mapOf("content" to content))) else HttpResponse(202)
        }
        val p = t.client(config())
        assertNull(p.content.cached("home_banner"))
        fetch(p, "home_banner")
        assertEquals("vip", p.content.cached("home_banner")!!.variant)
        assertEquals(setOf("home_banner"), p.content.all.keys)

        content = emptyMap()
        assertEquals(emptyMap(), fetch(p, "home_banner"))
        assertNull(p.content.cached("home_banner"), "a slot missing from the answer is emptied")
    }

    @Test
    fun failureKeepsCache() {
        val (t, p) = setup()
        fetch(p, "home_banner")
        val before = p.content.cached("home_banner")

        t.http.handler = offline()
        assertEquals(before, fetch(p, "home_banner")["home_banner"])
        t.http.handler = { HttpResponse(500, "oops") }
        assertEquals(before, fetch(p, "home_banner")["home_banner"])
        t.http.handler = { HttpResponse(200, "not json") }
        assertEquals(before, fetch(p, "home_banner")["home_banner"])
        assertEquals(before, p.content.cached("home_banner"))
        assertTrue(t.logs.any { it.contains("server answered 500") })
    }

    @Test
    fun loadUsesCacheFor15MinutesAndForegroundRefreshes() {
        val (t, p) = setup()
        var got: Map<String, SlotContent>? = null
        p.content.load(listOf("home_banner")) { got = it }
        assertEquals(1, t.http.to("/v1/web/content").size)
        p.content.load(listOf("home_banner")) { got = it }
        assertEquals(1, t.http.to("/v1/web/content").size, "fresh: from the cache")
        assertEquals("vip", got!!["home_banner"]!!.variant)

        t.now += 10 * 60_000
        p.onForeground()
        assertEquals(1, t.http.to("/v1/web/content").size)
        t.now += 5 * 60_000
        p.onForeground()
        assertEquals(2, t.http.to("/v1/web/content").size)
    }

    @Test
    fun identifyAndResetClearTheCacheAndNotifyListeners() {
        val (t, p) = setup()
        val seen = ArrayList<Set<String>>()
        val handle = p.content.addListener { seen.add(it.keys) }
        fetch(p, "home_banner")
        assertEquals(listOf(setOf("home_banner")), seen)

        t.http.handler = offline()
        p.identify("u1")
        assertNull(p.content.cached("home_banner"), "the previous person's content is gone even if the refetch fails")
        assertEquals(setOf<String>(), seen.last())
        assertEquals(2, t.http.to("/v1/web/content").size)

        p.reset()
        assertEquals(3, t.http.to("/v1/web/content").size)
        handle.cancel()
        p.reset()
        assertEquals(3, seen.size, "removed listener is not called")
    }

    @Test
    fun optedOutRequestsNothing() {
        val (t, p) = setup()
        p.optOut()
        assertEquals(emptyMap(), fetch(p, "home_banner"))
        assertEquals(0, t.http.to("/v1/web/content").size)
        p.optIn()
        assertEquals(1, t.http.to("/v1/web/content").size, "opt-in fetches the known slots")
    }

    @Test
    fun viewedAndClickedEventsAndDeepLink() {
        val opened = ArrayList<String>()
        val (t, p) = setup(onAction = { opened.add(it) })
        val c = fetch(p, "home_banner").getValue("home_banner")
        p.content.trackViewed("home_banner", c)
        p.content.trackClicked("home_banner", c)
        p.content.trackClicked("promo", SlotContent(html = "<b>x</b>"))
        assertEquals(listOf("app://shop"), opened)
        p.flushBlocking()
        val events = t.http.sentEvents().filter { it["name"].toString().startsWith("app_content") }
        assertEquals(listOf("app_content_viewed", "app_content_clicked", "app_content_clicked"), events.map { it["name"] })
        assertEquals(mapOf("slot" to "home_banner", "variant" to "vip"), events[0].props())
        assertEquals(mapOf("slot" to "home_banner", "variant" to "vip", "link" to "app://shop"), events[1].props())
        assertEquals(mapOf("slot" to "promo", "variant" to null, "link" to null), events[2].props())
    }
}
