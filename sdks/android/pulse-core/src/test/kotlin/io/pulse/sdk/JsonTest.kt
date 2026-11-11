package io.pulse.sdk

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class JsonTest {
    @Test
    fun roundTrip() {
        val v = mapOf(
            "s" to "a\"b\\c\n\u0001é😀", "i" to 1, "l" to 9_000_000_000L, "d" to 1.5, "whole" to 2.0,
            "b" to true, "n" to null, "list" to listOf(1, "x", listOf<Any>()), "m" to mapOf("k" to emptyMap<String, Any>()),
            "nan" to Double.NaN,
        )
        val text = Json.encode(v)
        assertEquals(
            mapOf(
                "s" to "a\"b\\c\n\u0001é😀", "i" to 1L, "l" to 9_000_000_000L, "d" to 1.5, "whole" to 2L,
                "b" to true, "n" to null, "list" to listOf(1L, "x", listOf<Any>()), "m" to mapOf("k" to emptyMap<String, Any>()),
                "nan" to null,
            ),
            Json.decode(text),
        )
        assertEquals("""{"u":"é"}""".let { Json.decode(it) }, mapOf("u" to "é"))
        assertEquals(listOf(-1.5e3, 0L), Json.decode(" [ -1.5e3 , 0 ] "))
    }

    @Test
    fun rejectsInvalid() {
        for (bad in listOf("", "{", "[1,]", "{\"a\"}", "tru", "\"x", "1 2", "{'a':1}")) {
            assertFailsWith<IllegalArgumentException>(bad) { Json.decode(bad) }
        }
        assertNull(Json.decodeObject("[1]"))
        assertNull(Json.decodeObject("nope"))
    }

    @Test
    fun parsesTimes() {
        assertEquals(0L, Json.parseTime("1970-01-01T00:00:00Z")!!.time)
        assertEquals(1500L, Json.parseTime("1970-01-01T00:00:01.5Z")!!.time)
        assertEquals(-3600_000L, Json.parseTime("1970-01-01T00:00:00+01:00")!!.time)
        assertNull(Json.parseTime("yesterday"))
        assertNull(Json.parseTime(null))
    }
}
