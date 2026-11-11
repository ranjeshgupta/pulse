package io.pulse.sdk

import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale
import java.util.TimeZone

/**
 * A small JSON encoder and decoder, so pulse-core has no dependencies.
 *
 * Decoded values are `Map<String, Any?>`, `List<Any?>`, `String`, `Long`, `Double`, `Boolean` or
 * null. When encoding, a `Date` becomes an ISO 8601 UTC string and an unknown object its
 * `toString()`; non-finite numbers become null.
 */
object Json {
    fun encode(value: Any?): String = StringBuilder().also { write(it, value) }.toString()

    /** Parses [text]. Throws [IllegalArgumentException] for invalid JSON. */
    fun decode(text: String): Any? = Parser(text).parseDocument()

    /** Parses [text] as a JSON object, or returns null when it is not one (or not JSON). */
    fun decodeObject(text: String?): Map<String, Any?>? {
        if (text == null) return null
        return try {
            @Suppress("UNCHECKED_CAST")
            decode(text) as? Map<String, Any?>
        } catch (_: IllegalArgumentException) {
            null
        }
    }

    /** Converts [value] to plain JSON values (what encoding and decoding it would give). */
    @Suppress("UNCHECKED_CAST")
    fun plain(value: Map<String, Any?>): Map<String, Any?> = decode(encode(value)) as Map<String, Any?>

    private fun write(sb: StringBuilder, v: Any?) {
        when (v) {
            null -> sb.append("null")
            is String -> writeString(sb, v)
            is Boolean -> sb.append(v)
            is Int, is Long, is Short, is Byte -> sb.append(v.toString())
            is Double -> writeDouble(sb, v)
            is Float -> writeDouble(sb, v.toDouble())
            is Number -> sb.append(v.toString())
            is Date -> writeString(sb, isoUtc(v))
            is Map<*, *> -> {
                sb.append('{')
                var first = true
                for ((k, value) in v) {
                    if (!first) sb.append(',')
                    first = false
                    writeString(sb, k.toString())
                    sb.append(':')
                    write(sb, value)
                }
                sb.append('}')
            }
            is Iterable<*> -> writeArray(sb, v.iterator())
            is Array<*> -> writeArray(sb, v.iterator())
            else -> writeString(sb, v.toString())
        }
    }

    private fun writeArray(sb: StringBuilder, it: Iterator<*>) {
        sb.append('[')
        var first = true
        while (it.hasNext()) {
            if (!first) sb.append(',')
            first = false
            write(sb, it.next())
        }
        sb.append(']')
    }

    private fun writeDouble(sb: StringBuilder, d: Double) {
        when {
            d.isNaN() || d.isInfinite() -> sb.append("null")
            d == Math.floor(d) && Math.abs(d) < 1e15 -> sb.append(d.toLong())
            else -> sb.append(d.toString())
        }
    }

    private fun writeString(sb: StringBuilder, s: String) {
        sb.append('"')
        for (c in s) {
            when (c) {
                '"' -> sb.append("\\\"")
                '\\' -> sb.append("\\\\")
                '\n' -> sb.append("\\n")
                '\r' -> sb.append("\\r")
                '\t' -> sb.append("\\t")
                '\b' -> sb.append("\\b")
                '\u000C' -> sb.append("\\f")
                else -> if (c < ' ' || c == ' ' || c == ' ') {
                    sb.append(String.format("\\u%04x", c.code))
                } else {
                    sb.append(c)
                }
            }
        }
        sb.append('"')
    }

