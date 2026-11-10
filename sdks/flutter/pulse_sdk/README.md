# Pulse Flutter SDK

Send events from a Flutter app to [Pulse](../../../README.md): track events and screens, identify
signed-in users (with identity verification), and register push tokens.

It mirrors the web SDK (`web/src/sdk/pulse.ts`): people are anonymous (`anon-` + 24 hex characters)
until `identify`, and `identify` merges the anonymous visitor into the user. Events are queued on the
device, persisted (they survive app restarts) and sent in batches.

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
    siteKey: 'sk_...',                       // Settings → Website in Pulse
    flushInterval: const Duration(seconds: 10),
    maxBatch: 50,                            // events per request, 1-50
    debug: false,                            // true logs every step with debugPrint
  ));
  runApp(const MyApp());
}
```

Other options: `maxQueue` (default 1,000 events kept while offline; the oldest are dropped beyond
that), `flushOnBackground` (default true, see below) and `requestTimeout` (default 30 s).

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

## Testing

`Pulse.init` accepts an `http.Client` (use `MockClient` from `package:http/testing.dart`) and a
`PulseStorage` (use `InMemoryPulseStorage`). Set `flushOnBackground: false` in unit tests, and call
`Pulse.instance.dispose()` in `tearDown`.

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
