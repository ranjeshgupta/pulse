// @vitest-environment happy-dom
// Odd input for the pure helpers: what the API, saved settings, pasted JSON, CSV files and people's
// typing can hand them. Names that are also Object's own ("constructor", "toString", "__proto__"),
// null where a list is expected, NaN from an emptied number field, invalid dates, huge counts and
// pathological text. Helpers must not throw, hang, pollute Object.prototype, or put "NaN",
// "undefined" or "[object Object]" (or a function's source) on screen.
import { afterEach, describe, expect, it } from "vitest";
import type { EventSchema, JourneyGraph, Rule, SchemaProp } from "./api";
import { attrsFromRows, rowsFromAttrs } from "./accounts";
import { daysFor, formatDays, formatDuration, retrySchedule, rowsToOverrides } from "./automation";
import { bounceTypeOf } from "./campaignDash";
import { chunks, toProducts } from "./catalogCsv";
import { lastBounds } from "./cohorts";
import { consentLine } from "./consentHistory";
import { defaults, FILTER_MENU } from "./contactFilterMenu";
import { periodPresets, suggest } from "./contactFilters";
import { addDays, canListContacts, whyNoLink } from "./contactLinks";
import { attrText, contactName, messageStatus } from "./contactPage";
import { csvCell } from "./csv";
import { fold } from "./customerValue";
import { exampleProps, fillDays, formatShare } from "./eventSchemas";
import { share as funnelShare } from "./funnelMath";
import { explain, keyLabel, progress } from "./identityKeys";
import { guessTarget } from "./importMapping";
import { parseClipboard, pasteSteps } from "./journeyClipboard";
import { channelRuleProblems, formatWait, rulesText, share, stepLabel, typeInfo } from "./journeyGraph";
import { percent, whenText } from "./overview";
import { summary } from "./pages/journey/nodeText";
import { layout } from "./paths";
import { contactVars, eventVars, exampleEvent, fillSample, findRefs, insertAt, lint } from "./personalize";
import { productsCsvRows } from "./productsDash";
import { parseRequest, parseStatuses } from "./providerAuth";
import { describe as describeSpec, formatValue, groupLabel, labelIndices, lineGeometry, rangeDaysOf, reportRows } from "./reportBuilder";
import { isSafeUrl, normalizeLink, richTextLoss } from "./richTextHtml";
import { dict, finite, hasOwn, own, setOwn } from "./safe";
import { parseText } from "./segmentClipboard";
import { describe as describeRule, eventProps, fieldInfo, fieldType, newGroup, pickNoun, problems, type EventCond } from "./segmentRules";
import { compact, labelIndexes, linePath, rangeDays, shortDay, updatedAt } from "./usersDash";

// ---- a small seeded generator (no dependencies) ----

function rng(seed: number) {
  let s = seed >>> 0;
  return () => ((s = (s * 1664525 + 1013904223) >>> 0) / 0x1_0000_0000);
}
const pick = <T,>(r: () => number, xs: readonly T[]): T => xs[Math.floor(r() * xs.length)];

/** Values that have broken helpers like these before. */
const ODD_NAMES = ["constructor", "toString", "valueOf", "hasOwnProperty", "__proto__", "prototype", "isPrototypeOf", "__defineGetter__"];
const ODD_NUMBERS = [NaN, Infinity, -Infinity, -1, 0, 1e308, -0, 2 ** 53 + 1];

/** shown is text a person would see: no NaN, undefined, [object Object] or a function's source. */
function expectShown(s: string) {
  expect(s).not.toMatch(/NaN|undefined|\[object Object\]|native code|function /);
}

const pollutionKeys = ["polluted", "x", "sample", "SKU", "code", "y"];
afterEach(() => {
  // Nothing may ever land on Object.prototype.
  for (const k of pollutionKeys) expect(Object.prototype.hasOwnProperty.call(Object.prototype, k), `Object.prototype.${k}`).toBe(false);
  expect(({} as Record<string, unknown>).polluted).toBeUndefined();
});

// ---- safe.ts ----

