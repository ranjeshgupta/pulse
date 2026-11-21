package io.pulse.sdk.android

import android.app.Activity
import android.app.Application
import android.content.ComponentCallbacks
import android.content.Context
import android.content.Intent
import android.content.SharedPreferences
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import io.pulse.sdk.DeviceInfo
import io.pulse.sdk.PulseClient
import io.pulse.sdk.PulseConfig
import io.pulse.sdk.PulseContent
import io.pulse.sdk.PulseEnvironment
import io.pulse.sdk.PulseInApp
import io.pulse.sdk.PulseInbox
import io.pulse.sdk.PulseLogger
import io.pulse.sdk.PulsePlatform
import io.pulse.sdk.PulseStorage
import io.pulse.sdk.PushTracking
import java.lang.ref.WeakReference
import java.util.concurrent.Executor

/**
 * The Pulse SDK for Android. Call [init] once in `Application.onCreate`, then use the other methods
 * from anywhere. Everything except [init] is safe to call from any thread; network work runs on the
 * SDK's worker thread and callbacks come back on the main thread.
 */
object Pulse {
    @Volatile
    private var client: PulseClient? = null
    private var lifecycle: Lifecycle? = null

    /** The core client [init] created. Throws if [init] was not called. */
    @JvmStatic
    val instance: PulseClient
        get() = client ?: throw IllegalStateException("Pulse.init must be called before using Pulse")

    /** Whether [init] has run. */
    @JvmStatic
    val isInitialized: Boolean get() = client != null

    /**
     * Starts the SDK: loads the stored ids and queued events, starts the flush timer, watches the app's
     * activities (flush and session end in the background, in-app messages in the foreground) and
     * fetches in-app campaigns. Calling it again replaces the previous instance.
     */
    @JvmStatic
    @Synchronized
    fun init(context: Context, config: PulseConfig): PulseClient {
        val app = context.applicationContext as Application
        client?.dispose()
        lifecycle?.let { app.unregisterActivityLifecycleCallbacks(it); app.unregisterComponentCallbacks(it) }

        val main = Handler(Looper.getMainLooper())
        val lc = Lifecycle()
        val device = AndroidDevice(app)
        val env = PulseEnvironment(
            storage = SharedPreferencesStorage(app.getSharedPreferences("pulse_sdk", Context.MODE_PRIVATE)),
            platform = PulsePlatform.ANDROID,
            deviceInfo = { device.info() },
            callbackExecutor = Executor { main.post(it) },
            logger = PulseLogger { Log.i("Pulse", it) },
            inAppPresenter = ActivityInAppPresenter(app, lc),
        )
        val c = PulseClient(config, env)
        lc.client = c
        lc.device = device
        app.registerActivityLifecycleCallbacks(lc)
        app.registerComponentCallbacks(lc)
        lifecycle = lc
        client = c
        return c
    }

    /** Records event [name] with optional [props]. Throws [IllegalArgumentException] for an invalid or reserved name. */
    @JvmStatic
    @JvmOverloads
    fun track(name: String, props: Map<String, Any?>? = null) = instance.track(name, props)

    /** Records a `screen_view` with `props.screen` = [name]. */
    @JvmStatic
    @JvmOverloads
    fun screen(name: String, props: Map<String, Any?>? = null) = instance.screen(name, props)

    /** Ties this install to [userId] (merging the anonymous visitor the first time). See [PulseClient.identify]. */
    @JvmStatic
    @JvmOverloads
    fun identify(userId: String, attributes: Map<String, Any?>? = null, email: String? = null, userHash: String? = null) =
        instance.identify(userId, attributes, email, userHash)

    /** Forgets the identified user (sign-out). */
    @JvmStatic
    fun reset() = instance.reset()

    /** Sends queued events now (on the worker thread). */
    @JvmStatic
    fun flush() = instance.flush()

    /** Registers the FCM registration token for the current person. [callback] runs on the main thread. */
    @JvmStatic
    @JvmOverloads
    fun registerPushToken(token: String, platform: PulsePlatform = PulsePlatform.ANDROID, callback: ((Boolean) -> Unit)? = null) =
        instance.registerPushToken(token, platform, callback)

    /** Reports a tap on a Pulse push; [data] is `RemoteMessage.data` or the notification's extras. */
    @JvmStatic
    fun trackPushClicked(data: Map<String, *>?): Boolean = instance.trackPushClicked(data)

