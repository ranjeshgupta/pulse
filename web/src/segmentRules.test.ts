import { describe as suite, expect, it } from "vitest";
import type { Rule } from "./api";
import { buildCatalog } from "./segmentCatalog";
import { describe, eventProps, formatDay, fromRule, isDateAttr, newAttr, newGroup, problems, rootFromRule, segmentRefs, toRule, type Group } from "./segmentRules";

const catalog = Object.fromEntries(buildCatalog().map((it) => [it.id, it]));

const rule: Rule = {
  all: [
    { attr: "city", op: "in", value: ["Pune", "Mumbai"] },
    { attr: "age", op: "gte", value: 18 },
    { any: [{ event: "purchase", op: "gte", count: 2, days: 30 }, { event: "login", op: "eq", count: 0, days: 14 }] },
  ],
};

suite("segment rule editing model", () => {
  it("round-trips the API rule", () => {
    expect(toRule(fromRule(rule))).toEqual(rule);
  });

  it("represents 'never did' as count 0 and back", () => {
    const g = rootFromRule(rule);
    const inner = g.children[2] as Group;
    expect(inner.children[1]).toMatchObject({ kind: "event", op: "none" });
    expect(toRule(inner.children[1])).toEqual({ event: "login", op: "eq", count: 0, days: 14 });
  });

  it("wraps a single condition in a group", () => {
    const g = rootFromRule({ attr: "plan", op: "eq", value: "pro" });
    expect(g.kind).toBe("group");
    expect(g.children).toHaveLength(1);
  });

  it("sends numbers for numeric comparisons and lists for 'is one of'", () => {
    expect(toRule({ kind: "attr", attr: "age", op: "gt", value: "30" })).toEqual({ attr: "age", op: "gt", value: 30 });
    expect(toRule({ kind: "attr", attr: "city", op: "in", value: "Pune, , Delhi " })).toEqual({ attr: "city", op: "in", value: ["Pune", "Delhi"] });
  });

  it("reports what must be fixed", () => {
    expect(problems(newGroup())).toEqual(["A group needs at least one filter."]);
    expect(problems(newGroup("all", [newAttr()]))).toEqual(["Enter an attribute name, e.g. city.", '"attribute" needs a value (or choose "is empty").']);
    expect(problems(newAttr("city", "eq", " "))).toEqual(['"City" needs a value (or choose "is empty").']);
    expect(problems(newAttr("city", "empty"))).toEqual([]);
    expect(problems({ kind: "attr", attr: "age", op: "gt", value: "old" })).toEqual(['"age" needs a number.']);
    expect(problems({ kind: "event", event: "buy", op: "gte", count: 1, days: 400, where: [] })).toContain("Event windows are 1 to 365 days.");
    expect(problems(rootFromRule(rule))).toEqual([]);
  });

  it("round-trips segment, tag and property-filter conditions", () => {
    const r: Rule = {
      any: [
        { segment: 4, op: "not_in" },
        { tag: "vip", op: "has" },
        { event: "purchase", op: "gte", count: 1, days: 7, where: [{ prop: "amount", op: "gte", value: 100 }, { prop: "sku", op: "in", value: ["A1", "B2"] }] },
      ],
    };
    expect(toRule(fromRule(r))).toEqual(r);
    expect(segmentRefs({ all: [r, { segment: 40, op: "in" }] })).toEqual([4, 40]);
    expect(describe(r, { 4: "Churned" })).toBe("not in “Churned” or tagged vip or did purchase where amount is at least 100 and sku is one of A1, B2 at least 1× in 7 days");
    expect(problems({ kind: "segment", segment: 0, op: "in" })).toEqual(["Choose a segment or list."]);
    expect(problems({ kind: "tag", tag: "a b", op: "has" })).toHaveLength(1);
    expect(problems({ kind: "event", event: "buy", op: "gte", count: 1, days: 7, where: [{ prop: "amount", op: "gt", value: "lots" }] })).toEqual(['"amount" needs a number.']);
    // No filters: the rule has no "where" key, so older rules stay byte-identical.
    expect(toRule({ kind: "event", event: "buy", op: "gte", count: 1, days: 7, where: [] })).toEqual({ event: "buy", op: "gte", count: 1, days: 7 });
  });

  it("describes rules in words", () => {
    expect(describe(rule)).toBe("city is one of Pune, Mumbai and age is at least 18 and (did purchase at least 2× in 30 days or never did login in 14 days)");
  });
});

