# 5. Mobile SDKs

Pulse has three app SDKs. They share one design with the [web SDK](04-web-sdk.md): people are
anonymous (`anon-` + 24 hex characters) until `identify`, `identify` merges the anonymous visitor
into the user, events are grouped into sessions, queued on the device (they survive restarts) and
sent in batches to the same browser API (`/v1/web/*`) with the workspace's public **site key**.

| SDK | Where | Package | Full reference |
|---|---|---|---|
| Flutter | `sdks/flutter/pulse_sdk` | `pulse_sdk` (Dart; depends on `http`, `shared_preferences`) | [README](../../sdks/flutter/pulse_sdk/README.md) |
| Android (Kotlin) | `sdks/android` | `pulse-core` (plain Kotlin/JVM) + `pulse-android` (minSdk 21) | [README](../../sdks/android/README.md) |
| iOS (Swift) | `sdks/ios` | Swift package: `PulseCore` + `Pulse` (iOS 14+) | [README](../../sdks/ios/README.md) |

This page is the overview: how to install and start each SDK, identify, track, push and the inbox
and in-app features, side by side. Each SDK's README is the complete reference (every option,
storage keys, threading, testing).

**Never put the server API key or the identity verification secret in an app.** Apps are
decompiled; the site key is public by design.

---

## What has been tested

Read this before shipping. From the SDK READMEs and [the feature parity notes](../05-feature-parity.md#known-gaps):

| | Flutter | Android | iOS |
|---|---|---|---|
| Core logic (queue, batching, backoff, sessions, validation, inbox, in-app rules, push tracking) | Unit tests with `flutter test` (mock HTTP client, in-memory storage) | `pulse-core`: 44 JVM unit tests, passing (Gradle 8, JDK 17, in Docker) | `PulseCore`: 33 tests, passing (Swift 5.10 on Linux, in Docker) |
| Platform layer (lifecycle, storage, device details, notifications, in-app UI) | Widget tests for the inbox view and in-app messages | **Compiled and linted only** (`assembleRelease`, `lintRelease`); never run on a device or emulator | **Never compiled**: the UIKit / UserNotifications code needs Xcode, which wasn't available. Expect small compile fixes the first time |
| Against a running Pulse server | Optional integration test (`tool/integration_test.dart`, needs `PULSE_SITE_KEY` and `PULSE_HOST`) | No | No |
| Push open / dismissal tracking | **Not implemented** (taps aren't reported) | Yes | Yes |
| Local opt-out (`optOut()`) | **Not implemented** | Yes | Yes |
| Install from a package registry | No: path or git dependency | No: include the modules, or `publishToMavenLocal` | No: local or git Swift package |

The server side these SDKs call is covered by Pulse's end-to-end tests (events, device
registration for `android` and `ios`, inbox, in-app), so what's unverified is the SDK code running
on real devices, not the API it talks to.

---

## Before you start

1. **Site key**: Settings → Sign-in & keys → Keys (or `GET /v1/auth/me` → `workspace.site_key`).
2. **Host**: your Pulse's public address (`PULSE_PUBLIC_URL`, or `PULSE_PAGES_URL` when set; both
   serve `/v1/web/*`). An Android emulator reaches a Pulse on your machine at
   `http://10.0.2.2:7080`; plain `http` needs a network security config on Android and an ATS
   exception on iOS, so use an `https` address for anything but local testing.
3. **Push** (optional): a Firebase project. Pulse sends mobile push through **FCM HTTP v1** on both
   platforms (on iOS, FCM delivers through APNs with the APNs key you upload to Firebase). Add the
   provider in **Settings → Channels → Push → Add provider → Firebase Cloud Messaging** with the
   Firebase project ID and a service account key (JSON).
