# Pulse Flutter SDK

Send events from a Flutter app to [Pulse](../../../README.md): track events and screens, identify
signed-in users (with identity verification), register push tokens, show the app inbox and in-app
messages.

It mirrors the web SDK (`web/src/sdk/pulse.ts`): people are anonymous (`anon-` + 24 hex characters)
until `identify`, and `identify` merges the anonymous visitor into the user. Events are grouped into
sessions, and identify, screen views and session starts carry the device's platform, OS and locale
(see [Device and session data](#device-and-session-data)). Events are queued on the device, persisted (they survive app restarts) and sent in batches.

The SDK only uses the workspace's public **site key**. Never put an API key or the identity
verification secret in an app.

## Install

```yaml
dependencies:
  pulse_sdk:
    path: ../pulse/sdks/flutter/pulse_sdk   # or a git dependency
```

Dependencies: `http` and `shared_preferences`. The SDK does not depend on Firebase; your app gets
push tokens itself and passes them in.

## Initialise

```dart
import 'package:pulse_sdk/pulse_sdk.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await Pulse.init(PulseConfig(
    host: 'https://pulse.example.com',
    siteKey: 'sk_...',                       // Settings → Sign-in & keys → Your website in Pulse
    flushInterval: const Duration(seconds: 10),
    maxBatch: 50,                            // events per request, 1-50
    debug: false,                            // true logs every step with debugPrint
  ));
  runApp(const MyApp());
}
```

Other options: `appVersion`, `deviceModel`, `deviceBrand` and `osVersion` (see
[Device and session data](#device-and-session-data)), `maxQueue` (default 1,000 events kept while offline; the oldest are dropped beyond
that), `flushOnBackground` (default true, see below), `requestTimeout` (default 30 s),
`inAppMessages` (default true) and `onInAppAction` (see [In-app messages](#in-app-messages)).

After `init`, use `Pulse.instance` anywhere.

## Identify users

Call `identify` when a user signs in (and on app start if they are already signed in):

```dart
await Pulse.instance.identify(
  user.id,
  attributes: {'first_name': user.firstName, 'plan': user.plan},
  email: user.email,
  userHash: user.pulseHash,   // from your server, see below
);
```

The first `identify` after an anonymous period sends `props.anonymous_id`, so the server merges the
anonymous visitor (and their events) into the user. `Pulse.instance.currentId` is the id events are
recorded under: the user id after `identify`, otherwise the anonymous id.

User ids must be 1-255 bytes and must not start with `anon-`. Attribute names follow
`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`; `optout_*`, `tags`, `points` and `merged_into` are ignored by the
server (consent, tags and points cannot be changed with a site key).

### Identity verification

With identity verification on in the workspace, the server only accepts events for a known user when
they carry that user's hash, so nobody can send events as someone else with your public site key.
The hash is:

```
hex(HMAC-SHA256(identity secret, user id))
```

Compute it **on your server** and send it to the app with the signed-in user (for example in your
login response). Never ship the identity secret in the app. Example in Go / Node:

```go
mac := hmac.New(sha256.New, []byte(identitySecret))
mac.Write([]byte(userID))
userHash := hex.EncodeToString(mac.Sum(nil))
```

```js
const userHash = crypto.createHmac("sha256", identitySecret).update(userId).digest("hex");
```

The SDK stores the hash and adds it to every event of the identified user (`props.user_hash`; the
server strips it) and to push-token registrations. Anonymous events need no hash. If the hash is
missing or wrong the server answers 403 and the SDK drops that batch (with a log line).

### Sign out

```dart
await Pulse.instance.reset();
```

`reset` forgets the user id and hash and starts a new anonymous id. Events already queued are still
sent under the id they were recorded with.

## Track events and screens

```dart
Pulse.instance.track('order_completed', {'order_id': 'A-1001', 'total': 49.90});
Pulse.instance.screen('Checkout', {'step': 2});   // event screen_view, props.screen = "Checkout"
```

Event names must match `^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`; a bad name throws `ArgumentError`.
Names the site-key API refuses also throw: `unsubscribed`, `resubscribed`, `tag_added`,
`tag_removed`, `points_changed`, `list_joined`, `list_left`. Props can hold JSON values; `DateTime`
values are sent as ISO 8601 UTC strings.

`track`, `screen` and `identify` return a `Future` that completes once the event is queued and
saved. You don't need to await them.

To record screens automatically, call `screen` from a `NavigatorObserver`:

```dart
class PulseNavigatorObserver extends NavigatorObserver {
  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previousRoute) {
    final name = route.settings.name;
    if (name != null) Pulse.instance.screen(name);
  }
}
// MaterialApp(navigatorObservers: [PulseNavigatorObserver()], ...)
```

## Device and session data

The SDK adds the device's details to `identify`, `screen_view` and `session_start` as flat props,
and Pulse keeps the latest values as profile attributes (for segments such as "Android users on app
version 2.3"):

| Prop | Value | Source |
|---|---|---|
| `platform` | `android` or `ios` | `defaultTargetPlatform` |
| `device_type` | `mobile` or `tablet` (shortest side of the app's view 600+ logical pixels) | the first `FlutterView` |
| `os` | `Android` or `iOS` | `defaultTargetPlatform` |
| `os_version` | e.g. `14`, `17.4` | `Platform.operatingSystemVersion`, or `PulseConfig.osVersion` |
| `locale` | BCP 47, e.g. `en-IN` | the device locale |
| `app_version` | e.g. `2.3.0` | `PulseConfig.appVersion` |
| `device_model` | e.g. `SM-S918B`, `Pixel 8`, `iPhone15,2` | `PulseConfig.deviceModel` (iOS default: `iPhone` / `iPad`) |
| `device_brand` | e.g. `Samsung`, `Google` | `PulseConfig.deviceBrand` (iOS default: `Apple`) |

Unknown values are left out, and each value is cut to 100 characters. Custom `track` events don't
carry them (they stay small). `Pulse.instance.deviceProperties` shows what is sent.

The SDK has no plugin dependencies, so the app version and device model come from your app. With
[package_info_plus](https://pub.dev/packages/package_info_plus) and
[device_info_plus](https://pub.dev/packages/device_info_plus):

```dart
import 'dart:io' show Platform;
import 'package:device_info_plus/device_info_plus.dart';
import 'package:package_info_plus/package_info_plus.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final app = await PackageInfo.fromPlatform();
  final info = DeviceInfoPlugin();
  String? model, brand, osVersion;
  if (Platform.isAndroid) {
    final a = await info.androidInfo;
    model = a.model;                 // "SM-S918B"
    brand = a.manufacturer;          // "samsung"
    osVersion = a.version.release;   // "14"
  } else if (Platform.isIOS) {
    final i = await info.iosInfo;
    model = i.utsname.machine;       // "iPhone15,2"
    osVersion = i.systemVersion;     // "17.4.1"
  }
  await Pulse.init(PulseConfig(
    host: 'https://pulse.example.com',
    siteKey: 'sk_...',
    appVersion: app.version,         // or '${app.version}+${app.buildNumber}'
    deviceModel: model,
    deviceBrand: brand,
    osVersion: osVersion,            // optional: more exact than the detected version on Android
  ));
  runApp(const MyApp());
}
```

### Sessions

A session starts with the first event after `init` and again after **30 minutes** without events or
in the background (measured from when the app was paused; a resume after 30+ minutes starts the new
session straight away). Each session begins with a `session_start` event,
`{session_id, ...device props}`, and every `screen_view` carries `session_id`. The session id and the
last activity time are stored, so a restart within 30 minutes continues the session. `reset` ends the
session: the next event starts a new one. `Pulse.instance.sessionId` is the current id (null before
the first event).

## Delivery, flushing and retries

Queued events are sent:

- every `flushInterval` (default 10 s),
- as soon as `maxBatch` events are waiting,
- right after `identify`,
- when you call `Pulse.instance.flush()`,
- when the app is paused or detached (`flushOnBackground: true`, the default; the SDK registers a
  `WidgetsBindingObserver`).

With `flushOnBackground: false`, flush yourself when the app goes to the background:

```dart
class _AppState extends State<App> with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.paused) Pulse.instance.flush();
  }
}
```

Each request carries 1-50 events. On 429, 5xx or a network error the events stay queued and are
retried with exponential backoff (2 s doubling up to 5 min); `flush()` always tries immediately.
On 400 (invalid events) or 403 (identity verification failed) the batch is dropped and logged.
The queue is saved in `shared_preferences`, so events recorded offline are sent after a restart.

## Push notifications

Get the token in your app (the SDK does not depend on Firebase) and register it. Pulse delivers
mobile push through FCM (HTTP v1) on both platforms, so register the **FCM registration token** on
iOS too (`getToken()`, with APNs configured in your Firebase project), with `PushPlatform.ios`.
The SDK remembers the token and registers it again after `identify` and `reset`, so the device always
belongs to the current person.

```dart
import 'dart:io' show Platform;
import 'package:firebase_messaging/firebase_messaging.dart';

Future<void> registerForPush() async {
  final messaging = FirebaseMessaging.instance;
  await messaging.requestPermission();
  final platform = Platform.isIOS ? PushPlatform.ios : PushPlatform.android;

  final token = await messaging.getToken();
  if (token != null) {
    await Pulse.instance.registerPushToken(token, platform: platform);
  }
  messaging.onTokenRefresh.listen((t) => Pulse.instance.registerPushToken(t, platform: platform));
}
```

`registerPushToken` returns `true` when the server stored the token (204). It is sent right away,
not queued; call it again on the next app start if it returned `false`.

## App inbox

Messages sent to the inbox channel (by journeys and broadcasts) are kept on the server for each
person. `Pulse.instance.inbox` reads and updates the current person's inbox:

```dart
final inbox = Pulse.instance.inbox;

final page = await inbox.list();                 // newest 50 messages, and inbox.unreadCount
if (page.hasMore) {
  final older = await inbox.list(before: page.messages.last.id);
}

await inbox.markRead([message.id]);              // markRead() marks everything read
await inbox.click(message.id);                   // a click in the sender's report; open message.url yourself
await inbox.delete([message.id]);                // delete() deletes EVERYTHING; delete([]) does nothing
```

`PulseInboxMessage` has `id`, `messageId`, `title`, `body`, `imageUrl`, `url`, `category`, `data`
(custom key-values), `createdAt`, `readAt` / `isRead` and `expiresAt`. Reads count as opens and
`click` as a click in the sending journey's or broadcast's report.

**User hash required for signed-in users.** Inboxes are private, so the server only answers for a
signed-in user when the request carries their user hash (see
[Identity verification](#identity-verification)), *even if the workspace does not require identity
verification for events*. Pass `userHash` to `identify`. Without it (or with a wrong one) the inbox
calls throw a `PulseException` (status 403, or before sending when no hash is set). Anonymous people
(`anon-` ids) need no hash.

Other failures don't throw: on a network error, rate limiting (429) or a server error `list` returns
an empty page and the actions return 0 / `false`, with a log line.

### Unread badge

`inbox.unread` is a `ValueListenable<int>`, updated by `list()` and lowered by `markRead` and
`delete` (and reset to 0 by `identify`/`reset`):

```dart
ValueListenableBuilder<int>(
  valueListenable: Pulse.instance.inbox.unread,
  builder: (context, unread, _) => Badge(
    isLabelVisible: unread > 0,
    label: Text('$unread'),
    child: const Icon(Icons.inbox_outlined),
  ),
)
```

Call `inbox.list()` at app start (and on resume, if you like) to refresh the count.

### Ready-made inbox screen

`PulseInboxView` is a `ListView` with an unread dot, image, title, body and time; pull to refresh,
swipe to delete, and older pages loaded at the end of the list. Tapping a message marks it read,
records a click and calls `onOpen`:

```dart
Scaffold(
  appBar: AppBar(title: const Text('Inbox')),
  body: PulseInboxView(
    onOpen: (m) { if (m.url != null) openLink(m.url!); },
    unreadColor: Colors.teal,            // optional; colours and text come from your Theme
    // itemBuilder: (context, m) => MyRow(m),   // draw rows yourself
    // empty: const Text('Nothing here yet'),
  ),
)
```

## In-app messages

In-app campaigns (modal, banner or full screen) are created in Pulse (Campaigns → On-site & in-app →
New → Mobile app) and shown by the SDK when a
trigger event happens in the app. The SDK fetches the current person's campaigns at `init`, after
`identify` and `reset`, and when the app resumes (at most every 15 minutes), and keeps them in memory.
A signed-in user without a valid user hash gets the campaigns of an anonymous visitor (no segment
targeting).

Give the SDK your navigator and handle button links:

```dart
final navigatorKey = GlobalKey<NavigatorState>();

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await Pulse.init(PulseConfig(
    host: 'https://pulse.example.com',
    siteKey: 'sk_...',
    // A button with action "url" was tapped: open the link or deep link.
    onInAppAction: (url) => launchUrl(Uri.parse(url)),   // or your router
  ));
  Pulse.instance.attachNavigator(navigatorKey);
  runApp(MaterialApp(navigatorKey: navigatorKey, home: const HomeScreen()));
  Pulse.instance.track('app_open');   // if you have campaigns triggered by app_open
}
```

When the app calls `track(name)` or `screen(name)`, the first campaign (in the server's priority
order) whose `trigger_event` is `name` — and, for `screen_view`, whose `screen` is that screen when
set — and whose frequency allows is shown after its `delay_seconds`:

| Frequency | Shown |
|---|---|
| `once` | once on this device, ever (remembered in storage) |
| `session` | once per app run |
| `daily` | once per calendar day (device time) |
| `always` | every time the trigger happens |

The SDK never sends `app_open` by itself: track it (or any other trigger event) yourself. One message
is shown at a time; a trigger while one is on screen is ignored. Without `attachNavigator` nothing is
shown (and the frequency is not used up).

Layouts: **modal** (a card over a dimmed background with optional image, title, body, buttons and a
close button), **banner** (at the top or bottom over the app; hides itself after 8 s, tapping it
follows its first `url` button) and **fullscreen** (a full-screen route). The campaign's
`background`, `text_color` and `button_color` are used when set, otherwise your `Theme`'s colours.
A `url` button calls `onInAppAction(value)` and closes the message; a `dismiss` button, the close
button, the back button or a tap outside closes it.

The SDK records, through the normal event queue: `inapp_shown` `{inapp_id}`, `inapp_clicked`
`{inapp_id, button, url}` and `inapp_dismissed` `{inapp_id}` (a banner that times out records no
dismissal). These are the shown and click numbers in Pulse's On-site & in-app list.

`Pulse.instance.inApp.campaigns` lists the cached campaigns and `inApp.refresh()` fetches them now.
Turn the feature off with `PulseConfig(inAppMessages: false)`.

## App content slots

A content slot is a named place in your app (a home banner, a tip on the cart screen) whose content
Pulse chooses per person: the app twin of a website's dynamic web content. Slots are created in Pulse
under **Website & app messages → Content slots** with **Where: App**; use the slot id (lowercase
letters, digits, `-` and `_`) in the app.

```dart
// Drop a slot into any screen. Slots built in the same frame are fetched in one request.
const PulseSlot(id: 'home_banner', padding: EdgeInsets.all(16))

// Draw it yourself (the only way to show `html` or `data`):
PulseSlot(
  id: 'cart_tip',
  placeholder: const SizedBox(height: 0),           // while loading / when empty (default)
  builder: (context, content) => ListTile(
    title: Text(content.card?.title ?? ''),
    subtitle: Text('${content.card?.data['coupon'] ?? ''}'),
    onTap: () => Pulse.instance.content.recordClick(content),  // records + opens the deep link
  ),
)

// Or fetch without a widget:
final content = await Pulse.instance.content.fetch(['home_banner', 'cart_tip']);
final banner = Pulse.instance.content.cached('home_banner');   // null: show nothing
```

The default `PulseSlot` view draws a Material card (image, title, body, button) when the content has
a card, and nothing for HTML-only content (the SDK has no web view). Tapping the card (when it has a
deep link) or its button opens the deep link through `PulseConfig.onInAppAction`, like an in-app
button.

The SDK caches each slot in memory. After `identify` and `reset` the cache is cleared and every slot
asked for so far is fetched again (`PulseSlot` widgets redraw), and when the app resumes, slots older
than 15 minutes are fetched again. When a fetch fails (network, 400, 429, 5xx) the cached content is
kept; `fetch` never throws for that.

Events, through the normal event queue: `app_content_viewed` `{slot, variant}` the first time a
`PulseSlot` shows a given content, and `app_content_clicked` `{slot, variant, link}` on a tap
(`variant` and `link` are left out when not set).

## Testing

`Pulse.init` accepts an `http.Client` (use `MockClient` from `package:http/testing.dart`) and a
`PulseStorage` (use `InMemoryPulseStorage`), and a `clock` to move time forward (sessions). Call `TestWidgetsFlutterBinding.ensureInitialized()` at
the top of `main()`, set `flushOnBackground: false` (and `inAppMessages: false` unless you test
in-app messages) in unit tests, and call `Pulse.instance.dispose()` in `tearDown` (or at the end of a
`testWidgets` body).

```
flutter test                                   # unit tests
PULSE_SITE_KEY=sk_... PULSE_HOST=http://localhost:7080 \
  flutter test tool/integration_test.dart      # optional: send real events to a running server
```

## Server API used

| Call | Endpoint |
|---|---|
| events | `POST {host}/v1/web/events`, header `X-Pulse-Site-Key`, body `{"events":[{"id","external_id","name","ts","props","attrs","email"}]}` → 202 |
| push token | `POST {host}/v1/web/devices`, header `X-Pulse-Site-Key`, body `{"external_id","token","platform":"android"\|"ios","user_hash"}` → 204 |
| inbox | `GET {host}/v1/web/inbox?id=&user_hash=&before=`, header `X-Pulse-Site-Key` → `{"messages":[...],"unread"}` |
| inbox actions | `POST {host}/v1/web/inbox`, body `{"id","user_hash","action":"read"\|"delete"\|"click","message_ids":[...]}` → `{"changed"}` |
| in-app | `POST {host}/v1/web/inapp`, body `{"id","user_hash","platform":"android"\|"ios"}` → `{"messages":[{"id","layout","content","trigger","end_at"}]}` |
| app content | `POST {host}/v1/web/content`, body `{"id","user_hash","platform":"android"\|"ios","slots":[...]}` → `{"content":{"<slot>":{"card","html","variant"}}}` |
