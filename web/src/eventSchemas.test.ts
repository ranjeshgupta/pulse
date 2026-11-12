import { describe, expect, it } from "vitest";
import type { SchemaProp } from "./api";
import {
  newEventProblem, copyProps, valuesText, parseValues,
  cleanProps, dependsSentence, exampleProps, fillDays, formatShare, guessType, keyFromPath, mergeProperties, modeLabel, moveProp,
  problemWords, propFromDiscovered, validateSchema, validListField, validPath,
} from "./eventSchemas";

const schema = (props: SchemaProp[], extra: object = {}) => ({ label: "", description: "", mode: "warn" as const, props, ...extra });
const method: SchemaProp = { key: "method", type: "enum", values: [{ value: "social", label: "Social" }, { value: "otp", label: "OTP" }] };
const provider: SchemaProp = { key: "social_provider", type: "enum", values: [{ value: "google" }], depends_on: { prop: "method", values: ["social"] } };

describe("validPath", () => {
  it("accepts names, dotted paths and one list", () => {
    for (const p of ["product_code", "product.code", "items[].code", "a.b.c.d", "items[].product.code", "_x"]) expect(validPath(p), p).toBe(true);
  });
  it("refuses bad paths", () => {
    for (const p of ["", "1abc", "a.b.c.d.e", "a[].b[].c", "a..b", "a-b", "items[]x", "a.".padEnd(130, "a")]) expect(validPath(p), p).toBe(false);
  });
});

