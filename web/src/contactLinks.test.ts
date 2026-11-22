import { describe, expect, it } from "vitest";
import type { Perms } from "./api";
import { CAMPAIGN_GOALS, campaignTypeOf, CATEGORIES, newCampaignHref, OLD_CAMPAIGN_LISTS, startTrigger } from "./campaigns";
import { isLinkFilter, splitQuery } from "./contactFilters";
import {
  activeFilters, addDays, audienceFilters, canListContacts, contactsHref, contactsQuery, estimateFilters, needKeys, outcomeFilters,
  overlapPairFilters, overlapSeesFilters, productFilters, whyNoLink,
} from "./contactLinks";
import { navActive } from "./Shell";

describe("contact list links", () => {
  it("joins filters into the list's search, quoting spaces", () => {
    expect(contactsQuery([["in", "4,9"], ["not_in", 11], ["reachable", "email"]])).toBe("in:4,9 not_in:11 reachable:email");
    expect(contactsHref([["event", "purchase"], ["product", "Blue shirt"]])).toBe(`/contacts?q=${encodeURIComponent('event:purchase product:"Blue shirt"')}`);
    expect(contactsHref([])).toBe("/contacts");
  });

  it("round-trips through the search box as removable chips", () => {
    const q = contactsQuery(outcomeFilters("broadcast", 4, "opened", { run: "1767225600" }));
    expect(splitQuery(q)).toEqual({ chips: ["campaign:broadcast/4", "run:1767225600", "outcome:opened"], text: "" });
    expect(splitQuery(q).chips.every(isLinkFilter)).toBe(true);
    expect(isLinkFilter("tag:vip")).toBe(false);
  });

  it("builds every audience estimate step", () => {
    expect(estimateFilters("audience", [4, 9], [11], "email")).toEqual([["in", "4,9"]]);
    expect(estimateFilters("exclude", [4], [11], "email")).toEqual([["in", "4"], ["not_in", "11"]]);
    expect(estimateFilters("consent", [4], [], "sms")).toEqual([["in", "4"], ["subscribed", "sms"]]);
    expect(estimateFilters("reach", [4], [11], "sms")).toEqual([["in", "4"], ["not_in", "11"], ["reachable", "sms"]]);
    expect(estimateFilters("cap", [4], [], "email")).toEqual([["in", "4"], ["reachable", "email"], ["under_cap", "email"]]);
  });

  it("builds product and users dashboard lists", () => {
    const ev = { view: "product_viewed", cart: "add_to_cart", purchase: "purchase" };
    expect(productFilters("abandoned", ev, { code: "SKU1" }, "2026-10-01", "2026-10-31"))
      .toEqual([["event", "add_to_cart"], ["no_event", "purchase"], ["product", "SKU1"], ["period", "2026-10-01..2026-10-31"]]);
    expect(productFilters("bought", ev, { category: "Shoes" }, "2026-10-01", "2026-10-01")).toEqual([["event", "purchase"], ["category", "Shoes"], ["period", "2026-10-01"]]);
    // The totals count only events naming a product, so their lists do too.
    expect(productFilters("bought", ev, {}, "2026-10-01", "2026-10-31")).toEqual([["event", "purchase"], ["product", "*"], ["period", "2026-10-01..2026-10-31"]]);
    expect(contactsHref([["event", "purchase"], ["product", "*"]])).toBe(`/contacts?q=${encodeURIComponent("event:purchase product:*")}`);
    expect(activeFilters("any", "2026-11-06", "2026-11-12")).toEqual([["active", "any"], ["period", "2026-11-06..2026-11-12"]]);
    expect(activeFilters("any", "2026-11-12", "2026-11-12", "web", "")).toEqual([["active", "web"], ["utm", "none"], ["period", "2026-11-12"]]);
    expect(addDays("2026-11-12", -29)).toBe("2026-10-14");
    expect(addDays("2026-03-01", -1)).toBe("2026-02-28");
  });

  it("builds on-site overlap lists only where segments can express them", () => {
    const a = { segment_ids: [1, 2] }, b = { segment_ids: [3], exclude_segment_ids: [9] }, all = {};
    expect(audienceFilters(a)).toEqual([["in", "1,2"]]);
    expect(audienceFilters(all)).toEqual([]); // everyone known: the whole list
    expect(overlapSeesFilters([a, b], 1)).toEqual([["in", "3"], ["not_in", "9,1,2"]]);
    expect(overlapSeesFilters([b, a], 1)).toBeNull(); // an earlier audience with exclusions
    expect(overlapSeesFilters([all, a], 1)).toBeNull(); // an earlier "everyone" audience
    expect(overlapPairFilters(a, b)).toEqual([["in", "1,2"], ["in", "3"], ["not_in", "9"]]);
    expect(overlapPairFilters(a, all)).toBeNull();
  });
});

