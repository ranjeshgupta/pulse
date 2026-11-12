import { describe, expect, it } from "vitest";
import type { DWCVariant, OnsiteAudience, Overlap } from "./api";
import {
  cleanAudience, covers, describeAudience, firstEveryone, isEveryone, localShadows, moveBy, moveVariant, normaliseVariant,
  overlapPairs, overlapRows, parseConditionValue, reorder, setConditionOp,
} from "./onsiteVariants";

describe("variant ordering UI logic", () => {
  const list = ["a", "b", "c", "d"];

  it("drops a dragged variant where it was dropped", () => {
    expect(moveVariant(list, 0, 2)).toEqual(["b", "c", "a", "d"]);
    expect(moveVariant(list, 3, 0)).toEqual(["d", "a", "b", "c"]);
    expect(moveVariant(list, 1, 1)).toEqual(list);
  });

  it("ignores moves out of range and never changes the input", () => {
    expect(moveVariant(list, 0, 9)).toEqual(list);
    expect(moveVariant(list, -1, 2)).toEqual(list);
    moveVariant(list, 0, 3);
    expect(list).toEqual(["a", "b", "c", "d"]);
  });

  it("moves one place up or down, not past either end", () => {
    expect(moveBy(list, 1, -1)).toEqual(["b", "a", "c", "d"]);
    expect(moveBy(list, 1, 1)).toEqual(["a", "c", "b", "d"]);
    expect(moveBy(list, 0, -1)).toEqual(list);
    expect(moveBy(list, 3, 1)).toEqual(list);
  });

  it("applies an order", () => {
    expect(reorder(list, [2, 0, 1, 3])).toEqual(["c", "a", "b", "d"]);
  });
});

describe("normalising variants", () => {
  it("folds a pre-M6 segment into the audience", () => {
    const v = normaliseVariant({ segment_id: 7, html: "<p>x</p>" } as DWCVariant);
    expect(v.audience.segment_ids).toEqual([7]);
    expect(v.segment_id).toBeUndefined();
    expect(v.html).toBe("<p>x</p>");
  });

  it("keeps the audience and doesn't add the segment twice", () => {
    const v = normaliseVariant({ id: "v1", segment_id: 7, audience: { segment_ids: [7, 8], devices: ["mobile"] }, html: "" });
    expect(v.audience).toEqual({ segment_ids: [7, 8], devices: ["mobile"] });
    expect(v.id).toBe("v1");
  });

  it("gives a variant without either an empty audience", () => {
    expect(isEveryone(normaliseVariant({ html: "" } as DWCVariant).audience)).toBe(true);
  });

  it("cleans empty lists and blank page rules", () => {
    expect(cleanAudience({ segment_ids: [], url_contains: [" /sale ", " "], devices: [], conditions: [] })).toEqual({ url_contains: ["/sale"] });
  });
});

describe("contact-field rules", () => {
  it("reshapes the value for the comparison", () => {
    expect(setConditionOp({ prop: "city", op: "eq", value: "Pune, Mumbai" }, "in")).toEqual({ prop: "city", op: "in", value: ["Pune", "Mumbai"] });
    expect(setConditionOp({ prop: "age", op: "eq", value: "30" }, "gt")).toEqual({ prop: "age", op: "gt", value: 30 });
    expect(setConditionOp({ prop: "age", op: "eq", value: "30" }, "exists")).toEqual({ prop: "age", op: "exists" });
    expect(parseConditionValue("gte", "abc")).toBe("abc");
  });
});