suite("date comparisons", () => {
  it("sends dates as text and day counts as numbers, and round-trips them", () => {
    const r: Rule = {
      all: [
        { attr: "signup_date", op: "before", value: "2026-01-31" },
        { attr: "first_seen", op: "after", value: "2025-12-01" },
        { attr: "last_seen", op: "within_days", value: 7 },
        { attr: "renewal", op: "older_than_days", value: 90 },
      ],
    };
    expect(toRule(fromRule(r))).toEqual(r);
    expect(toRule({ kind: "attr", attr: "last_seen", op: "within_days", value: "14" })).toEqual({ attr: "last_seen", op: "within_days", value: 14 });
    expect(problems(rootFromRule(r))).toEqual([]);
  });

  it("describes date comparisons in plain words", () => {
    expect(describe({ attr: "signup_date", op: "before", value: "2026-01-31" })).toBe("signup_date is before 31 Jan 2026");
    expect(describe({ attr: "first_seen", op: "after", value: "2025-12-01" })).toBe("first seen is after 1 Dec 2025");
    expect(describe({ attr: "last_seen", op: "within_days", value: 30 })).toBe("last active is in the last 30 days");
    expect(describe({ attr: "last_seen", op: "within_days", value: 1 })).toBe("last active is in the last day");
    expect(describe({ attr: "renewal", op: "older_than_days", value: 90 })).toBe("renewal is more than 90 days ago");
    expect(describe({ attr: "geo_country", op: "eq", value: "India" })).toBe("country is India");
  });

  it("checks dates and day counts", () => {
    expect(problems({ kind: "attr", attr: "d", op: "before", value: "2026-02-30" })).toEqual(['"d" needs a date.']);
    expect(problems({ kind: "attr", attr: "d", op: "after", value: "" })).toEqual(['"d" needs a date.']);
    expect(problems({ kind: "attr", attr: "d", op: "within_days", value: "0" })).toEqual(['"d" needs a whole number of days, 1 to 3650.']);
    expect(problems({ kind: "attr", attr: "d", op: "older_than_days", value: "2.5" })).toHaveLength(1);
    expect(problems({ kind: "attr", attr: "d", op: "older_than_days", value: "3650" })).toEqual([]);
    // Event properties can be before/after a date, but not relative to today.
    const ev = (op: "after" | "within_days", value: string) => problems({ kind: "event", event: "purchase", op: "gte", count: 1, days: 30, where: [{ prop: "shipped", op, value }] });
    expect(ev("after", "2026-03-01")).toEqual([]);
    expect(ev("within_days", "7")[0]).toMatch(/Use "is before" or "is after" on "shipped"/);
  });

  it("knows which attributes hold dates", () => {
    expect(isDateAttr("first_seen")).toBe(true);
    expect(isDateAttr(" last_seen ")).toBe(true);
    expect(isDateAttr("renewal", [{ key: "renewal", type: "date" }])).toBe(true);
    expect(isDateAttr("city", [{ key: "city", type: "string" }])).toBe(false);
  });

  it("formats days", () => {
    expect([formatDay("2026-11-05"), formatDay("2026-13-01"), formatDay("soon")]).toEqual(["5 Nov 2026", "2026-13-01", "soon"]);
  });
});