describe("who may open a list", () => {
  const contacts: Perms = { contacts: "view" } as Perms;
  const reports: Perms = { reports: "view" } as Perms;
  const both: Perms = { contacts: "view", reports: "view" } as Perms;
  const journeys: Perms = { contacts: "view", journeys: "view" } as Perms;

  it("needs contacts view, plus where the number came from", () => {
    const opened = outcomeFilters("broadcast", 4, "opened");
    expect(needKeys(opened)).toEqual(["campaign:broadcast"]);
    expect(canListContacts(contacts, opened)).toBe(false);
    expect(canListContacts(reports, opened)).toBe(false); // a reports user clicking a funnel number
    expect(canListContacts(both, opened)).toBe(true);
    expect(canListContacts(journeys, [["journey", 7], ["at", "n3"]])).toBe(true);
    expect(canListContacts(journeys, [["outcome", "bounced"], ["period", "2026-11-01"]])).toBe(false); // every campaign: reports
    expect(canListContacts(contacts, [["in", 4], ["reachable", "email"]])).toBe(true);
  });

  it("says why a number isn't a link", () => {
    expect(whyNoLink(reports, [["in", 4]])).toMatch(/view access to contacts/);
    expect(whyNoLink(contacts, outcomeFilters("journey", 7, "sent"))).toMatch(/triggered & automation campaigns or reports/);
    expect(whyNoLink(both, [["in", 4]])).toBe("");
  });
});

describe("campaign navigation", () => {
  it("names three types in plain words, each built one way", () => {
    expect(CATEGORIES.map((c) => c.label)).toEqual(["One-time", "Triggered", "Automation"]);
    for (const c of CATEGORIES) expect(c.hint).not.toMatch(/broadcast|journeys? started/i);
    expect(CAMPAIGN_GOALS.map((g) => g.key)).toEqual(["one_time", "trigger", "automation"]);
  });

  it("starts what the marketer picked", () => {
    expect(newCampaignHref("one_time", "whatsapp")).toBe("/campaigns/new/whatsapp");
    expect(newCampaignHref("trigger")).toBe("/journeys/new?start=event");
    expect(newCampaignHref("automation")).toBe("/journeys/new?start=segment");
    expect(startTrigger("event")).toBe("event");
    expect(startTrigger("form")).toBe("form_submitted");
    expect(startTrigger(null)).toBe("segment_entered");
    expect(campaignTypeOf({ type: "event" })).toBe("trigger");
    expect(campaignTypeOf({ type: "form_submitted" })).toBe("trigger");
    expect(campaignTypeOf({ type: "segment_entered" })).toBe("automation");
  });

  it("sends the old lists to Campaigns and keeps it highlighted in the editors", () => {
    expect(OLD_CAMPAIGN_LISTS["/journeys"]).toBe("/campaigns");
    expect(OLD_CAMPAIGN_LISTS["/broadcasts"]).toBe("/campaigns?tab=one_time");
    const item = { also: ["/journeys", "/broadcasts"] };
    expect(navActive(item, "/journeys/7")).toBe(true);
    expect(navActive(item, "/broadcasts/new")).toBe(true);
    expect(navActive(item, "/journeys-archive")).toBe(false);
    expect(navActive(item, "/contacts")).toBe(false);
  });
});
