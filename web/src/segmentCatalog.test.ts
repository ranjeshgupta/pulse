import { describe as suite, expect, it } from "vitest";
import type { Rule } from "./api";
import { buildCatalog, groupItems, GROUPS, MAX_RECENT, pushRecent, searchCatalog } from "./segmentCatalog";
import {
  canNest, describe, fromRule, MAX_GROUP_LEVELS, MAX_RULE_DEPTH, newAttr, newGroup, nodeAt, opsFor, problems, rootFromRule, toRule,
  treeReducer, valueShape, type Group,
} from "./segmentRules";

const src = {
  defs: [
    { key: "mfi_subscription_end", type: "date", label: "MFI Subscription End Date" },
    { key: "is_fund_subscriber", type: "boolean", label: "User is Fund Subscriber" },
    { key: "aum", type: "number" },
    { key: "company_industry", type: "string" },
  ],
  seen: ["plan", "city", "company_size", "tags", "aum"],
};
const items = buildCatalog(src);
const byId = Object.fromEntries(items.map((it) => [it.id, it]));

suite("filter catalog", () => {
  it("lists every workspace attribute once, typed and grouped", () => {
    expect(new Set(items.map((i) => i.id)).size).toBe(items.length);
    expect(byId["field:mfi_subscription_end"]).toMatchObject({ group: "contact", type: "date", custom: true, label: "MFI Subscription End Date" });
    expect(byId["field:is_fund_subscriber"]).toMatchObject({ type: "boolean", custom: true });
    expect(byId["field:aum"]).toMatchObject({ type: "number" });
    expect(byId["field:plan"]).toMatchObject({ type: "text", label: "Plan", hint: "Seen on contacts · text" });
    expect(byId["field:company_industry"].group).toBe("company");
    expect(byId["field:company_size"].group).toBe("company");
    // Pulse's own fields aren't listed twice, and tags have their own filter.
    expect(items.filter((i) => i.key === "city")).toHaveLength(1);
    expect(byId["field:tags"]).toBeUndefined();
  });

  it("starts each field with an operator and value suited to its type", () => {
    expect(toRule(byId["field:mfi_subscription_end"].make())).toEqual({ attr: "mfi_subscription_end", op: "within_days", value: 30 });
    expect(toRule(byId["field:is_fund_subscriber"].make())).toEqual({ attr: "is_fund_subscriber", op: "eq", value: true });
    expect(byId["field:aum"].make()).toMatchObject({ op: "gte" });
    expect(byId["field:plan"].make()).toMatchObject({ op: "eq", value: "" });
  });

  it("searches labels, hints, attribute keys and group names", () => {
    const ids = (q: string) => searchCatalog(items, q).map((i) => i.id);
    expect(ids("mfi")).toEqual(["field:mfi_subscription_end"]);
    expect(ids("subscription_end")).toEqual(["field:mfi_subscription_end"]);
    expect(ids("read email")).toContain("email_read_specific");
    expect(ids("device")).toEqual(expect.arrayContaining(["field:device_type", "field:device_model", "field:browser"]));
    expect(ids("BOUNCED sms")).toContain("field:sms_bounced");
    expect(ids("utm")).toEqual(expect.arrayContaining(["field:last_utm_source", "page_source"]));
    expect(ids("zzz nothing")).toEqual([]);
    expect(searchCatalog(items, "  ")).toHaveLength(items.length);
  });

  it("groups in picker order with recently used first", () => {
    const g = groupItems(items, ["form", "nope", "field:points"]);
    expect(g[0]).toMatchObject({ id: "recent", label: "Recently used" });
    expect(g[0].items.map((i) => i.id)).toEqual(["form", "field:points"]);
    expect(g.slice(1).map((x) => x.id)).toEqual(GROUPS.map((x) => x.id));
    expect(groupItems(items)[0].id).toBe("contact");
    expect(pushRecent(["a", "b", "c"], "b")).toEqual(["b", "a", "c"]);
    expect(pushRecent(["1", "2", "3", "4", "5", "6"], "7")).toHaveLength(MAX_RECENT);
  });

  it("maps every Mautic filter to a picker entry", () => {
    const mautic = [
      "field:address1", "field:attribution", "field:attribution_date", "field:email_bounced", "field:sms_bounced", "journey_entered",
      "field:city", "field:country", "field:date_added", "field:date_identified", "field:last_seen", "field:device_brand", "field:device_model",
      "field:device_os", "field:device_type", "field:email", "field:email_domain", "field:first_name", "field:last_name", "field:phone",
      "field:date_modified", "field:owner_id", "field:points", "field:locale", "segment", "field:stage", "field:state", "tag",
      "field:last_utm_campaign", "field:last_utm_content", "field:last_utm_medium", "field:last_utm_source", "field:last_utm_term",
      "field:optout_email", "field:optout_sms", "unsub_manual_email", "unsub_manual_sms", "field:zip", "field:company_name",
      "clicked_any_channel", "email_clicked_any", "sms_clicked_any", "asset", "sessions", "push_sent_any", "email_read_specific",
      "email_read_any", "email_read_times", "email_sent_any", "page_url", "landing_page", "page_any", "page_times", "page_referrer",
      "page_source", "page_source_id", "page_title", "email_sent_specific",
    ];
    for (const id of mautic) expect(byId[id], id).toBeDefined();
    // Every entry inserts a rule the backend accepts once its blanks are filled (or says what's missing).
    for (const it of items) expect(() => toRule(it.make()), it.id).not.toThrow();
  });

  it("builds combined filters as small groups", () => {
    expect(toRule(byId.unsub_manual_email.make())).toEqual({
      all: [
        { attr: "optout_email", op: "eq", value: true },
        { event: "unsubscribed", op: "gte", count: 1, days: 365, where: [{ prop: "channel", op: "eq", value: "email" }, { prop: "method", op: "eq", value: "manual" }] },
      ],
    });
    expect(toRule(byId.reachable_sms.make())).toEqual({
      all: [{ attr: "phone", op: "ne", value: "" }, { attr: "optout_sms", op: "ne", value: true }, { attr: "optout_all", op: "ne", value: true }, { attr: "sms_bounced", op: "ne", value: true }],
    });
    expect(problems(byId.clicked_any_channel.make())).toEqual([]);
    expect(byId.clicked_any_channel.nests).toBe(true);
  });
});

