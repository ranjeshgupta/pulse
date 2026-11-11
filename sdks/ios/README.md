# Pulse iOS SDK

Send events from an iOS app to [Pulse](../../README.md): track events and screens, identify signed-in
users (with identity verification), register push tokens, report push opens and dismissals, show the
app inbox and in-app messages, and let people opt out of tracking.

It mirrors the [Flutter SDK](../flutter/pulse_sdk/README.md) and the web SDK: people are anonymous
(`anon-` + 24 hex characters) until `identify`, and `identify` merges the anonymous visitor into the
user. Events are grouped into sessions, queued on the device, persisted (they survive app restarts)
and sent in batches.

The SDK only uses the workspace's public **site key**. Never put an API key or the identity
verification secret in an app.

A Swift package with two libraries:

| Target | What it is |
|---|---|
| `PulseCore` | Foundation only (builds on Linux too): queue, batching and backoff, sessions, validation, JSON, HTTP (`URLSession` behind `PulseHTTPClient`), storage (`UserDefaultsStorage`, `InMemoryStorage`), inbox, in-app logic, push tracking, opt-out. |
| `Pulse` | Re-exports `PulseCore` and adds, behind `#if canImport(UIKit)` / `#if canImport(UserNotifications)`: `PulseClient.start(_:)` with app lifecycle observation, device details from `UIDevice` and the bundle, a `UNUserNotificationCenterDelegate` helper for opens and dismissals, and in-app messages drawn with UIKit. |

iOS 14 or later. No third-party dependencies.

## Install

Swift Package Manager. In Xcode: File → Add Package Dependencies → Add Local… and pick
`sdks/ios`, or in a `Package.swift`:

```swift
dependencies: [
    .package(path: "../pulse/sdks/ios"),     // or a git URL once the repository is published
],
targets: [
    .target(name: "MyApp", dependencies: [.product(name: "Pulse", package: "Pulse")]),
]
```

## Initialise

```swift
import Pulse

@main
class AppDelegate: UIResponder, UIApplicationDelegate {
    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        let config = try! PulseConfig(
            host: "https://pulse.example.com",
            siteKey: "sk_...",                 // Settings → Sign-in & keys → Your website in Pulse
            flushInterval: 10,
            maxBatch: 50,                      // events per request, 1-50
            debug: false,                      // true prints every step
            onInAppAction: { url in            // an in-app "url" button was tapped
                if let u = URL(string: url) { UIApplication.shared.open(u) }
            }
        )
        PulseClient.start(config)
        return true
    }
}
```

Other options: `maxQueue` (default 1,000 events kept while offline; the oldest are dropped beyond
that), `flushOnBackground` (default true), `requestTimeout` (default 30 s), `inAppMessages` (default
true), and `appVersion`, `deviceModel`, `deviceBrand`, `osVersion` to override what the SDK reads.
`PulseConfig.init` throws `PulseError.invalidArgument` for an empty host or site key, or `maxBatch`
outside 1-50.

`start` must be called on the main thread. Afterwards use `PulseClient.shared` (an optional) from any
thread; network work runs on the SDK's serial queue, and callbacks come back on the main queue.

## API

| Call | What it does |
|---|---|
| `PulseClient.start(config)` | Starts the SDK on iOS and sets `PulseClient.shared`. |
| `try track(_ name:, properties:)` | Records an event. Throws `PulseError.invalidArgument` for a bad or reserved name. |
| `try screen(_ name:, properties:)` | Records `screen_view` with `props.screen = name`, `session_id` and device props. |
| `try identify(_ userID:, attributes:, email:, userHash:)` | Ties the install to your user id. |
| `reset()` | Sign-out: new anonymous id, session ends. |
| `flush(completion:)` / `flushAndWait()` | Sends queued events now (on the SDK queue / on the calling thread). |
| `registerPushToken(_:platform:completion:)` | Registers the FCM registration token (`.ios` by default). |
| `trackPushClicked(userInfo:)` / `trackPushDismissed(userInfo:)` | Reports a push open / dismissal. |
| `handleNotificationResponse(_:)` | Reports the open or dismissal from a `UNNotificationResponse`. |
| `optOut()` / `optIn()` / `isOptedOut` | Local tracking consent. |
| `inbox` | The app inbox (`PulseInbox`). |
| `inApp` | In-app campaigns (`PulseInApp`). |
| `currentID`, `anonymousID`, `isIdentified`, `sessionID`, `pendingEvents`, `deviceProperties`, `pushToken`, `pendingPushReports` | State. |
| `appDidEnterBackground()` / `appWillEnterForeground()` | Lifecycle hooks; `start` calls them for you. |

