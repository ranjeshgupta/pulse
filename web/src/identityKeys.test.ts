import { describe, expect, it } from "vitest";
import { addKey, DEFAULT_KEYS, explain, move, problem, progress, removeKey } from "./identityKeys";

describe("identity keys", () => {
  const keys = addKey(addKey(DEFAULT_KEYS, "email"), "phone");
  it("adds, moves and removes keys; the external id stays", () => {
    expect(keys.map((k) => k.field)).toEqual(["external_id", "email", "phone"]);
    expect(addKey(keys, "email")).toBe(keys);
    expect(move(keys, 2, -1).map((k) => k.field)).toEqual(["external_id", "phone", "email"]);
    expect(move(keys, 0, -1)).toBe(keys);
    expect(removeKey(keys, "external_id")).toBe(keys);
    expect(removeKey(keys, "email").map((k) => k.field)).toEqual(["external_id", "phone"]);
  });
  it("checks keys", () => {
    expect(problem(keys)).toBe("");
    expect(problem([{ field: "email", mode: "merge" }])).toMatch(/external id/);
  });
  it("explains the order", () => {
    expect(explain(DEFAULT_KEYS)[0]).toMatch(/external id only/);
    const words = explain(keys).join(" ");
    expect(words).toMatch(/matched by external id, then email address, then phone number/);
    expect(words).toMatch(/takes that contact in/);
    const above = explain(move(keys, 1, -1)).join(" ");
    expect(above).toMatch(/Ranked above the external id/);
  });
  it("shows backfill progress", () => {
    expect(progress(undefined)).toBe("");
    expect(progress({ field: "email", next_id: 50, contacts: 200 })).toMatch(/25%/);
    expect(progress({ field: "email", next_id: 0, contacts: 0, done_at: "2026-11-21T00:00:00Z" })).toBe("Ready");
  });
});
