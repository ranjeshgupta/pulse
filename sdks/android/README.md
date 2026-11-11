# Pulse Android SDK

Send events from an Android app to [Pulse](../../README.md): track events and screens, identify
signed-in users (with identity verification), register FCM push tokens, report push opens and
dismissals, show the app inbox and in-app messages, and let people opt out of tracking.

It mirrors the [Flutter SDK](../flutter/pulse_sdk/README.md) and the web SDK: people are anonymous
(`anon-` + 24 hex characters) until `identify`, and `identify` merges the anonymous visitor into the
user. Events are grouped into sessions, queued on the device, persisted (they survive app restarts)
and sent in batches.

The SDK only uses the workspace's public **site key**. Never put an API key or the identity
verification secret in an app.

Two Gradle modules:

| Module | What it is |
|---|---|
| `pulse-core` | Plain Kotlin/JVM, no Android dependency: queue, batching and backoff, sessions, validation, JSON, HTTP (`HttpURLConnection` behind `PulseHttpClient`), storage interface, inbox, in-app logic, push tracking, opt-out. No third-party dependencies. |
| `pulse-android` | Android library: the `Pulse` object, SharedPreferences storage, activity lifecycle (flush and sessions), device details from `Build` and `Configuration`, notification click/dismiss helpers, an FCM token helper (no compile-time Firebase dependency) and an in-app message activity. minSdk 21. |

## Install

The modules are not on a public Maven repository yet. Either include them in your build:

```kotlin
// settings.gradle.kts of your app
include(":pulse-core", ":pulse-android")
project(":pulse-core").projectDir = file("../pulse/sdks/android/pulse-core")
project(":pulse-android").projectDir = file("../pulse/sdks/android/pulse-android")

// app/build.gradle.kts
dependencies {
    implementation(project(":pulse-android"))
}
```

or publish them to your local Maven repository and depend on the artifacts:

```
cd sdks/android
gradle publishToMavenLocal          # needs an Android SDK (ANDROID_HOME or local.properties sdk.dir)
```

```kotlin
repositories { mavenLocal() }
dependencies { implementation("io.pulse:pulse-android:0.1.0") }
```

`settings.gradle.kts` includes `pulse-android` only when an Android SDK is found (`ANDROID_HOME`,
`ANDROID_SDK_ROOT` or `sdk.dir` in `local.properties`); without one only `pulse-core` is built.
The library's manifest adds the `INTERNET` permission, the in-app activity and the dismissal receiver.

## Initialise

In `Application.onCreate`:

```kotlin
import io.pulse.sdk.PulseConfig
import io.pulse.sdk.android.Pulse

class App : Application() {
    override fun onCreate() {
        super.onCreate()
        Pulse.init(this, PulseConfig(
            host = "https://pulse.example.com",
            siteKey = "sk_...",                 // Settings → Sign-in & keys → Your website in Pulse
            flushIntervalMillis = 10_000,
            maxBatch = 50,                      // events per request, 1-50
            debug = BuildConfig.DEBUG,          // logs every step to Logcat (tag "Pulse")
            onInAppAction = { url -> openLink(url) },
        ))
    }
}
```

Other options: `maxQueue` (default 1,000 events kept while offline; the oldest are dropped beyond that),
`flushOnBackground` (default true), `requestTimeoutMillis` (default 30 s), `inAppMessages` (default true),
and `appVersion`, `deviceModel`, `deviceBrand`, `osVersion` to override what the SDK reads from the
device. `PulseConfig` throws `IllegalArgumentException` for an empty host or site key, or `maxBatch`
outside 1-50.

After `init`, call `Pulse.*` from anywhere. Everything except `init` is thread-safe; network work
runs on the SDK's own worker thread, and callbacks come back on the main thread.

## API