    /** Reports a dismissed Pulse push. */
    @JvmStatic
    fun trackPushDismissed(data: Map<String, *>?): Boolean = instance.trackPushDismissed(data)

    /**
     * Call from your launcher activity's `onCreate` and `onNewIntent`: when the activity was opened by
     * tapping a Pulse push, reports the click. Returns whether it was a Pulse push. An intent is only
     * reported once (the SDK removes `pulse_track` from it).
     */
    @JvmStatic
    fun handleNotificationIntent(intent: Intent?): Boolean {
        val extras = intent?.extras ?: return false
        val url = extras.getString(PushTracking.KEY) ?: return false
        intent.removeExtra(PushTracking.KEY)
        return instance.trackPushClicked(mapOf(PushTracking.KEY to url))
    }

    /** Stops all tracking and sending on this device and clears the queue (stored). */
    @JvmStatic
    fun optOut() = instance.optOut()

    /** Resumes tracking after [optOut]. */
    @JvmStatic
    fun optIn() = instance.optIn()

    @JvmStatic
    val isOptedOut: Boolean get() = instance.isOptedOut

    /** The id events are recorded under. */
    @JvmStatic
    val currentId: String get() = instance.currentId

    @JvmStatic
    val inbox: PulseInbox get() = instance.inbox

    @JvmStatic
    val inApp: PulseInApp get() = instance.inApp

    /** App content slots (`PulseContent`); [PulseSlotView] draws one. */
    @JvmStatic
    val content: PulseContent get() = instance.content

    /** Tracks started activities: background/foreground, and the activity in-app messages are shown over. */
    internal class Lifecycle : Application.ActivityLifecycleCallbacks, ComponentCallbacks {
        var client: PulseClient? = null
        var device: AndroidDevice? = null
        private var started = 0
        @Volatile
        var top: WeakReference<Activity>? = null

        override fun onActivityStarted(activity: Activity) {
            if (activity !is PulseInAppActivity) top = WeakReference(activity)
            if (started++ == 0) client?.onForeground()
        }

        override fun onActivityStopped(activity: Activity) {
            started = maxOf(0, started - 1)
            if (started == 0 && !activity.isChangingConfigurations) client?.onBackground()
        }

        override fun onActivityResumed(activity: Activity) {
            if (activity !is PulseInAppActivity) top = WeakReference(activity)
        }

        override fun onActivityDestroyed(activity: Activity) {
            if (top?.get() === activity) top = null
        }

        override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) {}
        override fun onActivityPaused(activity: Activity) {}
        override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) {}

        override fun onConfigurationChanged(newConfig: Configuration) {
            device?.invalidate()
        }

        @Deprecated("Deprecated in Java")
        override fun onLowMemory() {}

        val inForeground: Boolean get() = started > 0
    }
}

/** [PulseStorage] on SharedPreferences. */
class SharedPreferencesStorage(private val prefs: SharedPreferences) : PulseStorage {
    override fun read(key: String): String? = prefs.getString(key, null)
    override fun write(key: String, value: String) {
        prefs.edit().putString(key, value).apply()
    }
    override fun delete(key: String) {
        prefs.edit().remove(key).apply()
    }
}

/** Device details from `Build`, the package and the `Configuration`. */
internal class AndroidDevice(private val app: Application) {
    @Volatile
    private var cached: DeviceInfo? = null

    fun invalidate() {
        cached = null
    }

    fun info(): DeviceInfo {
        cached?.let { return it }
        val cfg = app.resources.configuration
        val locale = if (Build.VERSION.SDK_INT >= 24) cfg.locales.get(0) else @Suppress("DEPRECATION") cfg.locale
        val appVersion = try {
            @Suppress("DEPRECATION")
            app.packageManager.getPackageInfo(app.packageName, 0).versionName
        } catch (_: PackageManager.NameNotFoundException) {
            null
        }
        val brand = Build.MANUFACTURER?.takeIf { it.isNotBlank() }?.let { it.substring(0, 1).uppercase() + it.substring(1) }
        val info = DeviceInfo(
            platform = "android",
            deviceType = if (cfg.smallestScreenWidthDp >= 600) "tablet" else "mobile",
            os = "Android",
            osVersion = Build.VERSION.RELEASE,
            locale = locale?.toLanguageTag()?.takeIf { it != "und" },
            appVersion = appVersion,
            deviceModel = Build.MODEL,
            deviceBrand = brand,
        )
        cached = info
        return info
    }
}