suite("operators per type", () => {
  it("offers type-specific operators", () => {
    expect(opsFor("text")).toEqual(["eq", "ne", "contains", "not_contains", "starts_with", "ends_with", "in", "not_in", "empty", "not_empty"]);
    expect(opsFor("number")).toContain("between");
    expect(opsFor("number")).not.toContain("contains");
    expect(opsFor("boolean")).toEqual(["eq", "ne", "exists", "not_exists"]);
    expect(opsFor("date")).toEqual(["within_days", "older_than_days", "on", "before", "after", "between", "empty", "not_empty"]);
    // Event properties can't compare with today.
    expect(opsFor("date", undefined, true)).not.toContain("within_days");
    // A saved op the type doesn't offer stays visible.
    expect(opsFor("boolean", "contains")).toContain("contains");
    expect([valueShape("between"), valueShape("not_in"), valueShape("empty"), valueShape("on"), valueShape("older_than_days")]).toEqual(["range", "list", "none", "date", "days"]);
  });

  it("round-trips the new operators", () => {
    const r: Rule = {
      all: [
        { attr: "city", op: "not_in", value: ["Pune", "Delhi, NCR"] },
        { attr: "aum", op: "between", value: [10, 50] },
        { attr: "email", op: "ends_with", value: "@valueresearch.in" },
        { attr: "renewal", op: "on", value: "2026-12-31" },
        { attr: "owner_id", op: "not_exists" },
        { attr: "phone", op: "eq", value: "" },
        { attr: "plan", op: "ne", value: "" },
        { event: "page_view", op: "gte", count: 1, days: 7, where: [{ prop: "title", op: "not_contains", value: "Login" }, { prop: "amount", op: "between", value: [1, 9] }] },
      ],
    };
    const g = rootFromRule(r);
    expect(g.children[0]).toMatchObject({ op: "not_in", values: ["Pune", "Delhi, NCR"] });
    expect(g.children[5]).toMatchObject({ op: "empty" });
    expect(g.children[6]).toMatchObject({ op: "not_empty" });
    expect(toRule(fromRule(r))).toEqual(r);
    expect(problems(g)).toEqual([]);
  });

  it("checks the new operators' values", () => {
    expect(problems({ kind: "attr", attr: "aum", op: "between", value: "50", value2: "10" })).toEqual(['"aum": put the smaller number first.']);
    expect(problems({ kind: "attr", attr: "aum", op: "between", value: "5", value2: "" })).toEqual(['"aum" needs two numbers.']);
    expect(problems({ kind: "attr", attr: "city", op: "not_in", value: "", values: [] })).toEqual(['"City": list at least one value.']);
    expect(problems({ kind: "attr", attr: "email", op: "starts_with", value: " " })).toEqual(['"Email" needs some text to look for.']);
    expect(problems({ kind: "attr", attr: "renewal", op: "on", value: "2026-02-30" })).toEqual(['"renewal" needs a date.']);
  });
});