describe("safe helpers", () => {
  it("read and write own keys only", () => {
    const o: Record<string, number> = { a: 1 };
    for (const k of ODD_NAMES) expect(own(o, k)).toBeUndefined();
    expect(own(o, "a")).toBe(1);
    expect(own(null, "a")).toBeUndefined();
    expect(own(o, 5 as unknown as string)).toBeUndefined();
    const d = dict({ x: 1 });
    for (const k of ODD_NAMES) expect(d[k]).toBeUndefined();
    const w: Record<string, string> = {};
    setOwn(w, "__proto__", "kept");
    expect(Object.keys(w)).toEqual(["__proto__"]);
    expect(JSON.stringify(w)).toBe('{"__proto__":"kept"}');
    expect(Object.getPrototypeOf(w)).toBe(Object.prototype);
    expect(hasOwn(w, "__proto__")).toBe(true);
    expect(finite("12")).toBe(12);
    for (const n of [NaN, Infinity, null, "x", ""]) expect(finite(n, 7)).toBe(7);
  });
});

// ---- personalize.ts ----

const schema = (props: Partial<SchemaProp>[]): Pick<EventSchema, "props"> => ({ props: props as SchemaProp[] });

describe("personalize: sample events", () => {
  it("a list property with an items[] path inside gives object items (the reported crash)", () => {
    const ev = exampleEvent(schema([
      { key: "items", type: "list" },
      { key: "product_code", path: "items[].product_code", type: "string" },
    ]));
    expect(ev.items).toEqual([{ product_code: "Sample product_code" }]);
    // The other order too.
    const ev2 = exampleEvent(schema([
      { key: "product_code", path: "items[].product_code", type: "string" },
      { key: "items", type: "list" },
    ]));
    expect(ev2.items).toEqual([{ product_code: "Sample product_code" }]);
  });

  it("a path ending in [] is a list of sample values, not an object keyed by an empty name", () => {
    expect(exampleEvent(schema([{ key: "tags", path: "tags[]", type: "string" }])).tags).toEqual(["Sample tags"]);
  });

  it("never writes to Object.prototype, whatever the property paths", () => {
    const ev = exampleEvent(schema([
      { key: "a", path: "__proto__.polluted", type: "string" },
      { key: "b", path: "constructor.prototype.polluted", type: "string" },
      { key: "c", path: "__proto__[].polluted", type: "string" },
      { key: "d", path: "toString", type: "number" },
    ]));
    expect(Object.keys(ev).sort()).toEqual(["__proto__", "constructor", "toString"]);
    expect(Object.getPrototypeOf(ev)).toBe(Object.prototype);
    expect(ev.toString).toBe(1299);
  });

  it("skips properties without a key or path, and takes no schema", () => {
    const odd = schema([{ type: "string" }, { key: undefined, path: undefined, type: "number" }, null as unknown as SchemaProp, { key: "ok", type: "bool" }]);
    expect(exampleEvent(odd)).toEqual({ ok: true });
    expect(eventVars(odd).map((v) => v.var)).toEqual(["event.ok"]);
    expect(exampleEvent(null)).toEqual({});
    expect(eventVars(undefined)).toEqual([]);
    expect(contactVars([{ key: undefined as unknown as string, label: "", type: "string" }]).some((v) => v.var === "contact.undefined")).toBe(false);
  });
});