## Identify users

```swift
try PulseClient.shared?.identify(
    user.id,
    attributes: ["first_name": user.firstName, "plan": user.plan],
    email: user.email,
    userHash: user.pulseHash          // from your server, see below
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
(`props.user_hash`; the server strips it), push-token registrations, inbox and in-app requests. If it
is missing or wrong the server answers 403 and the SDK drops that batch (with a log line).

### Sign out

`PulseClient.shared?.reset()` forgets the user id and hash, starts a new anonymous id and ends the
session. Events already queued are still sent under the id they were recorded with.

## Track events and screens

```swift
try? PulseClient.shared?.track("order_completed", properties: ["order_id": "A-1001", "total": 49.90])
try? PulseClient.shared?.screen("Checkout", properties: ["step": 2])
```

Event names must match `^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`. Names the site-key API refuses also throw:
`unsubscribed`, `resubscribed`, `tag_added`, `tag_removed`, `points_changed`, `list_joined`,
`list_left`. Properties hold JSON values (strings, numbers, booleans, `NSNull`, arrays, dictionaries);
`Date` becomes an ISO 8601 UTC string, `URL` its string, other values their description.

## Device and session data

`identify`, `screen_view` and `session_start` carry these props (custom `track` events don't):

| Prop | Value | Source |
|---|---|---|
| `platform` | `ios` | |
| `device_type` | `mobile` or `tablet` | `UIDevice.userInterfaceIdiom` (`.pad` is tablet) |
| `os` | `iOS` | |
| `os_version` | e.g. `17.4` | `UIDevice.systemVersion`, or `PulseConfig.osVersion` |
| `locale` | BCP 47, e.g. `en-IN` | `Locale.preferredLanguages.first` |
| `app_version` | e.g. `2.3.0` | `CFBundleShortVersionString`, or `PulseConfig.appVersion` |
| `device_model` | e.g. `iPhone15,2` | the hardware id (`uname`), or `PulseConfig.deviceModel` |
| `device_brand` | `Apple` | or `PulseConfig.deviceBrand` |

Unknown values are left out and each value is cut to 100 characters.

A session starts with the first event and again after **30 minutes** without events or in the
background. Each session begins with `session_start` `{session_id, ...device props}`, and every
`screen_view` carries `session_id`. The session id and last activity time are stored, so a restart
within 30 minutes continues the session. `start` observes `didEnterBackground` (records the time and
flushes, inside a background task) and `willEnterForeground` (starts a new session after 30+
minutes, refreshes in-app campaigns).

## Delivery, flushing and retries

Queued events are sent every `flushInterval` (10 s), as soon as `maxBatch` events are waiting, right
after `identify`, on `flush()`, and when the app goes to the background. Each request carries 1-50
events. On 429, 5xx or a network error the events stay queued and are retried with exponential
backoff (2 s doubling up to 5 min, plus up to 1 s of jitter); `flush()` always tries at once. On 400
or 403 the batch is dropped and logged. The queue is saved in `UserDefaults`.

## Push notifications

Pulse sends mobile push through **FCM HTTP v1** on both platforms. On iOS, add Firebase Messaging to
the app and upload your APNs key in the Firebase console; FCM then delivers to APNs. Register the
**FCM registration token**, not the raw APNs device token:

```swift
import FirebaseCore
import FirebaseMessaging
import Pulse

extension AppDelegate: MessagingDelegate {
    func messaging(_ messaging: Messaging, didReceiveRegistrationToken fcmToken: String?) {
        if let token = fcmToken {
            PulseClient.shared?.registerPushToken(token)     // platform .ios
        }
    }
}

