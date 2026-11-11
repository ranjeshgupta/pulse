import { describe, expect, it } from "vitest";
import {
  attrText, campaignLink, campaignName, contactName, distinctNames, eventsPath, formatProps, messagesPath, messageStatus, nextBefore,
  parseNames, productList, propsPreview, stepTypeLabel, toggleName, uniqueBy,
} from "./contactPage";

describe("contactName", () => {
  it("prefers name, then first and last", () => {
    expect(contactName({ attrs: { name: " Asha Rao ", first_name: "X" } })).toBe("Asha Rao");
    expect(contactName({ attrs: { first_name: "Asha", last_name: "Rao" } })).toBe("Asha Rao");
    expect(contactName({ attrs: { last_name: "Rao", name: "" } })).toBe("Rao");
    expect(contactName({ attrs: {} })).toBe("");
  });
});

describe("attrText", () => {
  it("formats lists, objects and missing values", () => {
    expect(attrText({ attrs: { a: ["x", 1] } }, "a")).toBe("x, 1");
    expect(attrText({ attrs: { a: { b: 1 } } }, "a")).toBe('{"b":1}');
    expect(attrText({ attrs: { a: 5 } }, "a")).toBe("5");
    expect(attrText({ attrs: {} }, "a")).toBe("");
  });
});

describe("productList", () => {
  it("reads lists, comma text and JSON", () => {
    expect(productList(["A", "B", "A", " "])).toEqual(["A", "B"]);
    expect(productList("A, B ,C")).toEqual(["A", "B", "C"]);
    expect(productList('["X","Y"]')).toEqual(["X", "Y"]);
    expect(productList(42)).toEqual(["42"]);
    expect(productList(undefined)).toEqual([]);
    expect(productList({ a: 1 })).toEqual([]);
  });
});

describe("campaignLink and campaignName", () => {
  it("links broadcasts and journeys", () => {
    expect(campaignLink({ source: "broadcast", source_id: 4 })).toBe("/broadcasts/4");
    expect(campaignLink({ source: "journey", source_id: 9 })).toBe("/journeys/9");
    expect(campaignLink({ source: "other", source_id: 0 })).toBeNull();
    expect(campaignName({ source: "journey", source_id: 9 })).toBe("Journey 9");
    expect(campaignName({ source: "broadcast", source_id: 4, campaign: "Sale" })).toBe("Sale");
  });
});

describe("messageStatus", () => {
  it("labels known and unknown statuses", () => {
    expect(messageStatus("bounced")).toEqual({ label: "Bounced", tone: "bad" });
    expect(messageStatus("fallback").label).toBe("Sent as SMS");
    expect(messageStatus("new_thing")).toEqual({ label: "New thing", tone: "" });
    expect(messageStatus("").label).toBe("Unknown");
  });
});

describe("props", () => {
  it("pretty-prints and previews", () => {
    expect(formatProps("{}")).toBe("");
    expect(formatProps('{"a":1}')).toBe('{\n  "a": 1\n}');
    expect(formatProps("not json")).toBe("not json");
    expect(propsPreview('{"a":1,"b":{"c":2}}')).toBe('a=1 · b={"c":2}');
    expect(propsPreview('{"a":"' + "x".repeat(200) + '"}', 20)).toHaveLength(20);
    expect(propsPreview("")).toBe("");
  });
});

describe("names", () => {
  it("parses and toggles comma lists", () => {
    expect(parseNames(" a, b,,a ,c")).toEqual(["a", "b", "c"]);
    expect(parseNames(Array.from({ length: 30 }, (_, i) => `e${i}`).join(","))).toHaveLength(20);
    expect(toggleName("a, b", "b")).toBe("a");
    expect(toggleName("a", "c")).toBe("a, c");
    expect(toggleName("", "c")).toBe("c");
  });
  it("lists names by frequency", () => {
    expect(distinctNames([{ name: "b" }, { name: "a" }, { name: "b" }, { name: "c" }], 2)).toEqual(["b", "a"]);
  });
});

describe("paths and paging", () => {
  it("builds the timeline and message requests", () => {
    expect(eventsPath("a b", { kind: "pulse", names: ["x", "y"], before: "2026-01-01T00:00:00Z", limit: 50 }))
      .toBe("/v1/contacts/a%20b/events?name=x%2Cy&kind=pulse&before=2026-01-01T00%3A00%3A00Z&limit=50");
    expect(eventsPath("u1", { kind: "", names: [], limit: 50 })).toBe("/v1/contacts/u1/events?limit=50");
    expect(messagesPath("u/1", undefined, 50)).toBe("/v1/contacts/u%2F1/messages?limit=50");
  });
  it("pages on the last row's time only when the page was full", () => {
    const rows = [{ t: "3" }, { t: "2" }];
    expect(nextBefore(rows, 2, (r) => r.t)).toBe("2");
    expect(nextBefore(rows, 3, (r) => r.t)).toBeUndefined();
    expect(nextBefore([], 0, (r: { t: string }) => r.t)).toBeUndefined();
  });
  it("drops repeated rows", () => {
    expect(uniqueBy([{ id: "a" }, { id: "b" }, { id: "a" }], (r) => r.id)).toEqual([{ id: "a" }, { id: "b" }]);
  });
});

describe("stepTypeLabel", () => {
  it("turns step types into words", () => {
    expect(stepTypeLabel("send_email")).toBe("Send email");
    expect(stepTypeLabel("")).toBe("");
  });
});