describe("audience descriptions", () => {
  const names = { 1: "VIP", 2: "Staff", 3: "Newsletter" };

  it("says everyone for no rules", () => {
    expect(describeAudience({}, names)).toBe("Everyone");
    expect(describeAudience(undefined)).toBe("Everyone");
  });

  it("reads as one sentence", () => {
    expect(describeAudience({ segment_ids: [1], exclude_segment_ids: [2], url_contains: ["/sale"], devices: ["mobile"] }, names))
      .toBe("Members of VIP, not in Staff, on pages containing /sale, on mobile");
  });

  it("joins several with or, and describes fields", () => {
    expect(describeAudience({ segment_ids: [1, 3], url_not_contains: ["/checkout"], devices: ["desktop", "tablet"], conditions: [{ prop: "city", op: "in", value: ["Pune", "Delhi"] }] }, new Map(Object.entries(names).map(([k, v]) => [Number(k), v]))))
      .toBe("Members of VIP or Newsletter, not on pages containing /checkout, on desktop or tablet, whose city is one of Pune or Delhi");
  });

  it("starts from everyone when only other rules are set, and names unknown segments", () => {
    expect(describeAudience({ exclude_segment_ids: [9], conditions: [{ prop: "plan", op: "exists" }] }, names)).toBe("Everyone, not in segment 9, whose plan is set");
  });
});

describe("shadow detection", () => {
  it("finds nothing among different segments", () => {
    expect(localShadows([{ segment_ids: [1] }, { segment_ids: [2] }]).shadowed).toEqual([]);
    expect(localShadows([] as OnsiteAudience[])).toEqual({ shadowed: [], defaultNeverShown: false });
  });

  it("an everyone variant hides the later ones and the default", () => {
    const r = localShadows([{ segment_ids: [1] }, {}, { segment_ids: [2] }, { devices: ["mobile"] }]);
    expect(r.shadowed.map((s) => [s.index, s.by])).toEqual([[2, 1], [3, 1]]);
    expect(r.defaultNeverShown).toBe(true);
    expect(firstEveryone([{ segment_ids: [1] }, {}])).toBe(1);
  });

  it("segments: a later variant inside an earlier one's segments is hidden", () => {
    expect(covers({ segment_ids: [1, 2] }, { segment_ids: [1] })).toBe(true);
    expect(covers({ segment_ids: [1] }, { segment_ids: [1, 2] })).toBe(false);
    expect(covers({ segment_ids: [1] }, {})).toBe(false);
  });

  it("exclusions: the earlier one's must all be in the later one's", () => {
    expect(covers({ exclude_segment_ids: [2] }, { exclude_segment_ids: [2, 3] })).toBe(true);
    expect(covers({ exclude_segment_ids: [2, 3] }, { exclude_segment_ids: [2] })).toBe(false);
  });

  it("pages: every later page rule contains an earlier one", () => {
    expect(covers({ url_contains: ["/sale"] }, { url_contains: ["/sale/shoes"] })).toBe(true);
    expect(covers({ url_contains: ["/sale/shoes"] }, { url_contains: ["/sale"] })).toBe(false);
    expect(covers({ url_contains: ["/sale"] }, {})).toBe(false);
    expect(covers({ url_not_contains: ["/checkout/pay"] }, { url_not_contains: ["/checkout"] })).toBe(true);
    expect(covers({ url_not_contains: ["/checkout"] }, { url_not_contains: ["/checkout/pay"] })).toBe(false);
    expect(covers({ url_not_contains: ["/checkout"] }, {})).toBe(false);
  });

  it("devices: the later one's must be among the earlier one's", () => {
    expect(covers({ devices: ["mobile", "tablet"] }, { devices: ["mobile"] })).toBe(true);
    expect(covers({ devices: ["mobile"] }, { devices: ["mobile", "desktop"] })).toBe(false);
    expect(covers({ devices: ["mobile"] }, {})).toBe(false);
  });

  it("contact fields: the earlier one's rules must all be in the later one's", () => {
    const pune = { prop: "city", op: "eq", value: "Pune" };
    const vip = { prop: "vip", op: "eq", value: true };
    expect(covers({ conditions: [pune] }, { conditions: [pune, vip] })).toBe(true);
    expect(covers({ conditions: [pune, vip] }, { conditions: [pune] })).toBe(false);
    expect(covers({ conditions: [pune] }, { conditions: [{ ...pune, value: "Delhi" }] })).toBe(false);
  });

  it("reports the first earlier variant that covers, with a note", () => {
    const r = localShadows([{ segment_ids: [1, 2] }, { segment_ids: [1] }, { segment_ids: [2] }]);
    expect(r.shadowed).toEqual([
      { index: 1, by: 0, note: expect.stringContaining("variant 1 first") },
      { index: 2, by: 0, note: expect.stringContaining("variant 1") },
    ]);
    expect(r.defaultNeverShown).toBe(false);
  });
});