| Call | What it does |
|---|---|
| `Pulse.init(context, config)` | Starts the SDK (returns the `PulseClient`). |
| `Pulse.track(name, props)` | Records an event. Throws `IllegalArgumentException` for a bad or reserved name. |
| `Pulse.screen(name, props)` | Records `screen_view` with `props.screen = name`, `session_id` and device props. |
| `Pulse.identify(userId, attributes, email, userHash)` | Ties the install to your user id. |
| `Pulse.reset()` | Sign-out: new anonymous id, session ends. |
| `Pulse.flush()` | Sends queued events now. |
| `Pulse.registerPushToken(token, platform, callback)` | Registers the FCM token (`PulsePlatform.ANDROID` by default). |
| `Pulse.trackPushClicked(data)` / `Pulse.trackPushDismissed(data)` | Reports a push open / dismissal. |
| `Pulse.handleNotificationIntent(intent)` | Reports the open when an activity was launched from a Pulse push. |
| `Pulse.optOut()` / `Pulse.optIn()` / `Pulse.isOptedOut` | Local tracking consent. |
| `Pulse.inbox` | The app inbox (`PulseInbox`). |
| `Pulse.inApp` | In-app campaigns (`PulseInApp`). |
| `Pulse.currentId` | The id events are recorded under. |
| `Pulse.instance` | The `PulseClient`: also `anonymousId`, `isIdentified`, `sessionId`, `pendingEvents`, `deviceProperties`, `pushToken`, `pendingPushReports`, `flushBlocking()`. |

All `Pulse.*` methods are `@JvmStatic`, so Java callers use `Pulse.track("x", null)`.

## Identify users

```kotlin
Pulse.identify(
    user.id,
    attributes = mapOf("first_name" to user.firstName, "plan" to user.plan),
    email = user.email,
    userHash = user.pulseHash,   // from your server, see below
)
```

The first `identify` after an anonymous period sends `props.anonymous_id`, so the server merges the
anonymous visitor (and their events) into the user. User ids must be 1-255 bytes and must not start
with `anon-`. Attribute names follow `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`; `optout_*`, `tags`, `points` and
`merged_into` are ignored by the server.

### Identity verification

With identity verification on, the server only accepts events for a known user when they carry that
user's hash, `hex(HMAC-SHA256(identity secret, user id))`. Compute it **on your server** and send it
to the app with the signed-in user. The SDK adds it to every event of the identified user
(`props.user_hash`; the server strips it), to push-token registrations, inbox and in-app requests. If
it is missing or wrong the server answers 403 and the SDK drops that batch (with a log line).

### Sign out

`Pulse.reset()` forgets the user id and hash, starts a new anonymous id and ends the session. Events
already queued are still sent under the id they were recorded with.

## Track events and screens

```kotlin
Pulse.track("order_completed", mapOf("order_id" to "A-1001", "total" to 49.90))
Pulse.screen("Checkout", mapOf("step" to 2))
```

Event names must match `^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`. Names the site-key API refuses also throw:
`unsubscribed`, `resubscribed`, `tag_added`, `tag_removed`, `points_changed`, `list_joined`,
`list_left`. Props hold JSON values (strings, numbers, booleans, null, lists, maps); `java.util.Date`
becomes an ISO 8601 UTC string, other objects their `toString()`.

## Device and session data