suite("known events and presets", () => {
  const refs = { forms: { 3: "Contact us" }, broadcasts: { 5: "Diwali sale" }, journeys: { 8: "Onboarding" } };

  it("describes Pulse's own events like a marketer would", () => {
    expect(describe({ event: "email_opened", op: "gte", count: 1, days: 30, where: [{ prop: "broadcast_id", op: "eq", value: "5" }] }, {}, refs))
      .toBe("opened an email from campaign “Diwali sale” in the last 30 days");
    expect(describe({ event: "email_clicked", op: "eq", count: 0, days: 14, where: [{ prop: "journey_id", op: "eq", value: "8" }] }, {}, refs))
      .toBe("didn't click a link in an email from journey “Onboarding” in the last 14 days");
    expect(describe({ event: "form_submitted", op: "gte", count: 2, days: 90, where: [{ prop: "form_id", op: "eq", value: "3" }] }, {}, refs))
      .toBe("submitted the form “Contact us” at least 2 times in the last 90 days");
    expect(describe({ event: "journey_completed", op: "gte", count: 1, days: 1, where: [{ prop: "journey_id", op: "eq", value: "9" }] }, {}, refs))
      .toBe("completed the journey #9 in the last day");
    expect(describe({ event: "page_view", op: "gte", count: 1, days: 7, where: [{ prop: "url", op: "contains", value: "/pricing" }] }))
      .toBe("visited a page whose URL contains /pricing in the last 7 days");
    expect(describe({ event: "unsubscribed", op: "gte", count: 1, days: 365, where: [{ prop: "channel", op: "eq", value: "whatsapp" }] }))
      .toBe("unsubscribed from WhatsApp in the last 365 days");
    expect(describe({ event: "whatsapp_read", op: "gte", count: 1, days: 30, where: [{ prop: "broadcast_id", op: "ne", value: 5 }] }, {}, refs))
      .toBe("read a WhatsApp message not from campaign “Diwali sale” in the last 30 days");
    expect(describe({ event: "email_bounced", op: "gte", count: 1, days: 90 })).toBe("had an email bounce in the last 90 days");
  });

  it("offers catalog filters that insert valid rules once their blanks are filled", () => {
    expect(toRule(catalog["field:last_seen"].make())).toEqual({ attr: "last_seen", op: "within_days", value: 30 });
    expect(problems(catalog["field:last_seen"].make())).toEqual([]);
    expect(problems(catalog.email_bounce_event.make())).toEqual([]);
    expect(problems(catalog.unsub_event.make())).toEqual([]);
    // Pickers start empty and say what to choose.
    expect(problems(catalog.form.make())).toEqual(["Choose a form."]);
    expect(problems(catalog.email_opened_campaign.make())).toEqual(["Choose a campaign."]);
    expect(problems(catalog.email_read_specific.make())).toEqual(["Choose an email."]);
    expect(problems(catalog.journey_entered.make())).toEqual(["Choose a journey."]);
    expect(problems(catalog.asset.make())).toEqual(["Choose an asset."]);
    expect(problems(catalog.page_url.make())).toEqual(['"url" needs some text to look for.']);
    expect(toRule(catalog.didnt.make())).toMatchObject({ op: "eq", count: 0 });
  });

  it("suggests the properties each event carries", () => {
    expect(eventProps("page_view").slice(0, 3)).toEqual(["url", "path", "title"]);
    expect(eventProps("page_view")).toContain("utm_source");
    expect(eventProps("sms_delivered")).toContain("broadcast_id");
    expect(eventProps("purchase")).toEqual([]);
  });
});

suite("stage and company conditions", () => {
  it("offers stage and company filters that need a choice", () => {
    expect(catalog["field:stage"].make()).toEqual({ kind: "attr", attr: "stage", op: "eq", value: "" });
    expect(problems(catalog["field:stage"].make())).toEqual(["Choose a stage."]);
    expect(problems(catalog["field:company_id"].make())).toEqual(["Choose a company."]);
    expect(problems(catalog["field:owner_id"].make())).toEqual(["Choose an owner."]);
    expect(problems(catalog.company_attr.make())[0]).toBe("Finish the company attribute's name, e.g. company_industry.");
    expect(problems(newAttr("company_industry", "eq", "Retail"))).toEqual([]);
    expect(problems(catalog.stage_recent.make())).toEqual([]);
    expect(isDateAttr("stage_changed_at")).toBe(true);
  });

  it("describes stages and companies in words", () => {
    expect(describe({ attr: "stage", op: "eq", value: "MQL" })).toBe("in stage “MQL”");
    expect(describe({ attr: "stage", op: "ne", value: "Customer" })).toBe("not in stage “Customer”");
    expect(describe({ attr: "company_id", op: "eq", value: "7" }, {}, { companies: { 7: "Acme" } })).toBe("works at “Acme”");
    expect(describe({ attr: "company_id", op: "ne", value: "8" })).toBe("doesn't work at company #8");
    expect(describe({ attr: "company_id", op: "exists" })).toBe("works at a company");
    expect(describe({ attr: "company_annual_revenue", op: "gte", value: 1000 })).toBe("company annual revenue is at least 1000");
    expect(describe({ attr: "stage_changed_at", op: "within_days", value: 7 })).toBe("stage changed is in the last 7 days");
  });
});