suite("describing the new filters", () => {
  const refs = { users: { 4: "Ravi" }, templates: { 12: "Welcome" }, assets: { 3: "Fund guide" }, pages: { 9: "Diwali" } };
  it("reads like a sentence", () => {
    expect(describe({ attr: "owner_id", op: "eq", value: "4" }, {}, refs)).toBe("owned by “Ravi”");
    expect(describe({ attr: "owner_id", op: "not_exists" })).toBe("has no owner");
    expect(describe({ attr: "email_domain", op: "in", value: ["gmail.com", "yahoo.com"] })).toBe("email domain is one of gmail.com, yahoo.com");
    expect(describe({ attr: "device_type", op: "eq", value: "mobile" })).toBe("device type is mobile");
    expect(describe({ attr: "sms_bounced", op: "eq", value: true })).toBe("SMS bounced is yes");
    expect(describe({ attr: "optout_email", op: "ne", value: true })).toBe("unsubscribed from email is not yes");
    expect(describe({ attr: "date_added", op: "within_days", value: 7 })).toBe("date added is in the last 7 days");
    expect(describe({ attr: "aum", op: "between", value: [10, 50] })).toBe("aum is between 10 and 50");
    expect(describe({ attr: "phone", op: "eq", value: "" })).toBe("mobile is empty");
    expect(describe({ attr: "city", op: "not_contains", value: "x" })).toBe("city doesn't contain x");
    expect(describe({ attr: "renewal", op: "on", value: "2026-12-31" })).toBe("renewal is on 31 Dec 2026");
  });

  it("words behaviour filters", () => {
    expect(describe({ event: "email_opened", op: "gte", count: 1, days: 30, where: [{ prop: "template_id", op: "eq", value: "12" }] }, {}, refs))
      .toBe("opened the email “Welcome” in the last 30 days");
    expect(describe({ event: "email_clicked", op: "gte", count: 2, days: 30, where: [{ prop: "template_id", op: "eq", value: 12 }] }, {}, refs))
      .toBe("clicked a link in the email “Welcome” at least 2 times in the last 30 days");
    expect(describe({ event: "sms_clicked", op: "gte", count: 1, days: 7 })).toBe("clicked a link in an SMS in the last 7 days");
    expect(describe({ event: "session_start", op: "gte", count: 3, days: 30 })).toBe("started a browsing session at least 3 times in the last 30 days");
    expect(describe({ event: "asset_downloaded", op: "gte", count: 1, days: 90, where: [{ prop: "asset_id", op: "eq", value: "3" }] }, {}, refs))
      .toBe("downloaded the asset “Fund guide” in the last 90 days");
    expect(describe({ event: "landing_page_viewed", op: "gte", count: 1, days: 90, where: [{ prop: "page_id", op: "eq", value: "9" }] }, {}, refs))
      .toBe("visited the landing page “Diwali” in the last 90 days");
    expect(describe({ event: "unsubscribed", op: "gte", count: 1, days: 365, where: [{ prop: "channel", op: "eq", value: "sms" }, { prop: "method", op: "eq", value: "manual" }] }))
      .toBe("unsubscribed from SMS manually (by your team or API) in the last 365 days");
    expect(describe({ event: "page_view", op: "gte", count: 1, days: 30, where: [{ prop: "utm_source", op: "eq", value: "newsletter" }] }))
      .toBe("visited a page whose campaign source is newsletter in the last 30 days");
  });
});