`identify`, `screen_view` and `session_start` carry these props (custom `track` events don't):

| Prop | Value | Source |
|---|---|---|
| `platform` | `android` | |
| `device_type` | `mobile` or `tablet` (smallest width 600 dp or more) | `Configuration.smallestScreenWidthDp` |
| `os` | `Android` | |
| `os_version` | e.g. `14` | `Build.VERSION.RELEASE`, or `PulseConfig.osVersion` |
| `locale` | BCP 47, e.g. `en-IN` | `Configuration` locale |
| `app_version` | e.g. `2.3.0` | the package's `versionName`, or `PulseConfig.appVersion` |
| `device_model` | e.g. `Pixel 8` | `Build.MODEL`, or `PulseConfig.deviceModel` |
| `device_brand` | e.g. `Google` | `Build.MANUFACTURER`, or `PulseConfig.deviceBrand` |

Unknown values are left out and each value is cut to 100 characters.

A session starts with the first event and again after **30 minutes** without events or in the
background. Each session begins with `session_start` `{session_id, ...device props}`, and every
`screen_view` carries `session_id`. The session id and last activity time are stored, so a restart
within 30 minutes continues the session. The SDK watches started activities: when the last one stops
it records the time and flushes; when one starts again after 30+ minutes a new session begins.

## Delivery, flushing and retries

Queued events are sent every `flushIntervalMillis` (10 s), as soon as `maxBatch` events are waiting,
right after `identify`, on `Pulse.flush()`, and when the app goes to the background. Each request
carries 1-50 events. On 429, 5xx or a network error the events stay queued and are retried with
exponential backoff (2 s doubling up to 5 min, plus up to 1 s of jitter); `flush()` always tries at
once. On 400 or 403 the batch is dropped and logged. The queue is saved in SharedPreferences.

## Push notifications

Pulse sends mobile push through **FCM HTTP v1**. Add Firebase Cloud Messaging to your app as usual
(the SDK has no compile-time Firebase dependency), then register the token:

```kotlin
class PushService : FirebaseMessagingService() {
    override fun onNewToken(token: String) {
        Pulse.registerPushToken(token)                     // PulsePlatform.ANDROID
    }
}

// At app start (after Pulse.init), when you have notification permission:
PulseFirebase.registerCurrentToken()                       // FirebaseMessaging.getInstance().token, by reflection
```

The SDK stores the token and registers it again after `identify`, `reset` and `optIn`, so the device
always belongs to the current person. `registerPushToken(token, platform, callback)` calls back
(main thread) with whether the server accepted it; registration is not queued, so call it again on
the next start if it failed.

### Open and dismissal tracking

Every Pulse push carries `pulse_track` in its data: a URL `{pulse}/t/p/{token}`. The SDK POSTs to
`pulse_track?a=clicked&platform=android` when the notification is tapped, and `a=dismissed` when it
is dismissed, which Pulse records as `push_clicked` / `push_dismissed` on the sending journey or
broadcast. Only http(s) URLs are used. Reports are stored and retried (429, 5xx, network errors) with
the same backoff; 4xx answers (404 for a bad token) drop them.

**Notification messages shown by FCM** (app in the background): tapping opens your launcher activity
with the data keys in the intent extras. Report it there:

```kotlin
class MainActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Pulse.handleNotificationIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        Pulse.handleNotificationIntent(intent)
    }
}
```

Android does not report dismissals of notifications FCM shows itself.

**Notifications you build** (data messages, or messages received in the foreground): add the
tracking URL to the tap intent, and a delete intent for dismissals:

```kotlin
override fun onMessageReceived(message: RemoteMessage) {
    val data = message.data
    val open = PulseNotifications.addClickTracking(
        Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP),
        data,
    )
    val notification = NotificationCompat.Builder(this, CHANNEL_ID)
        .setSmallIcon(R.drawable.ic_notification)
        .setContentTitle(message.notification?.title ?: data["title"])
        .setContentText(message.notification?.body ?: data["body"])
        .setAutoCancel(true)
        .setContentIntent(PendingIntent.getActivity(this, 0, open, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE))
        .setDeleteIntent(PulseNotifications.dismissIntent(this, data))   // null for non-Pulse pushes
        .build()
    NotificationManagerCompat.from(this).notify(message.messageId.hashCode(), notification)
}
```

You can also call `Pulse.trackPushClicked(message.data)` / `Pulse.trackPushDismissed(message.data)`
yourself, and `PulseNotifications.isPulsePush(data)` / `trackUrl(data)` read `pulse_track`.

## App inbox

```kotlin
Pulse.inbox.list { result ->                  // main thread
    result.onSuccess { page -> show(page.messages, page.unread) }
          .onFailure { e -> /* PulseException: user hash missing or refused */ }
}
Pulse.inbox.list(before = page.messages.last().id) { ... }   // older page, when page.hasMore
Pulse.inbox.markRead(listOf(message.id))       // markRead() marks everything read
Pulse.inbox.click(message.id)                  // a click in the sender's report; open message.url yourself
Pulse.inbox.delete(listOf(message.id))         // delete() deletes EVERYTHING; delete(emptyList()) does nothing
val handle = Pulse.inbox.addUnreadListener { count -> badge.text = "$count" }
```

`listBlocking`, `markReadBlocking`, `deleteBlocking` and `clickBlocking` do the same on the calling
thread (not the main thread). `PulseInboxMessage` has `id`, `messageId`, `title`, `body`, `imageUrl`,
`url`, `category`, `data`, `createdAt`, `readAt` / `isRead` and `expiresAt`. `Pulse.inbox.unreadCount`
is the count from the last `list`, lowered by `markRead` and `delete`, and reset by `identify`/`reset`.

**User hash required for signed-in users.** Inboxes are private: for a signed-in user the server
only answers when the request carries their user hash, even if the workspace does not require
identity verification for events. Without it the calls fail with `PulseException` (status 403, or
before sending when no hash is set). Anonymous people need none. Network errors, 429 and 5xx don't
fail: `list` returns an empty page and the actions return 0 / false, with a log line.

## In-app messages

In-app campaigns (modal, banner or full screen) are created in Pulse. The SDK fetches the current
person's campaigns at `init`, after `identify`, `reset` and `optIn`, and when the app returns to the
foreground (at most every 15 minutes). When the app calls `track(name)` or `screen(name)`, the first
campaign whose `trigger_event` is `name` (and, for `screen_view`, whose `screen` matches when set)
and whose frequency allows is shown after its `delay_seconds`:

| Frequency | Shown |
|---|---|
| `once` | once on this device, ever (stored) |
| `session` | once per app run |
| `daily` | once per calendar day (device time) |
| `always` | every time the trigger happens |

The SDK never sends `app_open` itself: track it if you have campaigns triggered by it. One message is
shown at a time, over the top activity, by `PulseInAppActivity`: **modal** (a card over a dimmed
background with image, title, body, buttons and a close button), **banner** (top or bottom; hides
itself after 8 s; a tap follows its first `url` button) and **fullscreen**. The campaign's
`background`, `text_color` and `button_color` are used when set. A `url` button calls
`PulseConfig.onInAppAction(value)` on the main thread and closes the message; a `dismiss` button,
the close button, back or a tap outside closes it. When no activity is in the foreground nothing is
shown and the frequency is not used up.

Recorded through the event queue: `inapp_shown` `{inapp_id}`, `inapp_clicked` `{inapp_id, button,
url}` and `inapp_dismissed` `{inapp_id}` (a banner that times out records nothing).
`Pulse.inApp.messages` lists the cached campaigns and `Pulse.inApp.refresh()` fetches them now.
Turn the feature off with `PulseConfig(inAppMessages = false)`. To draw messages yourself, build a
`PulseClient` with your own `InAppPresenter` in `PulseEnvironment`.

## Consent (opt-out)

```kotlin
Pulse.optOut()        // stop tracking on this device
Pulse.optIn()         // resume
Pulse.isOptedOut
```

`optOut()` deletes queued events and push reports, and from then on nothing is recorded or sent:
no events, push-token registrations, push reports, inbox or in-app requests (inbox calls return an
empty page). The flag is stored (`pulse.<siteKey>.optout`) and survives restarts. `identify` and
`reset` still update the ids locally, so the right person is used after `optIn()`, but an
anonymous-to-user merge that happened while opted out is not sent. `optIn()` registers the stored
push token again and fetches in-app campaigns.

This is local tracking consent. It does not unsubscribe the person from channels (that is done on
the server, for example from an unsubscribe link), and Pulse can still send pushes to a registered
token: to stop push too, delete the FCM token (`FirebaseMessaging.getInstance().deleteToken()`).

## Storage

State lives in the SharedPreferences file `pulse_sdk`, under keys namespaced by site key so two
workspaces in one app never share ids: `pulse.<siteKey>.aid` (anonymous id), `.uid`, `.uh` (user
hash), `.queue`, `.push` (token), `.sid`, `.sla` (last activity), `.optout`, `.inapp` (in-app
frequency state) and `.ptrack` (pending push reports).

## Testing

`pulse-core` runs on the JVM. Build a `PulseClient` with a `PulseEnvironment` holding
`InMemoryStorage`, a fake `PulseHttpClient`, a `Dispatcher` that runs tasks inline (and lets the test
run scheduled ones), and a `clock`. See `pulse-core/src/test/kotlin/io/pulse/sdk/Support.kt`. The
unit tests never touch the network.

```
cd sdks/android
gradle :pulse-core:test
# or, without a local JDK/Gradle:
docker run --rm -u $(id -u):$(id -g) -v "$PWD":/w -w /w -e GRADLE_USER_HOME=/w/.gradle \
  gradle:8-jdk17 gradle :pulse-core:test --no-daemon
```

To build `pulse-android` without a local Android SDK, use an image that has one (it has no Gradle,
so mount the one from `gradle:8-jdk17`):

```
docker run --rm -u $(id -u):$(id -g) -v /tmp/gradle-dist:/out gradle:8-jdk17 cp -r /opt/gradle/. /out/
docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -v "$PWD":/w -v /tmp/gradle-dist:/opt/gradle:ro -w /w \
  -e GRADLE_USER_HOME=/w/.gradle ghcr.io/cirruslabs/android-sdk:34 \
  /opt/gradle/bin/gradle :pulse-android:assembleRelease :pulse-android:lintRelease --no-daemon
```

## Server API used

| Call | Endpoint |
|---|---|
| events | `POST {host}/v1/web/events`, header `X-Pulse-Site-Key`, body `{"events":[{"id","external_id","name","ts","props","attrs","email"}]}` → 202 |
| push token | `POST {host}/v1/web/devices`, header `X-Pulse-Site-Key`, body `{"external_id","token","platform":"android"\|"ios","user_hash"}` → 204 |
| inbox | `GET {host}/v1/web/inbox?id=&user_hash=&before=`, header `X-Pulse-Site-Key` → `{"messages":[...],"unread"}` |
| inbox actions | `POST {host}/v1/web/inbox`, body `{"id","user_hash","action":"read"\|"delete"\|"click","message_ids":[...]}` → `{"changed"}` |
| in-app | `POST {host}/v1/web/inapp`, body `{"id","user_hash","platform":"android"}` → `{"messages":[{"id","layout","content","trigger","end_at"}]}` |
| push open / dismissal | `POST {pulse_track}?a=clicked\|dismissed&platform=android`, no body, no site key → 204 (404 bad token, 400 bad action) |

## What was verified

- `pulse-core` was compiled and its unit tests run in Docker with
  `docker run --rm -u $(id -u):$(id -g) -v $PWD:/w -w /w -e GRADLE_USER_HOME=/w/.gradle gradle:8-jdk17 gradle :pulse-core:test --no-daemon`
  (Gradle 8, JDK 17, Kotlin 2.0.21): 44 tests, all passing. They cover the anonymous id and its
  site-key namespace, the identify merge prop, validation, batching, persistence across restarts,
  backoff and drop rules, sessions, device props, opt-out, push token registration, push report URLs
  and retries, inbox parsing and the user-hash rule, and in-app frequency rules.
- `pulse-android` was compiled and linted, not run: `gradle :pulse-android:assembleRelease
  :pulse-android:lintRelease` (Android Gradle plugin 8.5.2, compileSdk 34) inside the
  `ghcr.io/cirruslabs/android-sdk:34` image, with Gradle copied from `gradle:8-jdk17`; it built
  and lint found no issues. There are no unit or instrumented tests for it, and it was not run on a
  device or emulator: the `Pulse` object, SharedPreferences storage, lifecycle handling, device
  details, notification helpers, the `PulseFirebase` reflection and `PulseInAppActivity` are
  untested at runtime.
- Nothing was tested against a running Pulse server.
