package io.pulse.sdk

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class InAppTest {
    class FakePresenter : InAppPresenter {
        var available = true
        val shown = ArrayList<PulseInAppMessage>()
        var close: ((InAppOutcome) -> Unit)? = null
        override fun canPresent() = available
        override fun present(message: PulseInAppMessage, done: (InAppOutcome) -> Unit) {
            shown.add(message)
            close = done
        }
        fun dismiss(outcome: InAppOutcome = InAppOutcome()) {
            val c = close!!
            close = null
            c(outcome)
        }
    }

    private fun campaign(id: Int, event: String, frequency: String, screen: String? = null, delay: Int = 0, endAt: String? = null) =
        mapOf(
            "id" to id, "layout" to "modal",
            "content" to mapOf("title" to "T$id", "body" to "B", "buttons" to listOf(
                mapOf("text" to "Go", "action" to "url", "value" to "app://go"),
                mapOf("text" to "Later", "action" to "dismiss"),
            )),
            "trigger" to mapOf("trigger_event" to event, "screen" to screen, "delay_seconds" to delay, "frequency" to frequency, "platforms" to listOf("android")),
            "end_at" to endAt,
        )

    private fun setup(vararg campaigns: Map<String, Any?>, onAction: ((String) -> Unit)? = null): Triple<TestEnv, PulseClient, FakePresenter> {
        val presenter = FakePresenter()
        val t = TestEnv(presenter = presenter)
        val body = Json.encode(mapOf("messages" to campaigns.toList()))
        t.http.handler = { if (it.url.endsWith("/v1/web/inapp")) HttpResponse(200, body) else HttpResponse(202) }
        val p = t.client(config(inApp = true, onInAppAction = onAction))
        return Triple(t, p, presenter)
    }

    private fun Triple<TestEnv, PulseClient, FakePresenter>.fire(name: String) {
        second.track(name)
        first.advance(0)
    }

    @Test
    fun fetchesCampaignsAtStartWithPlatform() {
        val (t, p, _) = setup(campaign(1, "app_open", "once"))
        val req = t.http.to("/v1/web/inapp").single()
        assertEquals(mapOf("id" to p.currentId, "platform" to "android"), Json.decode(req.body!!))
        val m = p.inApp.messages.single()
        assertEquals(1L, m.id)
        assertEquals("T1", m.content.title)
        assertEquals(2, m.content.buttons.size)
        assertTrue(m.content.buttons[0].isUrl)
        assertEquals("app_open", m.trigger.event)

        p.identify("u1", userHash = "h")
        assertEquals(mapOf("id" to "u1", "user_hash" to "h", "platform" to "android"), Json.decode(t.http.to("/v1/web/inapp").last().body!!))
        p.reset()
        assertEquals(3, t.http.to("/v1/web/inapp").size)
    }

    @Test
    fun onceIsShownOnlyOnceEvenAfterRestart() {
        val s = setup(campaign(1, "app_open", "once"))
        val (t, p, presenter) = s
        s.fire("app_open")
        assertEquals(1, presenter.shown.size)
        presenter.dismiss()
        s.fire("app_open")
        assertEquals(1, presenter.shown.size)
        p.flushBlocking()
        assertEquals(listOf("session_start", "app_open", "inapp_shown", "inapp_dismissed", "app_open"), t.http.eventNames())
        assertEquals(1L, t.http.sentEvents().first { it["name"] == "inapp_shown" }.props()["inapp_id"])

        p.dispose()
        val again = t.client(config(inApp = true))
        again.track("app_open")
        t.advance(0)
        assertEquals(1, presenter.shown.size)
    }

    @Test
    fun sessionFrequencyIsPerAppRun() {
        val s = setup(campaign(2, "add_to_cart", "session"))
        val (t, p, presenter) = s
        s.fire("add_to_cart"); presenter.dismiss()
        s.fire("add_to_cart")
        assertEquals(1, presenter.shown.size)
        p.dispose()
        val again = t.client(config(inApp = true))
        again.track("add_to_cart")
        t.advance(0)
        assertEquals(2, presenter.shown.size)
    }

    @Test
    fun dailyFrequency() {
        val s = setup(campaign(3, "x", "daily"))
        val (t, _, presenter) = s
        s.fire("x"); presenter.dismiss()
        s.fire("x")
        assertEquals(1, presenter.shown.size)
        t.now += 24 * 3600_000L
        s.fire("x")
        assertEquals(2, presenter.shown.size)
    }

    @Test
    fun alwaysFrequencyAndOneAtATime() {
        val s = setup(campaign(4, "x", "always"))
        val (_, p, presenter) = s
        s.fire("x")
        s.fire("x") // one on screen: ignored
        assertEquals(1, presenter.shown.size)
        assertTrue(p.inApp.isShowing)
        presenter.dismiss()
        assertFalse(p.inApp.isShowing)
        s.fire("x")
        assertEquals(2, presenter.shown.size)
    }

    @Test
    fun screenTriggerDelayAndEndAt() {
        val s = setup(
            campaign(5, "screen_view", "always", screen = "Checkout", delay = 3),
            campaign(6, "y", "always", endAt = "2000-01-01T00:00:00Z"),
        )
        val (t, p, presenter) = s
        p.screen("Home")
        t.advance(5_000)
        assertEquals(0, presenter.shown.size)
        p.screen("Checkout")
        t.advance(2_000)
        assertEquals(0, presenter.shown.size, "waits for delay_seconds")
        t.advance(1_000)
        assertEquals(5L, presenter.shown.single().id)
        presenter.dismiss()
        s.fire("y")
        assertEquals(1, presenter.shown.size, "ended campaign")
    }

    @Test
    fun clickRecordsEventAndCallsAction() {
        val opened = ArrayList<String>()
        val s = setup(campaign(7, "x", "always"), onAction = { opened.add(it) })
        val (t, p, presenter) = s
        s.fire("x")
        val m = presenter.shown.single()
        presenter.dismiss(InAppOutcome(m.content.buttons[0]))
        assertEquals(listOf("app://go"), opened)
        s.fire("x")
        presenter.dismiss(InAppOutcome(expired = true)) // a banner that timed out: nothing recorded
        p.flushBlocking()
        val names = t.http.eventNames()
        assertEquals(listOf("inapp_shown", "inapp_clicked", "inapp_shown"), names.filter { it.toString().startsWith("inapp") })
        val click = t.http.sentEvents().first { it["name"] == "inapp_clicked" }.props()
        assertEquals(mapOf("inapp_id" to 7L, "button" to "Go", "url" to "app://go"), click)
    }

    @Test
    fun notShownWithoutPresenterAndFrequencyNotUsed() {
        val s = setup(campaign(8, "x", "once"))
        val (_, _, presenter) = s
        presenter.available = false
        s.fire("x")
        assertEquals(0, presenter.shown.size)
        presenter.available = true
        s.fire("x")
        assertEquals(1, presenter.shown.size)
    }

    @Test
    fun refreshOnForegroundAtMostEvery15Minutes() {
        val (t, p, _) = setup(campaign(1, "x", "once"))
        assertEquals(1, t.http.to("/v1/web/inapp").size)
        t.now += 10 * 60_000
        p.onForeground()
        assertEquals(1, t.http.to("/v1/web/inapp").size)
        t.now += 5 * 60_000
        p.onForeground()
        assertEquals(2, t.http.to("/v1/web/inapp").size)
    }

    @Test
    fun parsesUnknownFieldsLeniently() {
        val m = PulseInAppMessage.fromJson(Json.decode("""{"id":9,"content":{},"trigger":{"trigger_event":"a"}}""").asMap())
        assertEquals("modal", m.layout)
        assertEquals("once", m.trigger.frequency)
        assertEquals(0, m.trigger.delaySeconds)
        assertTrue(m.trigger.matches("a", "any"))
    }
}
