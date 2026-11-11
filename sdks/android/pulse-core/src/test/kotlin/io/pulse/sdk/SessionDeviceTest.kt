package io.pulse.sdk

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

class SessionDeviceTest {
    private val deviceKeys = setOf("platform", "device_type", "os", "os_version", "locale", "app_version", "device_model", "device_brand")

    @Test
    fun deviceVersusEventProps() {
        val t = TestEnv()
        val p = t.client()
        p.screen("Home", mapOf("step" to 2))
        p.track("custom")
        p.identify("u1")
        val events = t.http.sentEvents().associateBy { it["name"] }

        val start = events.getValue("session_start").props()
        assertEquals(deviceKeys + "session_id", start.keys)
        assertEquals(
            mapOf("platform" to "android", "device_type" to "mobile", "os" to "Android", "os_version" to "14",
                "locale" to "en-IN", "app_version" to "2.3.0", "device_model" to "Pixel 8", "device_brand" to "Google"),
            start.filterKeys { it in deviceKeys },
        )

        val screen = events.getValue("screen_view").props()
        assertEquals("Home", screen["screen"])
        assertEquals(2L, screen["step"])
        assertEquals(start["session_id"], screen["session_id"])
        assertTrue(screen.keys.containsAll(deviceKeys))

        assertTrue(events.getValue("identify").props().keys.containsAll(deviceKeys))
        assertNull(events.getValue("custom")["props"], "custom events carry no device props")
    }

    @Test
    fun devicePropsAreClippedAndConfigWins() {
        val info = DeviceInfo("ios", "tablet", "iOS", "17.4", "en-US", "1.0", "x".repeat(150), "  Apple  ")
        val props = Device.buildProps(info, PulseConfig("https://h", "sk", appVersion = "9.9", osVersion = ""))
        assertEquals(100, props["device_model"]!!.length)
        assertEquals("Apple", props["device_brand"])
        assertEquals("9.9", props["app_version"])
        assertFalse("os_version" in props, "a blank override leaves the value out")
        assertEquals("tablet", props["device_type"])
        assertEquals(emptyMap(), Device.buildProps(DeviceInfo(), null))
    }

    @Test
    fun sessionsEndAfter30MinutesIdle() {
        val t = TestEnv()
        val p = t.client()
        p.track("a")
        val first = p.sessionId!!
        t.now += 29 * 60_000
        p.track("b")
        assertEquals(first, p.sessionId)
        t.now += 30 * 60_000
        p.track("c")
        assertNotEquals(first, p.sessionId)
        p.flushBlocking()
        assertEquals(listOf("session_start", "a", "b", "session_start", "c"), t.http.eventNames())
    }

    @Test
    fun backgroundTimeCountsAndResumeStartsNewSession() {
        val t = TestEnv()
        val p = t.client()
        p.track("a")
        val first = p.sessionId
        t.now += 10 * 60_000
        p.onBackground() // flushes
        assertEquals(listOf("session_start", "a"), t.http.eventNames())
        t.now += 31 * 60_000
        p.onForeground()
        assertNotEquals(first, p.sessionId)
        p.flushBlocking()
        assertEquals("session_start", t.http.eventNames().last())

        // A short trip to the background keeps the session.
        val second = p.sessionId
        p.onBackground()
        t.now += 5 * 60_000
        p.onForeground()
        assertEquals(second, p.sessionId)
    }

    @Test
    fun sessionSurvivesRestartWithin30Minutes() {
        val t = TestEnv()
        val p = t.client()
        p.track("a")
        val sid = p.sessionId
        p.dispose()
        t.now += 5 * 60_000
        val again = t.client()
        assertEquals(sid, again.sessionId)
        again.track("b")
        assertEquals(sid, again.sessionId)
        assertEquals(sid, t.storage.read("pulse.sk_test.sid"))
    }

    @Test
    fun nextSessionRules() {
        var n = 0
        val id = { "s${++n}" }
        assertEquals(Device.Session("s1", true), Device.nextSession(1000, null, null, id))
        assertEquals(Device.Session("x", false), Device.nextSession(1000, 0, "x", id))
        assertEquals(Device.Session("x", false), Device.nextSession(0, 1000, "x", id), "clock went backwards")
        assertEquals(Device.Session("s2", true), Device.nextSession(Device.SESSION_TIMEOUT_MILLIS, 0, "x", id))
    }
}
