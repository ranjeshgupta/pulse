package io.pulse.sdk.android

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import android.util.Log
import io.pulse.sdk.PulsePlatform
import io.pulse.sdk.PushTracking

/**
 * Helpers for notifications your app builds itself (for example in `FirebaseMessagingService.onMessageReceived`
 * for data messages). For notification messages that FCM shows while the app is in the background,
 * call [Pulse.handleNotificationIntent] in your launcher activity instead: FCM puts the data keys,
 * including `pulse_track`, in the launch intent's extras.
 */
object PulseNotifications {
    internal const val ACTION_DISMISSED = "io.pulse.sdk.android.PUSH_DISMISSED"

    /** The tracking URL of a Pulse push (`RemoteMessage.data`), or null. */
    @JvmStatic
    fun trackUrl(data: Map<String, String>?): String? = PushTracking.trackUrl(data)

    /** Whether [data] is from a Pulse push. */
    @JvmStatic
    fun isPulsePush(data: Map<String, String>?): Boolean = trackUrl(data) != null

    /**
     * Adds the push's `pulse_track` to [intent] (the intent your notification opens), so
     * [Pulse.handleNotificationIntent] reports the tap. Returns [intent].
     */
    @JvmStatic
    fun addClickTracking(intent: Intent, data: Map<String, String>?): Intent {
        trackUrl(data)?.let { intent.putExtra(PushTracking.KEY, it) }
        return intent
    }

    /**
     * A PendingIntent for `NotificationCompat.Builder.setDeleteIntent`: when the person swipes the
     * notification away, the SDK reports `dismissed`. Returns null when [data] is not from Pulse.
     */
    @JvmStatic
    fun dismissIntent(context: Context, data: Map<String, String>?, requestCode: Int = 0): PendingIntent? {
        val url = trackUrl(data) ?: return null
        val intent = Intent(context, PulseNotificationReceiver::class.java)
            .setAction(ACTION_DISMISSED)
            .putExtra(PushTracking.KEY, url)
        var flags = PendingIntent.FLAG_UPDATE_CURRENT
        if (Build.VERSION.SDK_INT >= 23) flags = flags or PendingIntent.FLAG_IMMUTABLE
        return PendingIntent.getBroadcast(context, requestCode, intent, flags)
    }
}

/** Receives notification dismissals set up with [PulseNotifications.dismissIntent]. */
class PulseNotificationReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != PulseNotifications.ACTION_DISMISSED) return
        val url = intent.getStringExtra(PushTracking.KEY) ?: return
        if (!Pulse.isInitialized) {
            Log.w("Pulse", "push dismissed before Pulse.init; not reported")
            return
        }
        val pending = goAsync()
        try {
            Pulse.trackPushDismissed(mapOf(PushTracking.KEY to url))
        } finally {
            // The report is queued and stored; the worker sends it (now, or on the next start).
            pending.finish()
        }
    }
}

/**
 * Gets the FCM registration token without a compile-time Firebase dependency: when the app has
 * `com.google.firebase:firebase-messaging`, [registerCurrentToken] asks `FirebaseMessaging` for the
 * token by reflection and registers it. Also call `Pulse.registerPushToken(token)` from your
 * `FirebaseMessagingService.onNewToken`.
 */
object PulseFirebase {
    /** Whether firebase-messaging is on the classpath. */
    @JvmStatic
    val isAvailable: Boolean
        get() = try {
            Class.forName("com.google.firebase.messaging.FirebaseMessaging")
            true
        } catch (_: ClassNotFoundException) {
            false
        }

    /**
     * Fetches the current FCM token and registers it with [Pulse.registerPushToken]. [callback] gets
     * whether Pulse stored it (false when Firebase is missing or has no token).
     */
    @JvmStatic
    @JvmOverloads
    fun registerCurrentToken(callback: ((Boolean) -> Unit)? = null) {
        try {
            val fm = Class.forName("com.google.firebase.messaging.FirebaseMessaging")
            val messaging = fm.getMethod("getInstance").invoke(null)
            val task = fm.getMethod("getToken").invoke(messaging)!!
            val listenerType = Class.forName("com.google.android.gms.tasks.OnCompleteListener")
            val listener = java.lang.reflect.Proxy.newProxyInstance(listenerType.classLoader, arrayOf(listenerType)) { proxy, method, args ->
                when (method.name) {
                    "onComplete" -> {
                        val t = args!![0]
                        val ok = t.javaClass.getMethod("isSuccessful").invoke(t) as Boolean
                        val token = if (ok) t.javaClass.getMethod("getResult").invoke(t) as? String else null
                        if (token.isNullOrEmpty()) {
                            Log.w("Pulse", "FirebaseMessaging has no token")
                            callback?.invoke(false)
                        } else {
                            Pulse.registerPushToken(token, PulsePlatform.ANDROID, callback)
                        }
                        null
                    }
                    "hashCode" -> System.identityHashCode(proxy)
                    "equals" -> proxy === args?.get(0)
                    "toString" -> "PulseFirebaseListener"
                    else -> null
                }
            }
            task.javaClass.getMethod("addOnCompleteListener", listenerType).invoke(task, listener)
        } catch (e: Exception) {
            Log.w("Pulse", "could not get the FCM token (is firebase-messaging in the app?): $e")
            callback?.invoke(false)
        }
    }
}