suite("group nesting reducer", () => {
  const start: Group = newGroup("all", [newAttr("city", "eq", "Pune")]);

  it("adds, replaces, switches and removes by path", () => {
    let g = treeReducer(start, { type: "add", group: [], node: newGroup("any", [newAttr("plan", "eq", "pro")]) });
    g = treeReducer(g, { type: "add", group: [1], node: newAttr("points", "gte", "50") });
    expect(toRule(g)).toEqual({ all: [{ attr: "city", op: "eq", value: "Pune" }, { any: [{ attr: "plan", op: "eq", value: "pro" }, { attr: "points", op: "gte", value: 50 }] }] });
    g = treeReducer(g, { type: "match", group: [1], match: "all" });
    expect(nodeAt(g, [1])).toMatchObject({ match: "all" });
    g = treeReducer(g, { type: "replace", path: [1, 0], node: newAttr("vip", "eq", "true") });
    expect(toRule(nodeAt(g, [1, 0])!)).toEqual({ attr: "vip", op: "eq", value: true });
    // The input tree is never changed.
    expect(start.children).toHaveLength(1);
  });

  it("removes a nested group with its last filter, but keeps the top group", () => {
    let g = treeReducer(start, { type: "add", group: [], node: newGroup("any", [newGroup("all", [newAttr("a", "eq", "1")])]) });
    g = treeReducer(g, { type: "remove", path: [1, 0, 0] });
    expect(g.children).toHaveLength(1); // both now-empty groups went away
    g = treeReducer(g, { type: "remove", path: [0] });
    expect(g).toEqual({ kind: "group", match: "all", children: [] });
    expect(problems(g)).toEqual(["A group needs at least one filter."]);
  });

  it("limits nesting to the backend's depth", () => {
    expect(canNest([])).toBe(true);
    const deepest = Array(MAX_GROUP_LEVELS - 1).fill(0);
    expect(canNest(deepest.slice(1))).toBe(true);
    expect(canNest(deepest)).toBe(false);
    let n: Group = newGroup("all", [newAttr("a", "eq", "1")]);
    for (let i = 0; i < MAX_RULE_DEPTH - 1; i++) n = newGroup("all", [n]);
    expect(problems(n)).toContain(`Groups can be nested at most ${MAX_GROUP_LEVELS} levels deep.`);
  });

  it("builds the product owner's example", () => {
    // Pune contacts on mobile who read Welcome in 30 days, OR (VIP and points >= 50)
    let g = newGroup("any");
    g = treeReducer(g, { type: "add", group: [], node: newGroup("all") });
    g = treeReducer(g, { type: "add", group: [0], node: newAttr("geo_city", "eq", "Pune") });
    g = treeReducer(g, { type: "add", group: [0], node: newAttr("device_type", "eq", "mobile") });
    g = treeReducer(g, { type: "add", group: [0], node: { kind: "event", event: "email_opened", op: "gte", count: 1, days: 30, where: [{ prop: "template_id", op: "eq", value: "12" }] } });
    g = treeReducer(g, { type: "add", group: [], node: newGroup("all", [{ kind: "tag", tag: "vip", op: "has" }, newAttr("points", "gte", "50")]) });
    expect(problems(g)).toEqual([]);
    expect(describe(toRule(g), {}, { templates: { 12: "Welcome" } })).toBe(
      "(city (from IP) is Pune and device type is mobile and opened the email “Welcome” in the last 30 days) or (tagged vip and points is at least 50)");
  });
});

suite("workspace events in the picker", () => {
  it("offers each as a Did … filter, found by its name", () => {
    const items = buildCatalog({ defs: [], seen: [], events: [{ name: "cart_abandoned", label: "Cart abandoned" }, { name: "webinar_joined" }] });
    const found = searchCatalog(items, "abandon");
    expect(found.map((i) => i.label)).toContain("Did Cart abandoned");
    const node = items.find((i) => i.id === "event:webinar_joined")!.make();
    expect(node).toMatchObject({ kind: "event", event: "webinar_joined", days: 30 });
  });
});
