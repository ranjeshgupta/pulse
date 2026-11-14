import { describe, expect, it } from "vitest";
import { addFilters, defaults, FILTER_MENU, findItem, missing } from "./contactFilterMenu";
import { LINK_KEYS, FILTER_KEYS } from "./contactFilters";

describe("Add filter menu", () => {
  it("offers every filter the contact list understands, except the step-level ones other pages link to", () => {
    const keys = new Set(FILTER_MENU.flatMap((g) => g.items).flatMap((i) => i.build(Object.fromEntries(i.fields.map((f) => [f.key, f.kind === "date" ? "2026-11-01" : "x"]))).map(([k]) => k)));
    const linkOnly = ["in", "at", "run", "step", "reason", "amp_poll", "answer", "category"]; // reached from journey/campaign/poll pages
    for (const k of [...FILTER_KEYS.map((f) => f.key), ...LINK_KEYS]) {
      if (!linkOnly.includes(k)) expect(keys, k).toContain(k);
    }
  });
  it("starts an event filter on the last 30 days and builds its chips", () => {
    const item = findItem("event")!;
    const v = defaults(item, "2026-11-14");
    expect([v.from, v.to]).toEqual(["2026-10-16", "2026-11-14"]);
    expect(missing(item, v)).toBe("Event");
    expect(item.build({ ...v, event: "purchase" })).toEqual([["event", "purchase"], ["period", "2026-10-16..2026-11-14"]]);
    expect(item.build({ ...v, event: "purchase", product: "SKU-1" }).at(-1)).toEqual(["product", "SKU-1"]);
  });
  it("needs both dates in order when it has any", () => {
    const item = findItem("campaign")!;
    const v = { campaign: "broadcast/4", outcome: "opened", from: "2026-11-02", to: "" };
    expect(missing(item, v)).toBe("To");
    expect(missing(item, { ...v, to: "2026-11-01" })).toMatch(/on or after/);
    expect(item.build({ ...v, from: "", to: "" })).toEqual([["campaign", "broadcast/4"], ["outcome", "opened"]]);
  });
  it("replaces filters that may appear only once and keeps the rest", () => {
    const chips = ["tag:vip", "event:purchase", "period:2026-10-01..2026-10-31"];
    expect(addFilters(chips, ["event:signup", "period:2026-11-01..2026-11-14"])).toEqual(["tag:vip", "event:purchase", "event:signup", "period:2026-11-01..2026-11-14"]);
    expect(addFilters(["tag:vip"], ["tag:vip"])).toEqual(["tag:vip"]);
  });
});
