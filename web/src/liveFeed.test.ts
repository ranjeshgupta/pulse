import { describe, expect, it } from "vitest";
import type { LiveEvent } from "./api";
import {
  announcement, clockTime, contactHref, ERROR_BACKOFF_MS, liveQuery, mergeEvents, nameProblem, normalizedName, POLL_MS, propsPreview, propsJSON,
  RATE_LIMIT_BACKOFF_MS, retryDelay,
} from "./liveFeed";

const ev = (n: number, extra: Partial<LiveEvent> = {}): LiveEvent => ({
  cursor: `${n}-0`, id: `e${n}`, name: "page_view", ts: "2026-11-11T10:00:00Z", external_id: `c${n}`, contact_id: n, ...extra,
});

describe("liveQuery", () => {
  it("sends only what is set", () => {
    expect(liveQuery("", { name: "", contact: "" })).toBe("limit=200");
    expect(liveQuery("17-3", { name: " order* ", contact: " u 1 " }, 50)).toBe("cursor=17-3&name=order*&contact=u+1&limit=50");
    expect(liveQuery("", { name: "", contact: "" }, 5000)).toBe("limit=200");
  });
});

describe("mergeEvents", () => {
  it("puts the newest on top", () => {
    const first = mergeEvents([], [ev(1), ev(2), ev(3)]);
    expect(first.events.map((e) => e.id)).toEqual(["e3", "e2", "e1"]);
    expect(first.added).toBe(3);
    const next = mergeEvents(first.events, [ev(4), ev(5)]);
    expect(next.events.map((e) => e.id)).toEqual(["e5", "e4", "e3", "e2", "e1"]);
    expect(next.added).toBe(2);
  });
  it("skips events already shown, also within one poll", () => {
    const cur = mergeEvents([], [ev(1), ev(2)]).events;
    const m = mergeEvents(cur, [ev(2), ev(3), ev(3)]);
    expect(m.events.map((e) => e.id)).toEqual(["e3", "e2", "e1"]);
    expect(m.added).toBe(1);
  });
  it("returns the same list when nothing is new", () => {
    const cur = mergeEvents([], [ev(1)]).events;
    expect(mergeEvents(cur, []).events).toBe(cur);
    expect(mergeEvents(cur, [ev(1)]).events).toBe(cur);
  });
  it("caps the list, dropping the oldest", () => {
    const many = Array.from({ length: 8 }, (_, i) => ev(i));
    const m = mergeEvents([], many, 5);
    expect(m.events.map((e) => e.id)).toEqual(["e7", "e6", "e5", "e4", "e3"]);
    const more = mergeEvents(m.events, [ev(8), ev(9)], 5);
    expect(more.events.map((e) => e.id)).toEqual(["e9", "e8", "e7", "e6", "e5"]);
  });
  it("falls back to the cursor when an event has no id", () => {
    const m = mergeEvents([ev(1, { id: "" })], [ev(1, { id: "" }), ev(2, { id: "" })]);
    expect(m.added).toBe(1);
  });
});

describe("filters", () => {
  it("checks event names with a trailing *", () => {
    expect(nameProblem("")).toBeNull();
    expect(nameProblem("order_placed")).toBeNull();
    expect(nameProblem("order*")).toBeNull();
    expect(nameProblem("*")).toBeNull();
    expect(nameProblem("or*der")).toMatch(/end/);
    expect(nameProblem("9lives")).toMatch(/start with a letter/);
    expect(nameProblem("has space")).toMatch(/letters/);
    expect(normalizedName(" * ")).toBe("");
    expect(normalizedName(" a* ")).toBe("a*");
  });
});

describe("backoff", () => {
  it("waits longer after errors, 5 s after 429", () => {
    expect(retryDelay(null)).toBe(POLL_MS);
    expect(retryDelay({ status: 429 })).toBe(RATE_LIMIT_BACKOFF_MS);
    expect(RATE_LIMIT_BACKOFF_MS).toBe(5000);
    expect(retryDelay(new Error("offline"))).toBe(ERROR_BACKOFF_MS);
  });
});

describe("display", () => {
  it("formats the clock", () => {
    const d = new Date(2026, 10, 11, 9, 5, 7);
    expect(clockTime(d.toISOString())).toBe("09:05:07");
    expect(clockTime("nope")).toBe("");
  });
  it("previews props on one line", () => {
    expect(propsPreview(undefined)).toBe("No properties");
    expect(propsPreview(undefined, true)).toBe("Properties too large to show");
    expect(propsPreview({ amount: 499, sku: "A1", tags: ["x", "y"], meta: { a: 1 }, ok: true, gone: null })).toBe(
      'amount=499  sku="A1"  tags=[2]  meta={…}  ok=true  gone=null',
    );
    const long = propsPreview({ text: "x".repeat(500) }, false, 40);
    expect(long.length).toBe(40);
    expect(long.endsWith("…")).toBe(true);
    expect(propsPreview({ a: 1 }, true)).toBe("a=1 (cut short)");
    expect(propsJSON({ a: 1 })).toBe('{\n  "a": 1\n}');
  });
  it("announces and links", () => {
    expect(announcement(0)).toBe("");
    expect(announcement(1)).toBe("1 new event");
    expect(announcement(1200)).toBe("1,200 new events");
    expect(contactHref("a/b c")).toBe("/contacts/a%2Fb%20c");
  });
});
