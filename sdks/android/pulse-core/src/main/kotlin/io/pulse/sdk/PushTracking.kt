package io.pulse.sdk

import java.net.URI

/**
 * Push open (click) and dismissal tracking.
 *
 * Every Pulse push carries `pulse_track` in its data: a URL `{pulse}/t/p/{sealed token}`. The SDK
 * POSTs (no body, no site key) to it with `?a=clicked&platform=android` when the notification is
 * tapped, or `a=dismissed` when it is dismissed. The server answers 204.
 */
object PushTracking {
    /** The data key of the tracking URL in a Pulse push. */
    const val KEY: String = "pulse_track"

    /** Push click or dismissal. */
    enum class Action(val wireName: String) { CLICKED("clicked"), DISMISSED("dismissed") }

    /**
     * Returns the tracking URL from a push's data (FCM `RemoteMessage.data`, notification intent
     * extras), or null when the push did not come from Pulse or the URL is not http(s).
     */
    fun trackUrl(data: Map<String, *>?): String? {
        val v = data?.get(KEY) as? String ?: return null
        return v.takeIf { isHttpUrl(it) }
    }

    /** Whether [url] is an absolute http or https URL with a host. */
    fun isHttpUrl(url: String): Boolean = try {
        val u = URI(url.trim())
        val scheme = u.scheme?.lowercase()
        (scheme == "http" || scheme == "https") && !u.host.isNullOrEmpty()
    } catch (_: Exception) {
        false
    }

    /**
     * The URL to POST: [trackUrl] with `a=<action>&platform=<platform>` added to its query (a
     * fragment is dropped). Returns null when [trackUrl] is not an http(s) URL.
     */
    fun reportUrl(trackUrl: String, action: Action, platform: PulsePlatform): String? {
        if (!isHttpUrl(trackUrl)) return null
        val base = trackUrl.trim().substringBefore('#')
        val sep = when {
            !base.contains('?') -> "?"
            base.endsWith("?") || base.endsWith("&") -> ""
            else -> "&"
        }
        return "$base${sep}a=${action.wireName}&platform=${platform.wireName}"
    }
}

/** Exponential backoff: 2 s after the first failure, doubling up to 5 minutes, plus up to 1 s of jitter. */
internal class Backoff(private val random: java.util.Random) {
    var failures = 0
        private set
    var retryAt: Long? = null
        private set

    fun onFailure(now: Long): Long {
        failures++
        val secs = delaySeconds(failures)
        val delay = secs * 1000 + random.nextInt(1000)
        retryAt = now + delay
        return delay
    }

    fun onSuccess() {
        failures = 0
        retryAt = null
    }

    fun isWaiting(now: Long): Boolean = retryAt?.let { now < it } ?: false

    companion object {
        const val MAX_SECONDS: Long = 300

        /** 2^failures seconds, at most 5 minutes. */
        fun delaySeconds(failures: Int): Long =
            if (failures >= 9) MAX_SECONDS else minOf(1L shl failures, MAX_SECONDS)
    }
}

/** An error the app has to fix, such as a missing or wrong user hash for the inbox. */
class PulseException(message: String, val statusCode: Int? = null) : Exception(message)