    /** Formats [date] as `2024-05-01T10:00:00.000Z`. */
    fun isoUtc(date: Date): String {
        val f = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.US)
        f.timeZone = TimeZone.getTimeZone("UTC")
        return f.format(date)
    }

    private val rfc3339 = Regex(
        """^(\d{4})-(\d{2})-(\d{2})[Tt ](\d{2}):(\d{2}):(\d{2})(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$"""
    )

    /** Parses an RFC 3339 time (as the server writes them), or returns null. */
    fun parseTime(s: String?): Date? {
        val m = rfc3339.matchEntire(s ?: return null) ?: return null
        val g = m.groupValues
        val cal = Calendar.getInstance(TimeZone.getTimeZone("UTC"), Locale.US)
        cal.clear()
        cal.set(g[1].toInt(), g[2].toInt() - 1, g[3].toInt(), g[4].toInt(), g[5].toInt(), g[6].toInt())
        var ms = cal.timeInMillis
        if (g[7].isNotEmpty()) ms += (g[7].substring(1).padEnd(3, '0').substring(0, 3)).toLong()
        val zone = g[8]
        if (zone != "Z" && zone != "z") {
            val sign = if (zone[0] == '-') -1 else 1
            val offset = zone.substring(1, 3).toLong() * 60 + zone.substring(4, 6).toLong()
            ms -= sign * offset * 60_000
        }
        return Date(ms)
    }

    private class Parser(private val s: String) {
        private var i = 0

        fun parseDocument(): Any? {
            skipWs()
            val v = parseValue()
            skipWs()
            if (i != s.length) fail("unexpected data after the value")
            return v
        }

        private fun fail(msg: String): Nothing = throw IllegalArgumentException("invalid JSON at $i: $msg")

        private fun skipWs() {
            while (i < s.length && (s[i] == ' ' || s[i] == '\n' || s[i] == '\r' || s[i] == '\t')) i++
        }

        private fun parseValue(): Any? {
            if (i >= s.length) fail("unexpected end")
            return when (val c = s[i]) {
                '{' -> parseObject()
                '[' -> parseArray()
                '"' -> parseString()
                't' -> literal("true", true)
                'f' -> literal("false", false)
                'n' -> literal("null", null)
                else -> if (c == '-' || c in '0'..'9') parseNumber() else fail("unexpected '$c'")
            }
        }

        private fun literal(word: String, v: Any?): Any? {
            if (!s.startsWith(word, i)) fail("expected $word")
            i += word.length
            return v
        }

        private fun parseObject(): Map<String, Any?> {
            val out = LinkedHashMap<String, Any?>()
            i++
            skipWs()
            if (i < s.length && s[i] == '}') { i++; return out }
            while (true) {
                skipWs()
                if (i >= s.length || s[i] != '"') fail("expected a key")
                val k = parseString()
                skipWs()
                if (i >= s.length || s[i] != ':') fail("expected ':'")
                i++
                skipWs()
                out[k] = parseValue()
                skipWs()
                if (i >= s.length) fail("unexpected end")
                when (s[i]) {
                    ',' -> i++
                    '}' -> { i++; return out }
                    else -> fail("expected ',' or '}'")
                }
            }
        }

        private fun parseArray(): List<Any?> {
            val out = ArrayList<Any?>()
            i++
            skipWs()
            if (i < s.length && s[i] == ']') { i++; return out }
            while (true) {
                skipWs()
                out.add(parseValue())
                skipWs()
                if (i >= s.length) fail("unexpected end")
                when (s[i]) {
                    ',' -> i++
                    ']' -> { i++; return out }
                    else -> fail("expected ',' or ']'")
                }
            }
        }

        private fun parseString(): String {
            i++ // opening quote
            val sb = StringBuilder()
            while (true) {
                if (i >= s.length) fail("unterminated string")
                val c = s[i++]
                when {
                    c == '"' -> return sb.toString()
                    c == '\\' -> {
                        if (i >= s.length) fail("unterminated escape")
                        when (val e = s[i++]) {
                            '"' -> sb.append('"')
                            '\\' -> sb.append('\\')
                            '/' -> sb.append('/')
                            'b' -> sb.append('\b')
                            'f' -> sb.append('\u000C')
                            'n' -> sb.append('\n')
                            'r' -> sb.append('\r')
                            't' -> sb.append('\t')
                            'u' -> {
                                if (i + 4 > s.length) fail("bad unicode escape")
                                val hex = s.substring(i, i + 4)
                                sb.append(hex.toIntOrNull(16)?.toChar() ?: fail("bad unicode escape"))
                                i += 4
                            }
                            else -> fail("bad escape '\\$e'")
                        }
                    }
                    c < ' ' -> fail("control character in string")
                    else -> sb.append(c)
                }
            }
        }

        private fun parseNumber(): Any {
            val start = i
            if (s[i] == '-') i++
            while (i < s.length && (s[i] in '0'..'9' || s[i] == '.' || s[i] == 'e' || s[i] == 'E' || s[i] == '+' || s[i] == '-')) i++
            val t = s.substring(start, i)
            val isInt = t.none { it == '.' || it == 'e' || it == 'E' }
            if (isInt) t.toLongOrNull()?.let { return it }
            return t.toDoubleOrNull() ?: fail("bad number '$t'")
        }
    }
}

internal fun Any?.asLong(): Long? = when (this) {
    is Long -> this
    is Int -> this.toLong()
    is Double -> if (this == Math.floor(this)) this.toLong() else null
    is Number -> this.toLong()
    else -> null
}

@Suppress("UNCHECKED_CAST")
internal fun Any?.asMap(): Map<String, Any?> = this as? Map<String, Any?> ?: emptyMap()

internal fun Any?.asList(): List<Any?> = this as? List<Any?> ?: emptyList()

internal fun Map<String, Any?>.str(key: String): String? = (this[key] as? String)?.takeIf { it.isNotEmpty() }