describe("overlap table", () => {
  const o: Overlap = {
    variants: [{ index: 0, matching: 100, sees: 100 }, { index: 1, matching: 50, sees: 20, approximate: true }, { index: 2, matching: 10, sees: 0 }],
    pairs: [{ a: 0, b: 1, both: 30 }, { a: 0, b: 2, both: 10 }, { a: 1, b: 2, both: 0 }],
    multi: 35, default: 400, contacts: 530, approximate: true, at: "2026-11-12T10:00:00Z",
    names: ["VIP", "", "Mobile"], shadowed: [{ index: 2, by: 0, note: "" }], default_never_shown: false, cached: false,
  };

  it("makes one row per variant in order", () => {
    expect(overlapRows(o, ["x", "Sale"])).toEqual([
      { index: 0, label: "1. VIP", matching: 100, sees: 100, approximate: false, hidden: false, shadowedBy: undefined },
      { index: 1, label: "2. Sale", matching: 50, sees: 20, approximate: true, hidden: false, shadowedBy: undefined },
      { index: 2, label: "3. Mobile", matching: 10, sees: 0, approximate: false, hidden: true, shadowedBy: 0 },
    ]);
  });

  it("lists only pairs that share contacts, most first", () => {
    expect(overlapPairs(o)).toEqual([{ a: "1. VIP", b: "Variant 2", both: 30, ai: 0, bi: 1 }, { a: "1. VIP", b: "3. Mobile", both: 10, ai: 0, bi: 2 }]);
  });

  it("copes with missing lists", () => {
    const empty = { ...o, variants: null, pairs: null, shadowed: null } as unknown as Overlap;
    expect(overlapRows(empty)).toEqual([]);
    expect(overlapPairs(empty)).toEqual([]);
  });
});

describe("on-site editors render", () => {
  it("server-renders the content slot editor, focus editor and overlap view", async () => {
    const { createElement: h } = await import("react");
    const { renderToString } = await import("react-dom/server");
    const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
    const { MemoryRouter, Route, Routes } = await import("react-router-dom");
    const { DWCEdit, FocusEdit, OverlapView } = await import("./pages/OnSite");
    const at = (path: string, el: React.ReactNode) => renderToString(h(QueryClientProvider, { client: new QueryClient() },
      h(MemoryRouter, { initialEntries: [path] }, h(Routes, null, h(Route, { path: "/x/:id", element: el })))));
    expect(at("/x/new", h(DWCEdit))).toContain("Check overlap");
    expect(at("/x/new", h(FocusEdit))).toContain("Not on pages whose address contains");
    const o: Overlap = {
      variants: [{ index: 0, matching: 5, sees: 5 }, { index: 1, matching: 3, sees: 1, approximate: true }], pairs: [{ a: 0, b: 1, both: 2 }],
      multi: 2, default: 7, contacts: 13, approximate: true, at: new Date().toISOString(), names: ["VIP", ""], shadowed: [], default_never_shown: false, cached: true,
    };
    const html = renderToString(h(QueryClientProvider, { client: new QueryClient() }, h(MemoryRouter, null, h(OverlapView, { o }))));
    expect(html).toContain("1. VIP");
    expect(html).toContain("they see the first one in the order");
    expect(html).toContain("Approximate");
  });
});
