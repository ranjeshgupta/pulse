import { describe, expect, it } from "vitest";
import { buildTag, contactVars, eventVars, exampleEvent, fillSample, findRefs, insertAt, lint, lintAll, PIPES, SAMPLE_CONTACT } from "./personalize";
import { channelRuleProblems, rulesText } from "./journeyGraph";
import type { EventSchema } from "./api";

const cart: Pick<EventSchema, "props"> = {
  props: [
    { key: "cart_id", type: "string" },
    { key: "total", type: "number", label: "Cart total" },
    { key: "item_name", path: "items[].name", type: "string", label: "Item" },
    { key: "card_last4", type: "string", personal: true },
  ],
};

describe("catalog", () => {
  it("lists built-in and workspace contact fields once", () => {
    const vs = contactVars([{ key: "first_name", label: "First", type: "string" }, { key: "plan", label: "Plan", type: "string" }, { key: "bad-key", label: "x", type: "string" }]);
    expect(vs.filter((v) => v.var === "contact.first_name")).toHaveLength(1);
    expect(vs.map((v) => v.var)).toContain("contact.plan");
    expect(vs.map((v) => v.var)).not.toContain("contact.bad-key");
    expect(vs.map((v) => v.var)).toContain("contact.phone_country");
  });
  it("offers event properties, list items both ways, never personal ones", () => {
    const vs = eventVars(cart).map((v) => v.var);
    expect(vs).toEqual(["event.cart_id", "event.total", "event.items.0.name", "event.items[].name"]);
  });
  it("keeps the pipe list in step with the server", () => {
    expect(PIPES.map((p) => p.name).sort().join(",")).toBe("date,default,json,lower,money,number,title,truncate,upper,urlencode");
  });
});

describe("tags", () => {
  it("builds tags with defaults and formats", () => {
    expect(buildTag("contact.first_name", [{ name: "default", arg: "there" }, { name: "title" }])).toBe('{{contact.first_name | default:"there" | title}}');
    expect(buildTag("event.title", [{ name: "truncate", arg: "40" }])).toBe("{{event.title | truncate:40}}");
    expect(buildTag("event.total", [{ name: "money", arg: "INR" }])).toBe('{{event.total | money:"INR"}}');
  });
  it("finds standard tags, decoding HTML attributes, and ignores the older syntax", () => {
    const refs = findRefs('Hi {{ contact.first_name | default:"there" }} {{.Attrs.city}} <a title="{{contact.x | default:&quot;y&quot;}}">');
    expect(refs.map((r) => r.var)).toEqual(["contact.first_name", "contact.x"]);
    expect(refs[1].pipes).toEqual([{ name: "default", arg: "y" }]);
  });
  it("inserts at the cursor", () => {
    expect(insertAt("Hi !", "{{contact.first_name}}", 3, 3)).toEqual({ value: "Hi {{contact.first_name}}!", cursor: 25 });
    expect(insertAt("Hi", "X").value).toBe("HiX");
  });
});

describe("lint", () => {
  const known = { contact: contactVars([]), event: eventVars(cart), channel: "sms" };
  it("warns about unknown fields and properties, errors on bad syntax", () => {
    const ps = lint('{{contact.nickname}} {{event.coupon}} {{event.items.0.name}} {{contact.first_name | shout}} {{campaign.owner}} {{links.unsubscribe}} {{contact.x | truncate}}', known);
    expect(ps.map((p) => `${p.level}:${p.var}`)).toEqual([
      "warning:contact.nickname", "warning:event.coupon", "error:contact.first_name", "error:campaign.owner", "warning:links.unsubscribe", "error:contact.x",
    ]);
    expect(ps[2].message).toMatch(/Unknown format “shout”/);
  });
  it("doesn't check event properties without a schema, and dedupes across fields", () => {
    expect(lint("{{event.anything}}", { contact: contactVars([]), event: null })).toEqual([]);
    expect(lintAll(["{{contact.nick}}", "{{contact.nick}}"], known)).toHaveLength(1);
  });
});

describe("sample fill", () => {
  const sample = { contact: SAMPLE_CONTACT, event: exampleEvent(cart), campaign: { name: "Cart rescue" } };
  it("fills both syntaxes with pipes", () => {
    expect(fillSample('Hi {{contact.first_name | upper}} ({{.Attrs.city}}), {{event.items.0.name}} for {{event.total | money}} in {{campaign.name}}', sample))
      .toBe("Hi ASHA (Pune), Sample Item for ₹1,299.00 in Cart rescue");
    expect(fillSample('{{contact.nickname | default:"friend" | title}} {{event.items[].name}} {{event.cart_id | truncate:5}}', sample)).toBe("Friend Sample Item Samp…");
    expect(fillSample("{{event.total | number}} {{contact.city | urlencode}}", { contact: { city: "Navi Mumbai" }, event: { total: 1234567 } })).toBe("1,234,567 Navi+Mumbai");
  });
  it("leaves broadcasts' event variables empty", () => {
    expect(fillSample("[{{event.cart_id}}]", { contact: SAMPLE_CONTACT })).toBe("[]");
  });
  it("builds an example event from a schema without personal properties", () => {
    expect(exampleEvent(cart)).toEqual({ cart_id: "Sample cart_id", total: 1299, items: [{ name: "Sample Item" }] });
  });
});

describe("send_message rules", () => {
  const rules = [
    { channel: "sms" as const, template_id: 1, when: [{ prop: "contact.phone_country", op: "eq", value: "IN" }] },
    { channel: "whatsapp" as const, template_id: 2 },
  ];
  it("describes rules", () => {
    expect(rulesText(rules, (c) => c.toUpperCase())).toBe("phone_country is IN → SMS; otherwise WHATSAPP");
    expect(rulesText([{ channel: "push" }, { channel: "email" }], (c) => c)).toBe("Send by push, then email");
  });
  it("checks rules as the server does", () => {
    expect(channelRuleProblems(rules)).toEqual([]);
    expect(channelRuleProblems([{ channel: "sms", template_id: 1, when: [{ prop: "phone_country", op: "eq", value: "IN" }] }])[0]).toMatch(/contact.<field>/);
    expect(channelRuleProblems([{ channel: "sms", template_id: 1 }, { channel: "sms", template_id: 2 }])[0]).toMatch(/each channel once/);
    expect(channelRuleProblems([{ channel: "sms", template_id: 1, when: [{ prop: "contact.phone_country", op: "eq", value: "" }] }])[0]).toMatch(/enter the value/);
    // The same channel twice is fine when conditions tell the rules apart.
    expect(channelRuleProblems([{ ...rules[0] }, { channel: "sms", template_id: 3 }])).toEqual([]);
  });
});

describe("exampleEvent with a list and a path through it", () => {
  it("uses sample objects when a property goes through the list", () => {
    const ev = exampleEvent({ props: [
      { key: "items", label: "Items", type: "list" },
      { key: "item_product_code", path: "items[].product_code", label: "Item product code", type: "string" },
    ] } as never);
    expect(ev.items).toEqual([{ product_code: "Sample Item product code" }]);
  });
});