describe("personalize: preview and tags", () => {
  it("never shows inherited values for tags named like Object's", () => {
    for (const k of ODD_NAMES.filter((k) => /^\w+$/.test(k))) {
      const out = fillSample(`[{{contact.${k}}}|{{.Attrs.${k}}}|{{event.${k}}}|{{reco.1.${k}}}]`);
      expectShown(out);
    }
    expect(fillSample("{{contact.constructor | default:\"x\"}}")).toBe("x");
    expect(fillSample("{{.Attrs.toString}}")).toBe("[toString]");
  });

  it("finds tags in linear time (an unclosed {{ before a long run of spaces used to hang)", () => {
    const t0 = performance.now();
    expect(findRefs("{{" + " ".repeat(200_000))).toEqual([]);
    expect(fillSample("{{".repeat(100_000))).toBe("{{".repeat(100_000));
    expect(fillSample("{{ contact.first_name " + " ".repeat(100_000) + "x")).toContain("{{ contact.first_name");
    expect(findRefs("{{contact.x}}".repeat(20_000))).toHaveLength(20_000);
    expect(performance.now() - t0).toBeLessThan(2000);
  });

  it("reads tags exactly as the old pattern did (random fields)", () => {
    const old = (src: string) => [...src.matchAll(/\{\{-?\s*([\s\S]*?)\s*-?\}\}/g)]
      .filter((m) => /^(contact|event|campaign|links|reco|survey)\./.test(m[1].trim()))
      .map((m) => [m.index, m.index! + m[0].length]);
    const r = rng(7);
    const parts = ["{{", "}}", "-", " ", "\n", "contact.first_name", "event.x", "|", "upper", '"', "a", "}", "{", ".Attrs.city"];
    for (let i = 0; i < 3000; i++) {
      const src = Array.from({ length: 1 + Math.floor(r() * 12) }, () => pick(r, parts)).join("");
      expect(findRefs(src).map((x) => [x.start, x.end]), JSON.stringify(src)).toEqual(old(src));
      expect(() => fillSample(src)).not.toThrow();
    }
  });

  it("lints odd catalogues without throwing", () => {
    expect(() => lint("{{contact.x}} {{event.y}}", { contact: [{ var: "contact", label: "", group: "contact" }], event: [{ var: "event", label: "", group: "event" }] })).not.toThrow();
  });

  it("inserts at a cursor that is NaN, negative or past the end without duplicating the text", () => {
    expect(insertAt("abc", "X", NaN)).toEqual({ value: "abcX", cursor: 4 });
    expect(insertAt("abc", "X", -5, -9)).toEqual({ value: "Xabc", cursor: 1 });
    expect(insertAt("abc", "X", 99)).toEqual({ value: "abcX", cursor: 4 });
    expect(insertAt("abc", "X", 2, 1)).toEqual({ value: "abXc", cursor: 3 });
  });
});

// ---- segment rules and clipboard ----

describe("segment rules: names people type", () => {
  it("words events, attributes and properties named like Object's", () => {
    for (const k of ODD_NAMES.filter((k) => /^[a-zA-Z]/.test(k))) {
      const ev: Rule = { event: k, op: "gte", count: 1, days: 7, where: [{ prop: k, op: "eq", value: k }] };
      expectShown(describeRule(ev));
      expectShown(describeRule({ attr: k, op: "eq", value: "x" }));
      expect(eventProps(k)).toEqual([]);
      expect(fieldInfo(k)).toBeUndefined();
      expect(fieldType(k)).toBe("text");
      expect(pickNoun("template_id", k)).toBe("template");
      const cond: EventCond = { kind: "event", event: "purchase", op: "gte", count: 1, days: 30, where: [{ prop: k, op: "eq", value: "" }] };
      for (const p of problems(newGroup("all", [cond]))) expectShown(p);
    }
  });

  it("refuses damaged clipboard rules instead of crashing the builder", () => {
    const root = newGroup();
    const paste = (rule: unknown) => parseText(JSON.stringify({ pulse: "segment-rules", version: 1, rule }), root, []);
    expect(paste({ event: "buy", op: "gte", count: 1, days: 7, where: "x" }).error).toBeTruthy();
    expect(paste({ event: "buy", op: "gte", count: 1, days: 7, where: [null] }).error).toBeTruthy();
    expect(paste({ event: "buy", op: "gte", count: 1, from: 20260101 }).error).toBeTruthy();
    expect(paste({ attr: "city", op: "eq", value: { a: 1 } }).error).toBeTruthy();
    expect(paste({ segment: 3 }).error).toBeTruthy();
    expect(paste({ all: [{ attr: "city", op: "eq", value: "Pune" }, { event: "buy", op: "gte", count: 1, days: 7, where: [{ prop: "sku", op: "in", value: ["a", "b"] }] }] }).node).toBeTruthy();
    expect(parseText('{"pulse":"segment-rules","version":1,"rule":{"__proto__":{"attr":"x","op":"eq"}}}', root, []).error).toBeTruthy();
  });
});