// In didFinishLaunching, after PulseClient.start:
FirebaseApp.configure()
Messaging.messaging().delegate = self
UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound]) { _, _ in }
application.registerForRemoteNotifications()
```

The SDK stores the token and registers it again after `identify`, `reset` and `optIn`, so the device
always belongs to the current person. `completion` (main queue) gets whether the server accepted it;
registration is not queued, so call it again on the next start if it failed.

### Open and dismissal tracking

Every Pulse push carries `pulse_track` in its data (FCM puts data keys at the top level of the APNs
`userInfo`; a nested `data` dictionary is read too): a URL `{pulse}/t/p/{token}`. The SDK POSTs to
`pulse_track?a=clicked&platform=ios` when the notification is tapped, and `a=dismissed` when it is
dismissed, which Pulse records as `push_clicked` / `push_dismissed` on the sending journey or
broadcast. Only http(s) URLs are used. Reports are stored and retried (429, 5xx, network errors) with
the same backoff; 4xx answers (404 for a bad token) drop them.

If you have your own `UNUserNotificationCenterDelegate`, report the response there:

```swift
func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                            withCompletionHandler completionHandler: @escaping () -> Void) {
    PulseClient.shared?.handleNotificationResponse(response)   // tap → clicked, dismiss → dismissed
    // ... your own handling (deep links etc.)
    completionHandler()
}
```

Otherwise install the SDK's delegate (it forwards to `next` if you set one, and shows notifications
while the app is in the foreground):

```swift
UNUserNotificationCenter.current().delegate = PulseNotificationDelegate.shared
```

iOS only reports dismissals for notifications whose category was registered with
`.customDismissAction`:

```swift
UNUserNotificationCenter.current().setNotificationCategories([
    PulseNotificationCategory.trackingDismissals(identifier: "pulse"),
])
```

and the push must name that category (`apns.payload.aps.category = "pulse"`). Without it only opens
are reported. `trackPushClicked(userInfo:)`, `trackPushDismissed(userInfo:)` and
`PulsePush.trackURL(from:)` work with any `userInfo` dictionary if you handle notifications yourself.

## App inbox

```swift
guard let inbox = PulseClient.shared?.inbox else { return }
let page = try await inbox.list()                         // newest 50 messages, and page.unread
if page.hasMore { _ = try await inbox.list(before: page.messages.last!.id) }
try await inbox.markRead([message.id])                    // markRead() marks everything read
try await inbox.click(message.id)                         // a click in the sender's report; open message.url yourself
try await inbox.delete([message.id])                      // delete() deletes EVERYTHING; delete([]) does nothing
inbox.addUnreadListener { count in badge.text = "\(count)" }   // main queue
```

`listBlocking`, `markReadBlocking`, `deleteBlocking` and `clickBlocking` do the same on the calling
thread (not the main thread). `PulseInboxMessage` has `id`, `messageID`, `title`, `body`, `imageURL`,
`url`, `category`, `data`, `createdAt`, `readAt` / `isRead` and `expiresAt`. `inbox.unreadCount` is
the count from the last `list`, lowered by `markRead` and `delete`, and reset by `identify`/`reset`.

**User hash required for signed-in users.** Inboxes are private: for a signed-in user the server only
answers when the request carries their user hash, even if the workspace does not require identity
verification for events. Without it the calls throw `PulseError.userHashRequired` (status 403, or nil
when the SDK refused before sending). Anonymous people need none. Network errors, 429 and 5xx don't
throw: `list` returns an empty page and the actions return 0 / false, with a log line.

## In-app messages

In-app campaigns (modal, banner or full screen) are created in Pulse (Campaigns → On-site & in-app →
New → Mobile app). The SDK fetches the current
person's campaigns at `start`, after `identify`, `reset` and `optIn`, and when the app returns to the
foreground (at most every 15 minutes). When the app calls `track(name)` or `screen(name)`, the first
campaign whose `trigger_event` is `name` (and, for `screen_view`, whose `screen` matches when set) and
whose frequency allows is shown after its `delay_seconds`:

| Frequency | Shown |
|---|---|
| `once` | once on this device, ever (stored) |
| `session` | once per app run |
| `daily` | once per calendar day (device time) |
| `always` | every time the trigger happens |

The SDK never sends `app_open` itself: track it if you have campaigns triggered by it. One message is
shown at a time: **modal** (a card over a dimmed background with image, title, body, buttons and a
close button) and **fullscreen** are presented over the top view controller; a **banner** is added to
the key window (top or bottom; hides itself after 8 s; a tap follows its first `url` button). The
campaign's `background`, `text_color` and `button_color` are used when set, otherwise system colours.
A `url` button calls `onInAppAction(value)` on the main queue and closes the message; a `dismiss`
button, the close button or a tap outside closes it. While the app is not active nothing is shown
and the frequency is not used up.

Recorded through the event queue: `inapp_shown` `{inapp_id}`, `inapp_clicked` `{inapp_id, button,
url}` and `inapp_dismissed` `{inapp_id}` (a banner that times out records nothing).
`inApp.messages` lists the cached campaigns and `inApp.refresh()` fetches them now. Turn the feature
off with `inAppMessages: false`. To draw messages yourself, create the client with
`PulseClient.configure(_:environment:)` and your own `PulseInAppPresenter`.

## Consent (opt-out)

```swift
PulseClient.shared?.optOut()      // stop tracking on this device
PulseClient.shared?.optIn()       // resume
PulseClient.shared?.isOptedOut
```

`optOut()` deletes queued events and push reports, and from then on nothing is recorded or sent: no
events, push-token registrations, push reports, inbox or in-app requests (inbox calls return an
empty page). The flag is stored (`pulse.<siteKey>.optout`) and survives restarts. `identify` and
`reset` still update the ids locally, so the right person is used after `optIn()`, but an
anonymous-to-user merge that happened while opted out is not sent. `optIn()` registers the stored
push token again and fetches in-app campaigns.

This is local tracking consent. It does not unsubscribe the person from channels (that is done on
the server, for example from an unsubscribe link), and Pulse can still send pushes to a registered
token: to stop push too, delete the FCM token (`Messaging.messaging().deleteToken`).

## Storage

State lives in `UserDefaults.standard` under keys namespaced by site key, so two workspaces in one
app never share ids: `pulse.<siteKey>.aid` (anonymous id), `.uid`, `.uh` (user hash), `.queue`,
`.push` (token), `.sid`, `.sla` (last activity), `.optout`, `.inapp` (in-app frequency state) and
`.ptrack` (pending push reports).

## Testing

`PulseCore` builds without UIKit. Create a `PulseClient(config:environment:)` with a
`PulseEnvironment` holding `InMemoryStorage`, a fake `PulseHTTPClient`, a `PulseDispatcher` that runs
work inline (and runs scheduled work when the test says so), a `clock` and `callback: { $0() }`. See
`Tests/PulseCoreTests/Support.swift`. The tests never touch the network.

```
cd sdks/ios
swift test
# or on Linux, without a local Swift toolchain:
docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -v "$PWD":/w -w /w swift:5.10 swift test
```

## Server API used

| Call | Endpoint |
|---|---|
| events | `POST {host}/v1/web/events`, header `X-Pulse-Site-Key`, body `{"events":[{"id","external_id","name","ts","props","attrs","email"}]}` → 202 |
| push token | `POST {host}/v1/web/devices`, header `X-Pulse-Site-Key`, body `{"external_id","token","platform":"ios"\|"android","user_hash"}` → 204 |
| inbox | `GET {host}/v1/web/inbox?id=&user_hash=&before=`, header `X-Pulse-Site-Key` → `{"messages":[...],"unread"}` |
| inbox actions | `POST {host}/v1/web/inbox`, body `{"id","user_hash","action":"read"\|"delete"\|"click","message_ids":[...]}` → `{"changed"}` |
| in-app | `POST {host}/v1/web/inapp`, body `{"id","user_hash","platform":"ios"}` → `{"messages":[{"id","layout","content","trigger","end_at"}]}` |
| push open / dismissal | `POST {pulse_track}?a=clicked\|dismissed&platform=ios`, no body, no site key → 204 (404 bad token, 400 bad action) |

## What was verified

- `PulseCore` and its tests were compiled and run on Linux in Docker with
  `docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -v $PWD:/w -w /w swift:5.10 swift test`
  (Swift 5.10): 33 tests, all passing. They cover the anonymous id and its site-key namespace, the
  identify merge prop, validation, batching, persistence across restarts, backoff and drop rules,
  sessions, device props, opt-out, push token registration, push report URLs and retries, inbox
  parsing and the user-hash rule, and in-app frequency rules. On Linux the `Pulse` target compiles to
  just the re-export, because UIKit and UserNotifications are missing.
- The `Pulse` target's UIKit and UserNotifications code (`PulseClient.start`, lifecycle observers,
  `UIDevice` details, `PulseNotificationDelegate`, the in-app view controller and banner) was **not**
  built or run here: there is no Xcode on this machine. Expect to fix small compile issues the first
  time it is built for iOS.
- `URLSessionHTTPClient` was compiled but not exercised (the tests use a fake client). Nothing was
  tested against a running Pulse server or a real device.
