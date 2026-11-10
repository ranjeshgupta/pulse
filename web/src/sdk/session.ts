// Sessions for the web SDK. A session ends after 30 minutes without any event from this browser;
// the session id and the last activity time live in localStorage, so all tabs share one session.

export const SESSION_TIMEOUT_MS = 30 * 60 * 1000;

/**
 * nextSession decides which session an event at `now` (ms) belongs to: the current one while the
 * last activity was less than 30 minutes ago, else a new one (`newId()`, started: true). There is no
 * current session without a session id or a readable last activity time. When the clock went
 * backwards (now before the last activity) the session goes on.
 */
export function nextSession(
  now: number,
  lastActivityMs: number | null | undefined,
  currentSid: string | null | undefined,
  newId: () => string,
  timeoutMs = SESSION_TIMEOUT_MS,
): { sid: string; started: boolean } {
  if (currentSid && typeof lastActivityMs === "number" && Number.isFinite(lastActivityMs) && now - lastActivityMs < timeoutMs) {
    return { sid: currentSid, started: false };
  }
  return { sid: newId(), started: true };
}

/** parseActivity reads a stored last activity time (ms since the epoch), or null. */
export function parseActivity(raw: string | null): number | null {
  if (!raw || !/^\d+$/.test(raw)) return null;
  const n = Number(raw);
  return Number.isSafeInteger(n) ? n : null;
}