// ---- journeys ----

const graph = (nodes: JourneyGraph["nodes"], entry = Object.keys(nodes)[0]): JourneyGraph => ({ trigger: { type: "segment_entered" }, entry, nodes });

describe("journeys", () => {
  it("refuses a clipboard whose ids or links aren't plain strings", () => {
    const clip = (c: object) => parseClipboard(JSON.stringify({ pulse: "journey-steps", version: 1, ...c }));
    expect(clip({ entry: "constructor", nodes: { a: { type: "exit" } } }).error).toBeTruthy();
    expect(clip({ entry: "toString", nodes: { toString: { type: "exit" } } }).clip).toBeTruthy();
    expect(parseClipboard('{"pulse":"journey-steps","version":1,"entry":"__proto__","nodes":{"__proto__":{"type":"exit"}}}').error).toBeTruthy();
    expect(clip({ entry: "a", nodes: { a: { type: "wait", next: 5 } } }).error).toBeTruthy();
    expect(clip({ entry: "a", nodes: { a: { type: "split", variants: "x" } } }).error).toBeTruthy();
    expect(clip({ entry: "a", nodes: { a: { type: "wait", x: "left" } } }).error).toBeTruthy();
  });

  it("pastes steps whose links point outside the copy without inventing edges", () => {
    const g = graph({ exit: { type: "exit", x: 0, y: 0 } });
    const c = parseClipboard(JSON.stringify({ pulse: "journey-steps", version: 1, entry: "w", nodes: { w: { type: "wait", seconds: 60, next: "toString" } } })).clip!;
    const out = pasteSteps(g, { source: "__trigger", handle: "next" }, c);
    for (const id of out.ids) expect(typeof (out.graph.nodes[id].next ?? "")).toBe("string");
  });

  it("words odd numbers and op names on the canvas", () => {
    for (const n of [...ODD_NUMBERS, null, undefined]) expectShown(formatWait(n as number));
    const s = { type: "split", variants: [{ weight: NaN, label: "A" }, { weight: 50, label: "B" }] };
    expect(share(s as never, 0)).toBe(0);
    expect(typeInfo("constructor")).toBeUndefined();
    expect(stepLabel(graph({}), { type: "check", preset: "constructor" } as never)).toBe("Contact field value");
    expect(channelRuleProblems([{ channel: "sms", template_id: 1, when: [{ prop: "contact.city", op: "constructor", value: "x" }] }] as never)).toContain("choose how to compare contact.city.");
    expectShown(rulesText([{ channel: "sms", when: [{ op: "eq", value: "x" }] }] as never, (c) => c));
  });

  it("summarises steps that name slots or fields like Object's", () => {
    const g = graph({
      d: { type: "on_dwc", slot: "constructor", seconds: 3600 },
      p: { type: "push_dwc", slot: "tostring" },
      a: { type: "update_attr", attrs: { plan: { tier: "gold" } as never } },
      f: { type: "if_form_field", field: "x", op: "constructor" },
    });
    for (const id of ["d", "p", "a", "f"]) expectShown(summary(g, id, { slots: {} }));
    expect(summary(g, "d")).toContain("‘constructor’");
    expect(summary(g, "a")).toBe('plan = {"tier":"gold"}');
  });
});

// ---- settings, imports and data from the API ----

