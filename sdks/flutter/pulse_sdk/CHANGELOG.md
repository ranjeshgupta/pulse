# Changelog

## 0.1.0

- First release.
- `Pulse.init`, `identify` (with identity verification `userHash`), `track`, `screen`, `reset`, `flush`.
- Anonymous ids (`anon-` + 24 hex characters) until `identify`; `identify` merges the anonymous visitor.
- Persistent event queue (survives app restarts, capped at 1,000 events) with batching (up to 50 per
  request), periodic flush, retry with exponential backoff on 429/5xx/network errors.
- `registerPushToken` for FCM registration tokens (Android and iOS); the token is re-registered automatically
  after `identify` and `reset`.
- Optional flush when the app goes to the background (`flushOnBackground`).
