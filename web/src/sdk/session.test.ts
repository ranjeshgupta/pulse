import { describe, expect, it } from "vitest";
import { nextSession, parseActivity, SESSION_TIMEOUT_MS } from "./session";

const MIN = 60_000;
const T = Date.UTC(2026, 10, 11, 12, 0, 0);
const id = () => "new-sid";

describe("nextSession", () => {
  it("starts a session on the first event", () => {
    expect(nextSession(T, null, null, id)).toEqual({ sid: "new-sid", started: true });
    expect(nextSession(T, undefined, undefined, id)).toEqual({ sid: "new-sid", started: true });
  });

  it("keeps the session for activity within 30 minutes", () => {
    expect(nextSession(T + 29 * MIN, T, "s1", id)).toEqual({ sid: "s1", started: false });
    expect(nextSession(T, T, "s1", id)).toEqual({ sid: "s1", started: false });
    expect(nextSession(T + SESSION_TIMEOUT_MS - 1, T, "s1", id)).toEqual({ sid: "s1", started: false });
  });

  it("starts a new session after 30 minutes without events", () => {
    expect(nextSession(T + 31 * MIN, T, "s1", id)).toEqual({ sid: "new-sid", started: true });
    expect(nextSession(T + SESSION_TIMEOUT_MS, T, "s1", id)).toEqual({ sid: "new-sid", started: true });
  });

  it("keeps the session when the clock goes backwards", () => {
    expect(nextSession(T - 5 * MIN, T, "s1", id)).toEqual({ sid: "s1", started: false });
    expect(nextSession(T - 24 * 60 * MIN, T, "s1", id)).toEqual({ sid: "s1", started: false });
  });

  it("starts a session when the id or the last activity is missing or unreadable", () => {
    expect(nextSession(T, T, null, id).started).toBe(true); // e.g. after reset()
    expect(nextSession(T, T, "", id).started).toBe(true);
    expect(nextSession(T, null, "s1", id).started).toBe(true);
    expect(nextSession(T, NaN, "s1", id).started).toBe(true);
  });

  it("only makes a new id when it needs one", () => {
    let made = 0;
    nextSession(T + MIN, T, "s1", () => { made++; return "x"; });
    expect(made).toBe(0);
  });
});

describe("parseActivity", () => {
  it("reads stored times defensively", () => {
    expect(parseActivity(String(T))).toBe(T);
    expect(parseActivity(null)).toBeNull();
    expect(parseActivity("")).toBeNull();
    expect(parseActivity("12abc")).toBeNull();
    expect(parseActivity("-5")).toBeNull();
    expect(parseActivity("9".repeat(30))).toBeNull();
  });
});
