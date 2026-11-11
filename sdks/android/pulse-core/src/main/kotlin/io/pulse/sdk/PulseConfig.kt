package io.pulse.sdk

/** The app platform: sent with push tokens, push click tracking and in-app campaign requests. */
enum class PulsePlatform(val wireName: String) {
    /** An Android device (FCM registration token). */
    ANDROID("android"),

    /** An iOS device. Pulse sends through FCM, so this is the FCM registration token of the iOS app. */
    IOS("ios"),
}

/**
 * Settings for the SDK.
 *
 * [host] is the Pulse server's base URL (for example `https://pulse.example.com`) and [siteKey] the
 * workspace's public site key (`sk_...`). The site key is public: never put an API key or the identity
 * verification secret in an app.
 */
class PulseConfig(
    host: String,
    val siteKey: String,
    /** How often queued events are sent (default 10 s). */
    val flushIntervalMillis: Long = 10_000,
    /** Events per request (1-50), and the queue length that triggers an immediate flush. */
    val maxBatch: Int = 50,
    /** Most events kept while offline; the oldest are dropped beyond this. */
    val maxQueue: Int = 1000,
    /** Flush when the app goes to the background. */
    val flushOnBackground: Boolean = true,
    /** Connect and read timeout of one HTTP request (default 30 s). */
    val requestTimeoutMillis: Long = 30_000,
    /** Log what the SDK does. */
    val debug: Boolean = false,
    /** Fetch in-app campaigns and show them when their trigger event happens. */
    val inAppMessages: Boolean = true,
    /**
     * Called with the button's `value` when someone taps an in-app button whose action is `url`
     * (a web link or a deep link). The app opens it; the SDK only closes the message.
     */
    val onInAppAction: ((String) -> Unit)? = null,
    /** The app's version, sent as `app_version`. The Android module fills it from the package. */
    val appVersion: String? = null,
    /** The device model, sent as `device_model` (the Android module uses `Build.MODEL`). */
    val deviceModel: String? = null,
    /** The device maker, sent as `device_brand` (the Android module uses `Build.MANUFACTURER`). */
    val deviceBrand: String? = null,
    /** The OS version, sent as `os_version`, when you know it better than the SDK. */
    val osVersion: String? = null,
) {
    /** Server base URL without a trailing slash. */
    val host: String = host.trimEnd('/')

    init {
        require(this.host.isNotEmpty()) { "host is empty" }
        require(siteKey.isNotEmpty()) { "siteKey is empty" }
        require(maxBatch in 1..50) { "maxBatch must be 1-50, got $maxBatch" }
        require(maxQueue >= maxBatch) { "maxQueue must be at least maxBatch" }
        require(flushIntervalMillis > 0) { "flushIntervalMillis must be positive" }
        require(requestTimeoutMillis > 0) { "requestTimeoutMillis must be positive" }
    }
}

/**
 * What the platform knows about the device. The Android module reads it from `Build` and the
 * `Configuration`; values in [PulseConfig] (app version, model, brand, OS version) win over these.
 */
data class DeviceInfo(
    /** `android` or `ios`. */
    val platform: String? = null,
    /** `mobile` or `tablet`. */
    val deviceType: String? = null,
    /** `Android` or `iOS`. */
    val os: String? = null,
    val osVersion: String? = null,
    /** BCP 47 tag, e.g. `en-IN`. */
    val locale: String? = null,
    val appVersion: String? = null,
    val deviceModel: String? = null,
    val deviceBrand: String? = null,
)
