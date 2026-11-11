package io.pulse.sdk

/** Device properties and sessions. */
object Device {
    /** How long without events (or in the background) ends a session: 30 minutes. */
    const val SESSION_TIMEOUT_MILLIS: Long = 30 * 60 * 1000

    private const val MAX_LEN = 100

    internal fun clip(s: String?): String? {
        val t = s?.trim()
        if (t.isNullOrEmpty()) return null
        return if (t.length > MAX_LEN) t.substring(0, MAX_LEN) else t
    }

    /**
     * The device properties sent with identify, screen_view and session_start: platform, device_type,
     * os, os_version, locale, app_version, device_model and device_brand. Values in [config] win over
     * [info]; unknown values are left out and each is cut to 100 characters.
     */
    fun buildProps(info: DeviceInfo, config: PulseConfig?): Map<String, String> {
        val out = LinkedHashMap<String, String>()
        fun put(k: String, v: String?) {
            clip(v)?.let { out[k] = it }
        }
        put("platform", info.platform)
        put("device_type", info.deviceType)
        put("os", info.os)
        put("os_version", config?.osVersion ?: info.osVersion)
        put("device_brand", config?.deviceBrand ?: info.deviceBrand)
        put("device_model", config?.deviceModel ?: info.deviceModel)
        put("app_version", config?.appVersion ?: info.appVersion)
        put("locale", info.locale)
        return out
    }

    /** The result of [nextSession]. */
    data class Session(val id: String, val started: Boolean)

    /**
     * Decides which session an event at [now] belongs to: the current one ([sid]) while the [last]
     * activity was less than [timeoutMillis] ago, else a new one from [newId]. A clock that went
     * backwards keeps the session.
     */
    fun nextSession(
        now: Long,
        last: Long?,
        sid: String?,
        newId: () -> String,
        timeoutMillis: Long = SESSION_TIMEOUT_MILLIS,
    ): Session {
        if (!sid.isNullOrEmpty() && last != null && now - last < timeoutMillis) return Session(sid, false)
        return Session(newId(), true)
    }
}