4. **Identity verification** (recommended): your backend computes
   `hex(HMAC-SHA256(identity secret, user id))` for the signed-in user and returns it to the app,
   for example in the login response. See [Web SDK, identity verification](04-web-sdk.md#identity-verification)
   for the secret and server-side code. The app inbox of a signed-in user always needs this hash.

---

## Install and start

**Flutter**

```yaml
# pubspec.yaml
dependencies:
  pulse_sdk:
    path: ../pulse/sdks/flutter/pulse_sdk   # or a git dependency
```

```dart
import 'package:pulse_sdk/pulse_sdk.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await Pulse.init(PulseConfig(
    host: 'https://pulse.example.com',
    siteKey: 'sk_...',
    appVersion: '2.3.0',          // optional; the SDK has no plugin dependencies to read it itself
  ));
  runApp(const MyApp());
}
// then: Pulse.instance.track(...)
```

**Android**

```kotlin
// settings.gradle.kts
include(":pulse-core", ":pulse-android")
project(":pulse-core").projectDir = file("../pulse/sdks/android/pulse-core")
project(":pulse-android").projectDir = file("../pulse/sdks/android/pulse-android")
// app/build.gradle.kts: implementation(project(":pulse-android"))

class App : Application() {
    override fun onCreate() {
        super.onCreate()
        Pulse.init(this, PulseConfig(
            host = "https://pulse.example.com",
            siteKey = "sk_...",
            debug = BuildConfig.DEBUG,
            onInAppAction = { url -> openLink(url) },
        ))
    }
}
```

**iOS**

```swift
// Package.swift: .package(path: "../pulse/sdks/ios"), product "Pulse"
import Pulse

func application(_ application: UIApplication,
                 didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
    let config = try! PulseConfig(host: "https://pulse.example.com", siteKey: "sk_...",
                                  onInAppAction: { url in if let u = URL(string: url) { UIApplication.shared.open(u) } })
    PulseClient.start(config)          // main thread; then use PulseClient.shared
    return true
}
```

Common options in all three: `flushInterval` (10 s), `maxBatch` (1-50 events per request),
`maxQueue` (1,000 events kept offline; the oldest are dropped beyond that), `flushOnBackground`
(true), `requestTimeout` (30 s), `inAppMessages` (true), `debug`, and overrides for `appVersion`,
`deviceModel`, `deviceBrand`, `osVersion`.

---

## Identify, track, reset

| | Flutter | Android | iOS |
|---|---|---|---|
| Identify | `Pulse.instance.identify(id, attributes: {...}, email: e, userHash: h)` | `Pulse.identify(id, attributes = mapOf(...), email = e, userHash = h)` | `try PulseClient.shared?.identify(id, attributes: [...], email: e, userHash: h)` |
| Event | `Pulse.instance.track('order_completed', {'total': 49.9})` | `Pulse.track("order_completed", mapOf("total" to 49.9))` | `try? PulseClient.shared?.track("order_completed", properties: ["total": 49.9])` |
| Screen | `Pulse.instance.screen('Checkout', {...})` | `Pulse.screen("Checkout", mapOf(...))` | `try? PulseClient.shared?.screen("Checkout", properties: [...])` |
| Sign out | `Pulse.instance.reset()` | `Pulse.reset()` | `PulseClient.shared?.reset()` |
| Send now | `Pulse.instance.flush()` | `Pulse.flush()` | `PulseClient.shared?.flush(completion:)` |
| Current ID | `Pulse.instance.currentId` | `Pulse.currentId` | `PulseClient.shared?.currentID` |

- Call `identify` at sign-in and on app start while signed in. The first `identify` after an
  anonymous period sends `props.anonymous_id`, so the server merges the visitor into the user.
  User IDs: 1-255 bytes, not starting with `anon-`.
- `screen` records `screen_view` with `props.screen`. `identify`, `screen_view` and `session_start`
  carry device properties (`platform`, `device_type`, `os`, `os_version`, `locale`, `app_version`,
  `device_model`, `device_brand`), which Pulse keeps as the contact's last-seen device.
- A session starts with the first event and again after 30 minutes without events or in the
  background, with a `session_start` event.
- Event names: `^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`. Names the site key can't send throw at once:
  `unsubscribed`, `resubscribed`, `tag_added`, `tag_removed`, `points_changed`, `list_joined`,
  `list_left`. `optout_*`, `tags`, `points` and `merged_into` attributes are dropped by the server.
- **Delivery**: every flush interval, when a batch is full, right after `identify`, on `flush()`
  and when the app goes to the background. On 429, 5xx or a network error events stay queued and are
  retried with backoff (2 s doubling to 5 min); on 400 or 403 (identity verification failed) the
  batch is dropped with a log line.

---

## Push

Get the FCM registration token in your app (none of the SDKs depends on Firebase at compile time)
and register it. The SDK remembers the token and registers it again after `identify` and `reset`
(and `optIn` on Android and iOS), so the device always belongs to the current person.

```dart
// Flutter (with firebase_messaging)
final token = await FirebaseMessaging.instance.getToken();
if (token != null) {
  await Pulse.instance.registerPushToken(token, platform: Platform.isIOS ? PushPlatform.ios : PushPlatform.android);
}
FirebaseMessaging.instance.onTokenRefresh.listen((t) => Pulse.instance.registerPushToken(t, platform: ...));
```

```kotlin
// Android
class PushService : FirebaseMessagingService() {
    override fun onNewToken(token: String) = Pulse.registerPushToken(token)
}
PulseFirebase.registerCurrentToken()     // at start, after Pulse.init (reads the token by reflection)
```

```swift
// iOS: register the FCM token, not the raw APNs device token
func messaging(_ messaging: Messaging, didReceiveRegistrationToken fcmToken: String?) {
    if let token = fcmToken { PulseClient.shared?.registerPushToken(token) }
}
```

Registration is sent at once (`POST /v1/web/devices`, `{"external_id","token","platform","user_hash"}`
→ 204), not queued: if it fails, register again on the next start. Pulse deletes a token FCM reports
as unregistered.

### Tap and dismissal tracking (Android and iOS)

Every Pulse push carries `pulse_track` in its data: a URL `{pulse}/t/p/{token}`. The SDKs POST
`?a=clicked&platform=…` when the notification is opened and `?a=dismissed` when it's dismissed;
Pulse records `push_clicked` / `push_dismissed` on the sending journey or broadcast, for reports and
for journey decisions waiting for a click.

- **Android**: call `Pulse.handleNotificationIntent(intent)` in your launcher activity's `onCreate`
  and `onNewIntent` (notifications FCM shows itself). For notifications you build, wrap the tap
  intent with `PulseNotifications.addClickTracking(intent, data)` and set
  `PulseNotifications.dismissIntent(context, data)` as the delete intent.
- **iOS**: call `PulseClient.shared?.handleNotificationResponse(response)` in your
  `UNUserNotificationCenterDelegate`, or set `UNUserNotificationCenter.current().delegate =
  PulseNotificationDelegate.shared`. Dismissals are reported only for a category registered with
  `.customDismissAction` (`PulseNotificationCategory.trackingDismissals(identifier: "pulse")`) that
  the push names.
- **Flutter**: not reported. If you need it, POST to the `pulse_track` URL from the data yourself
  (`?a=clicked&platform=android|ios`, no body, no key).

---

## App inbox

Messages sent to the **inbox** channel (by journeys' `send_inbox` and inbox broadcasts) are kept on
the server per person.

| | Flutter | Android | iOS |
|---|---|---|---|
| List (newest 50) | `await Pulse.instance.inbox.list()` / `list(before: id)` | `Pulse.inbox.list { result -> }` / `list(before = id) {}` | `try await inbox.list()` / `list(before:)` |
| Mark read | `inbox.markRead([id])`; `markRead()` = all | `Pulse.inbox.markRead(listOf(id))` | `try await inbox.markRead([id])` |
| Click (counts in reports) | `inbox.click(id)` | `Pulse.inbox.click(id)` | `try await inbox.click(id)` |
| Delete | `inbox.delete([id])`; `delete()` = **all** | `Pulse.inbox.delete(listOf(id))` | `try await inbox.delete([id])` |
| Unread badge | `inbox.unread` (`ValueListenable<int>`) | `Pulse.inbox.addUnreadListener { }` | `inbox.addUnreadListener { }` |
| Ready-made UI | `PulseInboxView` | | |

Reads count as opens and clicks as clicks in the sender's report. **A signed-in user's inbox needs
their user hash** (pass `userHash` to `identify`), even if the workspace doesn't require
verification for events: without it the calls fail with a 403 error (`PulseException` /
`PulseError.userHashRequired`). Anonymous people need none.

## In-app messages

In-app campaigns (modal, banner, full screen) are created in **Campaigns → On-site & in-app → New →
Mobile app**. The SDK fetches the person's campaigns (`POST /v1/web/inapp`) at start, after
`identify` and `reset`, and on return to the foreground (at most every 15 minutes), and shows one
when `track(name)` or `screen(name)` matches its trigger, after its delay, within its frequency
(`once` per device, `session`, `daily`, `always`).

- **Flutter**: `Pulse.instance.attachNavigator(navigatorKey)` is required, and `onInAppAction`
  handles `url` buttons.
- **Android**: shown by `PulseInAppActivity` over the top activity; `onInAppAction` in `PulseConfig`.
- **iOS**: modal and full screen over the top view controller, banners on the key window;
  `onInAppAction` in `PulseConfig`.

The SDKs record `inapp_shown`, `inapp_clicked` and `inapp_dismissed` (`props.inapp_id`). The SDKs
never send `app_open` themselves: track it if a campaign is triggered by it. Segment targeting
applies to signed-in users only with a valid user hash. To try a campaign on one device, use **Try it
with a contact → Send a test** on the campaign (`POST /v1/inapp/{id}/test` with `external_id`): the
app signed in as that contact (with its user hash) shows it once on its next fetch, whatever the
targeting.

## Opting out (Android and iOS)

`optOut()` stops everything on the device: queued events and push reports are deleted, and nothing
is recorded or sent (events, token registrations, push reports, inbox and in-app calls) until
`optIn()`. The flag is stored per site key and survives restarts. It is **local tracking consent**:
it doesn't unsubscribe the person from channels (do that on the server or with an unsubscribe link),
and Pulse can still push to a token registered earlier; delete the FCM token too to stop push. The
Flutter SDK has no opt-out: don't call `Pulse.init` until the person agrees, if you need consent
first.

---

## Debugging

- Turn on `debug` in the config: every step is logged (`debugPrint`, Logcat tag `Pulse`, or the
  console on iOS).
- Watch the contact in **Audience → Contacts**: events appear on the timeline within seconds.
  `is:anonymous` finds a fresh install before `identify`.
- A 403 in the log means identity verification refused the user hash: compare it with
  `hex(HMAC-SHA256(secret, userId))` on your server, with the current secret.
- A 400 means the server refused the batch (an invalid event name or a server-only event).
- Sends to a device appear in Settings → Test inbox when the workspace's push provider is the
  sandbox, so you can check journeys before Firebase is set up.

Next: [6. Webhooks and integrations](06-webhooks-and-integrations.md).