describe("validateSchema", () => {
  it("passes a good schema", () => {
    expect(validateSchema(schema([method, provider, { key: "item_code", path: "items[].code", type: "string", add_to_list: "products" }]))).toEqual([]);
  });
  it("catches bad keys, duplicate keys and duplicate paths", () => {
    const p = validateSchema(schema([
      { key: "1bad", type: "string" },
      { key: "a", type: "string" },
      { key: "a", type: "number" },
      { key: "b", path: "a", type: "string" },
    ]));
    expect(p.some((x) => x.startsWith("Property 1: the key"))).toBe(true);
    expect(p).toContain("“a” is listed twice.");
    expect(p.some((x) => x.includes("already reads a"))).toBe(true);
  });
  it("checks paths and types", () => {
    const p = validateSchema(schema([{ key: "x", path: "a[].b[].c", type: "string" }, { key: "y", type: "weird" as never }]));
    expect(p.some((x) => x.includes("the path must be"))).toBe(true);
    expect(p).toContain("“y”: choose a type.");
  });
  it("checks enum values", () => {
    expect(validateSchema(schema([{ key: "e", type: "enum", values: [] }]))[0]).toMatch(/between 1 and 200/);
    expect(validateSchema(schema([{ key: "e", type: "enum", values: [{ value: "a" }, { value: " a " }] }]))[0]).toMatch(/different/);
    expect(validateSchema(schema([{ key: "e", type: "enum", values: [{ value: "" }] }]))[0]).toMatch(/filled in/);
    expect(validateSchema(schema([{ key: "s", type: "string", values: [{ value: "a" }] }]))[0]).toMatch(/only/i);
  });
  it("checks list fields", () => {
    expect(validateSchema(schema([{ key: "t", type: "string", add_to_list: "tags" }]))[0]).toMatch(/can't be a list field/);
    expect(validateSchema(schema([{ key: "t", type: "list", add_to_list: "products" }]))[0]).toMatch(/single values/);
    expect(validListField("company_size")).toBe(false);
    expect(validListField("products")).toBe(true);
  });
  it("checks dependencies", () => {
    expect(validateSchema(schema([{ ...provider, depends_on: { prop: "nope", values: ["x"] } }]))[0]).toMatch(/choose which property/);
    expect(validateSchema(schema([method, { ...provider, depends_on: { prop: "method", values: [] } }]))[0]).toMatch(/tick the values/);
    const chained = validateSchema(schema([method, provider, { key: "z", type: "string", depends_on: { prop: "social_provider", values: ["google"] } }]));
    expect(chained[0]).toMatch(/one level/);
    const listy = validateSchema(schema([method, { key: "c", path: "items[].c", type: "string", depends_on: { prop: "method", values: ["social"] } }]));
    expect(listy[0]).toMatch(/items\[\]/);
  });
  it("checks mode and lengths", () => {
    expect(validateSchema(schema([], { mode: "loud" }))[0]).toMatch(/mode/);
    expect(validateSchema(schema([], { label: "x".repeat(121) }))[0]).toMatch(/label is too long/);
  });
});

describe("keyFromPath and discovered properties", () => {
  it("derives keys", () => {
    expect(keyFromPath("product_code")).toBe("product_code");
    expect(keyFromPath("product.code")).toBe("product_code");
    expect(keyFromPath("items[].code")).toBe("item_code");
    expect(keyFromPath("items[].product_code")).toBe("item_product_code");
    expect(keyFromPath("address[].city")).toBe("address_city");
  });
  it("guesses types and prefills", () => {
    expect(guessType("mixed")).toBe("string");
    expect(guessType("bool")).toBe("bool");
    expect(propFromDiscovered({ path: "items[].code", type: "string", share: 1, examples: [] })).toEqual({ key: "item_code", path: "items[].code", type: "string" });
    expect(propFromDiscovered({ path: "price", type: "number", share: 1, examples: [] })).toEqual({ key: "price", path: undefined, type: "number" });
  });
});

describe("mergeProperties", () => {
  it("puts schema properties first, then undescribed ones by share", () => {
    const rows = mergeProperties(
      [
        { path: "a", type: "string", share: 0.2, examples: [] },
        { path: "items[].code", type: "string", share: 0.9, examples: ["X"] },
        { path: "b", type: "number", share: 0.5, examples: [] },
      ],
      [{ key: "item_code", path: "items[].code", type: "string" }, { key: "never", type: "bool" }],
    );
    expect(rows.map((r) => [r.path, r.described, r.seen])).toEqual([
      ["items[].code", true, true], ["never", true, false], ["b", false, true], ["a", false, true],
    ]);
  });
});

describe("labels", () => {
  it("names modes and problems", () => {
    expect(modeLabel(undefined)).toBe("Not described");
    expect(modeLabel("enforce")).toBe("Enforce");
    expect(problemWords("enum")).toBe("not an allowed value");
    expect(problemWords("not_applicable")).toBe("sent when it doesn't apply");
    expect(formatShare(0.004)).toBe("<1%");
    expect(formatShare(0.456)).toBe("46%");
  });
  it("reads dependencies as a sentence", () => {
    expect(dependsSentence(provider, [{ ...method, label: "Method" }, provider])).toBe("Only when Method is Social");
    expect(dependsSentence({ ...provider, depends_on: { prop: "method", values: ["social", "otp"] } }, [method])).toBe("Only when method is Social or OTP");
    expect(dependsSentence(method, [])).toBe("Always applies");
  });
});

describe("fillDays", () => {
  it("returns every day, oldest first, with zeros", () => {
    const out = fillDays([{ day: "2026-11-10", count: 5 }, { day: "2026-10-01", count: 9 }], 30, new Date("2026-11-11T20:00:00Z"));
    expect(out).toHaveLength(30);
    expect(out[0].day).toBe("2026-10-13");
    expect(out[29]).toEqual({ day: "2026-11-11", count: 0 });
    expect(out[28]).toEqual({ day: "2026-11-10", count: 5 });
    expect(out.reduce((s, d) => s + d.count, 0)).toBe(5);
  });
});

describe("exampleProps", () => {
  const today = new Date("2026-11-11T00:00:00Z");
  it("gives a value per type", () => {
    expect(exampleProps({ props: [
      { key: "s", type: "string" }, { key: "n", type: "number" }, { key: "b", type: "bool" }, { key: "d", type: "date" },
      { key: "l", type: "list" }, { key: "e", type: "enum", values: [{ value: "upi" }, { value: "card" }] }, { key: "o", type: "object" },
    ] }, today)).toEqual({ s: "SKU-1", n: 499, b: true, d: "2026-11-11", l: ["A1"], e: "upi", o: {} });
  });
  it("nests dotted and items[] paths", () => {
    expect(exampleProps({ props: [
      { key: "product_code", path: "product.code", type: "string" },
      { key: "product", type: "object" },
      { key: "items", type: "list" },
      { key: "item_code", path: "items[].code", type: "string" },
      { key: "item_qty", path: "items[].qty", type: "number" },
    ] }, today)).toEqual({ product: { code: "SKU-1" }, items: [{ code: "SKU-1", qty: 499 }] });
  });
  it("includes dependent properties only when they apply", () => {
    expect(exampleProps({ props: [method, provider] }, today)).toEqual({ method: "social", social_provider: "google" });
    const otpFirst = { ...method, values: [{ value: "otp" }, { value: "social" }] };
    expect(exampleProps({ props: [otpFirst, provider] }, today)).toEqual({ method: "otp" });
  });
  it("skips invalid paths", () => {
    expect(exampleProps({ props: [{ key: "x", path: "a[].b[].c", type: "string" }] }, today)).toEqual({});
  });
});

describe("editor helpers", () => {
  it("moves properties", () => {
    expect(moveProp([1, 2, 3], 0, 1)).toEqual([2, 1, 3]);
    expect(moveProp([1, 2, 3], 0, -1)).toEqual([1, 2, 3]);
  });
  it("cleans properties for saving", () => {
    expect(cleanProps([
      { key: " a ", path: "a", label: " ", type: "string", values: [], add_to_list: "", depends_on: null },
      { key: "e", type: "enum", values: [{ value: " x ", label: "" }], required: true },
    ])).toEqual([{ key: "a", type: "string" }, { key: "e", type: "enum", required: true, values: [{ value: "x" }] }]);
  });
});

describe("newEventProblem", () => {
  const known = [{ name: "purchase", schema: true }, { name: "webinar_joined", schema: false }];
  it("accepts names the server accepts", () => {
    expect(newEventProblem("", known)).toBe("");
    expect(newEventProblem("webinar_joined", known)).toBe("");
    expect(newEventProblem("app:Opened.v2-x", known)).toBe("");
  });
  it("explains bad or taken names", () => {
    expect(newEventProblem("webinar joined", known)).toMatch(/No spaces/);
    expect(newEventProblem("1st_login", known)).toMatch(/Start with a letter/);
    expect(newEventProblem("a".repeat(65), known)).toMatch(/64/);
    expect(newEventProblem("purchase", known)).toMatch(/already described/);
  });
});

describe("New event properties", () => {
  it("copies another event's properties once, keeping their details", () => {
    const cart: SchemaProp[] = [
      { key: "price", type: "number" },
      { key: "payment_method", type: "enum", values: [{ value: "upi", label: "UPI" }, { value: "card" }] },
      { key: "gateway", type: "string", depends_on: { prop: "payment_method", values: ["card"] } },
    ];
    const out = copyProps([{ key: "price", type: "string" }], cart);
    expect(out.map((p) => [p.key, p.type])).toEqual([["price", "string"], ["payment_method", "enum"], ["gateway", "string"]]);
    expect(out[2].depends_on?.prop).toBe("payment_method");
    out[1].values![0].value = "x";
    expect(cart[1].values![0].value).toBe("upi");
  });
  it("drops a dependency on a property that isn't there", () => {
    const out = copyProps([], [{ key: "gateway", type: "string", depends_on: { prop: "payment_method", values: ["card"] } }]);
    expect(out[0].depends_on).toBeNull();
  });
  it("edits allowed values as text and keeps their labels", () => {
    const old = [{ value: "upi", label: "UPI" }, { value: "card" }];
    expect(valuesText(old)).toBe("upi, card");
    expect(parseValues(" upi ,card,, wallet", old)).toEqual([{ value: "upi", label: "UPI" }, { value: "card" }, { value: "wallet" }]);
  });
});
