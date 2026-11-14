import { describe, expect, it } from "vitest";
import { currentWord, freeText, joinQuery, splitQuery, suggest, takeChip, tokenize, type SuggestData } from "./contactFilters";

const data: SuggestData = {
  segments: [{ id: 4, name: "VIP buyers", kind: "rule" }, { id: 9, name: "Webinar guests", kind: "static" }],
  tags: ["vip", "beta"], stages: ["Lead", "Customer"], companies: ["Acme"], users: [{ name: "Ravi", email: "ravi@example.com" }],
};

describe("contact filters", () => {
  it("tokenizes like the server", () => {
    expect(tokenize(`segment:"VIP buyers"  asha k`)).toEqual(["segment:VIP buyers", "asha", "k"]);
  });
  it("splits a query into chips and text, and back", () => {
    const { chips, text } = splitQuery(`is:anonymous segment:"VIP buyers" list:9 priya shopify:12`);
    expect(chips).toEqual(["is:anonymous", `segment:"VIP buyers"`, "list:9"]);
    expect(text).toBe("priya shopify:12");
    expect(joinQuery(chips, text)).toBe(`is:anonymous segment:"VIP buyers" list:9 priya shopify:12`);
  });
  it("takes a chip when a filter is finished", () => {
    expect(takeChip("asha tag:vip ")).toEqual({ chip: "tag:vip", rest: "asha " });
    expect(takeChip("tag:vip")).toBeNull();
    expect(takeChip("tag:vip", true)).toEqual({ chip: "tag:vip", rest: "" });
    expect(takeChip(`segment:"VIP bu`)).toBeNull();
    expect(takeChip(`segment:"VIP buyers" `)).toEqual({ chip: `segment:"VIP buyers"`, rest: "" });
    expect(takeChip("hello ")).toBeNull();
  });
  it("leaves half-typed filters out of the query", () => {
    expect(freeText(`asha segment:vi is:`)).toBe("asha");
    expect(freeText(`"asha rao" shopify:1`)).toBe(`"asha rao" shopify:1`);
  });
  it("finds the word being typed", () => {
    expect(currentWord(`asha segment:"VIP b`)).toEqual({ before: "asha ", word: `segment:"VIP b` });
    expect(currentWord("asha ")).toEqual({ before: "asha ", word: "" });
  });
  it("suggests keys, then values", () => {
    expect(suggest("", data).map((s) => s.label)).toContain("is:");
    expect(suggest("se", data)).toEqual([expect.objectContaining({ label: "segment:", fill: "segment:" })]);
    expect(suggest("is:", data).map((s) => s.chip)).toEqual(["is:anonymous", "is:identified", "is:unsubscribed", "is:bounced", "is:dnc"]);
    expect(suggest("segment:vip", data)).toEqual([expect.objectContaining({ chip: `segment:"VIP buyers"` })]);
    expect(suggest("list:web", data)[0].chip).toBe(`segment:"Webinar guests"`);
    expect(suggest("owner:", data).map((s) => s.chip)).toEqual(["owner:me", "owner:none", "owner:ravi@example.com"]);
    expect(suggest("points:>5", data)[0].chip).toBe("points:>5");
    expect(suggest("tag:b", data).map((s) => s.chip)).toEqual(["tag:beta"]);
    expect(suggest("asha", data)).toEqual([]);
  });
});

describe("event and date filters in the search box", () => {
  const d = { segments: [], tags: [], stages: [], companies: [], users: [], events: ["purchase", "page_view"], today: "2026-11-14" };
  it("lists the Add filter questions when the box is empty, and finds them by word", () => {
    expect(suggest("", d).slice(0, 6).map((s) => s.menu)).toEqual(["event", "no_event", "campaign", "journey", "active", "form"]);
    expect(suggest("date", d).map((s) => s.menu)).toContain("event");
    expect(suggest("camp", d)[0]).toMatchObject({ menu: "campaign" });
    expect(suggest("ev", d).some((s) => s.label === "event:")).toBe(true);
  });
  it("suggests event names and quick date ranges as chips", () => {
    expect(suggest("event:pur", d)).toEqual([{ label: "event:purchase", hint: "Did it", chip: "event:purchase" }]);
    const p = suggest("period:", d);
    expect(p.map((s) => s.label)).toEqual(["Last 7 days", "Last 30 days", "Last 90 days", "This month"]);
    expect(p[1].chip).toBe("period:2026-10-16..2026-11-14");
    expect(p[3].chip).toBe("period:2026-11-01..2026-11-14");
  });
});