describe("settings and imports", () => {
  it("previews retries for any typed number of tries without freezing", () => {
    const t0 = performance.now();
    expect(retrySchedule(1e12, 3600).length).toBeLessThanOrEqual(100);
    expect(retrySchedule(NaN, 3600)).toEqual([]);
    expect(retrySchedule(-3, 3600)).toEqual([]);
    expect(performance.now() - t0).toBeLessThan(200);
    expect(formatDuration(NaN)).toBe("–");
    expect(formatDays(NaN)).toBe("forever");
  });

  it("takes event overrides named like Object's", () => {
    expect(rowsToOverrides([{ name: "constructor", days: 30 }, { name: "toString", days: 7 }])).toEqual({ value: { constructor: 30, toString: 7 }, error: null });
    const r = { event_days: 365, event_overrides: {} } as never;
    expect(daysFor(r, "constructor")).toBe(365);
    expect(daysFor(r, "valueOf")).toBe(365);
  });

  it("chunks with a size of 0, a negative or NaN still ends", () => {
    for (const n of [0, -1, NaN, Infinity]) expect(chunks([1, 2, 3], n)).toEqual([[1, 2, 3]]);
    expect(chunks([1, 2, 3], 2.5)).toEqual([[1, 2], [3]]);
  });

  it("reads CSV headings named like Object's as unknown columns", () => {
    const r = toProducts([["code", "name", "constructor", "__proto__", "toString"], ["A1", "Kurta", "x", "y", "z"]]);
    expect(r.unknown).toEqual(["constructor", "__proto__", "toString"]);
    expect(r.products).toEqual([{ code: "A1", name: "Kurta", in_stock: true }]);
    expect(guessTarget("constructor")).toBe("constructor");
    expect(guessTarget("__proto__")).toBe("__proto__");
    expect(guessTarget("toString")).toBe("tostring");
    expect(guessTarget("Mobile")).toBe("phone");
  });

  it("keeps a company attribute named __proto__ and reads attrs that are null", () => {
    const out = attrsFromRows([{ key: "__proto__", value: "x" }, { key: "size", value: "12" }]);
    expect(JSON.stringify(out)).toBe('{"__proto__":"x","size":12}');
    expect(rowsFromAttrs(null as never)).toEqual([]);
    expect(rowsFromAttrs({ address: { city: "Pune" } } as never)).toEqual([{ key: "address", value: '{"city":"Pune"}' }]);
  });

  it("keeps provider status words named like Object's", () => {
    const r = parseStatuses("__proto__=delivered\nconstructor=failed");
    expect(Object.keys(r.statuses)).toEqual(["__proto__", "constructor"]);
    expect(r.errors).toEqual([]);
    expect(parseRequest("null", "sms").method).toBe("POST");
    expect(parseRequest("[1]", "sms").method).toBe("POST");
  });

  it("builds example events from schemas without touching Object.prototype", () => {
    const ex = exampleProps({ props: [
      { key: "a", path: "__proto__.polluted", type: "string" },
      { key: "b", path: "__proto__[].polluted", type: "string" },
      { key: "c", path: "constructor", type: "object" },
    ] as SchemaProp[] });
    expect(Object.keys(ex).sort()).toEqual(["__proto__", "constructor"]);
    expect(fillDays([], Infinity, new Date()).length).toBeLessThanOrEqual(3660);
    expect(fillDays(null as never, 3, new Date("bad"))).toHaveLength(3);
    expect(formatShare(NaN)).toBe("–");
  });

  it("labels identity keys named like Object's", () => {
    expect(keyLabel("constructor")).toBe("constructor");
    expect(() => explain([{ field: "external_id", mode: "merge" }, { field: "constructor", mode: "merge" }, { field: "toString", mode: "suggest" }])).not.toThrow();
    expectShown(progress({ contacts: 0.5, next_id: NaN } as never));
  });
});

