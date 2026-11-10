# Changelog

## 0.3.0

- Device data: `identify`, `screen_view` and `session_start` carry flat props `platform`
  (`android`/`ios`), `device_type` (`mobile`/`tablet`, from the first view's shortest side, 600+
  logical pixels is a tablet), `os`, `os_version` (from `Platform.operatingSystemVersion`, shortened
  to e.g. "14" / "17.4"), `locale` (BCP 47), and `app_version`, `device_model`, `device_brand` from the
  new `PulseConfig.appVersion`, `deviceModel`, `deviceBrand` (plus `osVersion` to override the
  detected version). On iOS the brand defaults to Apple and the model to iPhone/iPad. Unknown values
  are left out; every value is cut to 100 characters. The server keeps the latest as profile attributes.
- Sessions: a session starts with the first event and again after 30 minutes without events or in the
  background. Each starts with a `session_start` event `{session_id, ...device props}`; `screen_view`
  carries `session_id`. The session survives app restarts within 30 minutes; `reset` ends it.
  `Pulse.instance.sessionId` and `deviceProperties` expose the current values.
- `Pulse.init` takes an optional `clock` (for tests). The lifecycle observer is now always registered
  (it measures time in the background), so `init` always calls `WidgetsFlutterBinding.ensureInitialized`.
- Custom `track` events are unchanged (no device props).

## 0.2.0

- App inbox: `Pulse.instance.inbox` with `list({before})`, `markRead([ids])`, `delete([ids])`,
  `click(id)`, `unreadCount` and an `unread` `ValueListenable<int>` for badges; typed
  `PulseInboxMessage` / `PulseInboxPage`. A signed-in user needs their user hash: the SDK throws
  `PulseException` (403) without a valid one. Other failures return an empty page / 0 and log.
- `PulseInboxView`: ready-made, themeable inbox list (unread dot, image, pull to refresh, swipe to
  delete, paging; tap marks read, records a click and calls `onOpen`).
- In-app messages: campaigns are fetched at init, after `identify`/`reset` and on resume (at most every
  15 minutes), and shown when `track`/`screen` matches their trigger (event, screen, delay) and frequency
  (`once` persisted, `session`, `daily`, `always`). Modal, banner and fullscreen layouts with the
  campaign's colours. Records `inapp_shown`, `inapp_clicked` and `inapp_dismissed`.
- New: `Pulse.attachNavigator` / `detachNavigator`, `Pulse.instance.inApp` (`campaigns`, `refresh()`),
  `PulseConfig.inAppMessages` (default true) and `PulseConfig.onInAppAction`, `PulseException`.
- The lifecycle observer is now also registered when `inAppMessages` is on (to refresh on resume).

## 0.1.0

- First release.
- `Pulse.init`, `identify` (with identity verification `userHash`), `track`, `screen`, `reset`, `flush`.
- Anonymous ids (`anon-` + 24 hex characters) until `identify`; `identify` merges the anonymous visitor.
- Persistent event queue (survives app restarts, capped at 1,000 events) with batching (up to 50 per
  request), periodic flush, retry with exponential backoff on 429/5xx/network errors.
- `registerPushToken` for FCM registration tokens (Android and iOS); the token is re-registered automatically
  after `identify` and `reset`.
- Optional flush when the app goes to the background (`flushOnBackground`).