describe("contacts and consent", () => {
  it("reads a consent event whose props are null, a list or odd channels", () => {
    for (const props of ["null", "[1,2]", "7", '"x"', "{", '{"channel":"constructor"}', '{"channel":"toString","source":{"a":1}}']) {
      const l = consentLine({ id: "1", name: "unsubscribed", ts: "", props });
      expectShown(l!.text);
    }
    expect(consentLine({ id: "1", name: "unsubscribed", ts: "", props: '{"channel":"sms"}' })!.text).toBe("Unsubscribed from SMS");
  });

  it("reads contacts with attrs null and keys named like Object's", () => {
    expect(contactName({ attrs: null as never })).toBe("");
    for (const k of ODD_NAMES) expect(attrText({ attrs: {} }, k)).toBe("");
    expect(attrText({ attrs: { list: [{ a: 1 }, "b"] } }, "list")).toBe('{"a":1}, b');
    expect(messageStatus("constructor").label).toBe("Constructor");
  });

  it("checks list permissions for filters named like Object's", () => {
    expect(canListContacts({ contacts: "view" }, [["constructor", "x"], ["toString", "y"]])).toBe(true);
    expect(whyNoLink({ contacts: "view" }, [["valueOf", "x"]])).toBe("");
  });

  it("moves days and builds date presets from unreadable days", () => {
    expect(addDays("", 1)).toBe("");
    expect(addDays("2026-02-30x", 1)).toBe("2026-02-30x");
    expect(addDays("2026-01-31", NaN)).toBe("2026-01-31");
    expect(addDays("2026-01-31", 1)).toBe("2026-02-01");
    for (const t of ["", "garbage", "2026-13-45"]) {
      for (const p of periodPresets(t)) expectShown(p.value);
      for (const item of FILTER_MENU.flatMap((g) => g.items)) for (const v of Object.values(defaults(item, t))) expectShown(v);
    }
  });

  it("suggests owners who have no name", () => {
    const d = { segments: [], tags: [], stages: [], companies: [], users: [{ name: null as unknown as string, email: "a@x.io" }] };
    expect(suggest("owner:a", d).map((s) => s.chip)).toContain("owner:a@x.io");
  });
});

// ---- dashboards and reports ----

describe("dashboards", () => {
  it("labels report groups that are values named like Object's", () => {
    for (const k of ODD_NAMES) {
      for (const g of ["prop", "attr", "kind", "channel", "event"] as const) expect(groupLabel(k, g)).toBe(k);
    }
    expect(groupLabel("2026-13-01", "month")).toBe("2026-13-01");
    expect(groupLabel(null as never, "prop")).toBe("(not set)");
    expectShown(describeSpec({ source: "events", metric: { type: "median" as never }, group_by: { type: "weird" as never }, chart: "bars" }));
    expectShown(describeSpec({ source: "messages", metric: { type: "count" }, group_by: { type: "none" }, kinds: ["constructor"], channels: ["toString" as never], chart: "bars" }));
  });

  it("formats and draws values that are NaN or missing", () => {
    for (const n of ODD_NUMBERS) {
      expectShown(formatValue(n, "sum"));
      expectShown(lineGeometry([n, 1, n], { width: 100, height: 50, left: 0, right: 0, top: 0, bottom: 0 }).path);
      expectShown(linePath([n, 2], n, 100, 50));
      expectShown(compact(n));
      expectShown(percent(n));
      expectShown(funnelShare(n, 3));
      expectShown(formatShare(n));
    }
    expect(reportRows({ source: "events", metric: { type: "sum", key: "amount" }, group_by: { type: "prop", key: "sku" }, chart: "bars" }, { rows: [{ group: "a", value: NaN }] } as never)[1]).toEqual(["a", ""]);
    expect(reportRows({ source: "events", metric: { type: "count" }, group_by: { type: "none" }, chart: "bars" }, { rows: null } as never)).toEqual([["Events"], [0]]);
    expect(rangeDaysOf({ preset: "custom", from: "bad", to: "2026-01-01" })).toBe(0);
  });

  it("draws a long line without overflowing the stack", () => {
    const values = Array.from({ length: 300_000 }, (_, i) => i % 97);
    expect(lineGeometry(values, { width: 100, height: 50, left: 0, right: 0, top: 0, bottom: 0 }).max).toBe(100);
  });

  it("picks axis labels for any count quickly", () => {
    const t0 = performance.now();
    expect(labelIndices(10, 1e12)).toHaveLength(10);
    expect(labelIndices(1e12, 3)).toHaveLength(3);
    expect(labelIndices(NaN)).toEqual([]);
    expect(labelIndices(5, 1)).toEqual([0, 4]);
    expect(labelIndexes(10, Infinity)).toEqual([0, 2, 5, 7, 9]);
    expect(labelIndexes(Infinity)).toEqual([]);
    expect(performance.now() - t0).toBeLessThan(200);
  });

  it("turns ranges and days that can't be read into something sensible", () => {
    expect(() => rangeDays({ preset: NaN } as never)).not.toThrow();
    expect(() => rangeDays({ preset: 30 } as never, new Date("bad"))).not.toThrow();
    expect(() => lastBounds("week", NaN)).not.toThrow();
    expect(() => lastBounds("month", 1e9)).not.toThrow();
    expect(shortDay("bad")).toBe("bad");
    expect(updatedAt("bad")).toBe("");
    expect(whenText("bad")).toBe("");
  });

  it("folds customer value with missing lists and odd risks", () => {
    const r = fold({ settings: { event: "purchase", amount_prop: "amount" }, rows: null, groups: null, risks: [] } as never);
    expect(r.total).toBe(0);
    const r2 = fold({ settings: { event: "p", amount_prop: "a" }, rows: [{ group: "Lost", churn_risk: "__proto__", contacts: 2, revenue: 1, avg_orders: 1, avg_days_since: 1 }], groups: [], risks: [] });
    expect(r2.groups[0].risks.__proto__).toBe(2);
  });

  it("lays out paths with steps or links out of range", () => {
    const links = [{ step: -1, from: "a", to: "b", people: 3 }, { step: 7, from: "a", to: "b", people: 3 }, { step: 0, from: "a", to: "b", people: NaN }, { step: 0, from: "s", to: "b", people: 2 }];
    for (const steps of [NaN, -2, 0, 1, 2]) {
      const l = layout(links, steps, "s");
      for (const n of l.nodes) expect(Number.isFinite(n.y + n.h + n.x)).toBe(true);
      for (const b of l.bands) expectShown(b.d);
    }
  });

  it("exports rows with missing numbers as empty cells", () => {
    expect(productsCsvRows([{ code: "A", views: 1, viewers: 1, carts: 0, carters: 0, purchases: 0, buyers: 0, revenue: null, abandoned: 0 }] as never, "INR")[1][8]).toBe("");
    expect(bounceTypeOf("constructor")).toBeUndefined();
    expect(csvCell("=1+1")).toBe("'=1+1");
  });
});

// ---- rich text ----

describe("rich text", () => {
  it("doesn't throw on character references past U+10FFFF", () => {
    expect(isSafeUrl("&#99999999;")).toBe(true);
    expect(isSafeUrl("&#x110000;javascript:alert(1)")).toBe(true); // U+FFFD then a scheme: not a javascript: URL
    expect(isSafeUrl("&#106;avascript:alert(1)")).toBe(false);
    expect(normalizeLink("https://x.io/&#99999999;")).toEqual({ url: "https://x.io/&#99999999;" });
    expect(richTextLoss('<p><a href="&#99999999;">x</a></p>')).toBeNull();
  });

  it("reports pasted elements named like Object's as unknown", () => {
    expect(richTextLoss("<constructor>x</constructor>")).toBe("<constructor> elements");
    expect(richTextLoss('<p style="constructor: x">x</p>')).toBe('the inline style "constructor" on <p>');
  });
});

// ---- a broad sweep: odd values into every hardened text helper ----

describe("sweep", () => {
  it("odd values never throw or show NaN/undefined in these helpers", () => {
    const r = rng(42);
    const strings = ["", " ", "😀👍🏽", ".*+?^${}()|[]\\", "a".repeat(5000), ...ODD_NAMES, "2026-02-30", "x\u0000y", "<script>"];
    for (let i = 0; i < 400; i++) {
      const s = pick(r, strings), n = pick(r, ODD_NUMBERS);
      expectShown(fillSample(`{{contact.${s}}} {{event.${s} | upper}} {{reco.${s}}}`));
      expectShown(groupLabel(s, pick(r, ["prop", "attr", "kind", "channel", "campaign", "day", "week", "month"] as const)));
      expectShown(formatWait(n));
      expectShown(keyLabel(s));
      expect(() => describeRule({ event: s, op: "gte", count: n, days: n })).not.toThrow();
      expect(() => describeRule({ attr: s, op: s, value: s } as never)).not.toThrow();
      expect(() => guessTarget(s)).not.toThrow();
      expect(() => periodPresets(s)).not.toThrow();
      expect(() => addDays(s, n)).not.toThrow();
      expect(() => isSafeUrl(s)).not.toThrow();
    }
  });
});
