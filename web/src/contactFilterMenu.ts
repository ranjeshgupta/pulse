// The Contacts "Add filter" menu: every filter the contact list understands, as plain questions
// ("Did an event… between…"), each turned into the chips the search box already uses. Filters that
// only make sense together (an event and its dates, a campaign and what happened) are one choice.
import type { ContactFilter } from "./contactLinks";
import { period } from "./contactLinks";
import { CHURN_RISKS, VALUE_GROUPS } from "./contactFilters";

export type FieldKind = "text" | "number" | "date" | "select";
export type MenuField = {
  key: string; label: string; kind: FieldKind; optional?: boolean; placeholder?: string;
  // select: fixed options, or a list the component fills in (segments, events, campaigns…).
  options?: { value: string; label: string }[]; source?: OptionSource;
};
export type OptionSource = "segments" | "segmentIds" | "tags" | "stages" | "companies" | "users" | "events" | "campaigns" | "journeys" | "forms" | "templates" | "unsubscribed";
export type MenuItem = { id: string; label: string; hint: string; fields: MenuField[]; build: (v: Record<string, string>) => ContactFilter[] };

const CHANNELS = [
  { value: "email", label: "Email" }, { value: "sms", label: "SMS" }, { value: "whatsapp", label: "WhatsApp" },
  { value: "rcs", label: "RCS" }, { value: "push", label: "Push" }, { value: "inbox", label: "App inbox" },
];

export const OUTCOMES = [
  { value: "targeted", label: "Was in the audience" }, { value: "sent", label: "Was sent it" }, { value: "delivered", label: "Got it (delivered)" },
  { value: "opened", label: "Opened it" }, { value: "clicked", label: "Clicked in it" }, { value: "unsubscribed", label: "Unsubscribed from it" },
  { value: "bounced", label: "Bounced (hard or soft)" }, { value: "hard_bounced", label: "Hard-bounced" }, { value: "soft_bounced", label: "Soft-bounced" },
  { value: "complained", label: "Complained (spam, blocked)" }, { value: "failed_all", label: "Failed or was skipped" },
];

/** What happened to messages on a channel, for "Bounced or complained on a channel". */
export const DELIVERY_OUTCOMES = [
  { value: "delivered", label: "Was delivered to" }, { value: "hard_bounced", label: "Hard-bounced" },
  { value: "soft_bounced", label: "Soft-bounced" }, { value: "bounced", label: "Bounced (hard or soft)" },
  { value: "complained", label: "Complained (spam, blocked)" }, { value: "failed", label: "Failed" },
];

const JOURNEY_STATUS = [
  { value: "in_progress", label: "Still in it" }, { value: "completed", label: "Finished it" }, { value: "converted", label: "Reached its goal" },
  { value: "exited", label: "Left it early" },
];

const ACTIVE = [
  { value: "any", label: "Anywhere" }, { value: "web", label: "On the website" }, { value: "android", label: "On Android" },
  { value: "ios", label: "On iOS" }, { value: "new", label: "For the first time (new)" }, { value: "returning", label: "Coming back (returning)" },
];

const POINTS_OPS = [{ value: ">", label: "more than" }, { value: "<", label: "less than" }, { value: "=", label: "exactly" }, { value: ">=", label: "at least" }];

// Date fields default to the last 30 days (see defaults()).
const FROM: MenuField = { key: "from", label: "From", kind: "date" };
const TO: MenuField = { key: "to", label: "To", kind: "date" };
const dates = (v: Record<string, string>): ContactFilter[] => (v.from && v.to ? [period(v.from, v.to)] : []);

export const FILTER_MENU: { group: string; items: MenuItem[] }[] = [
  {
    group: "Who they are",
    items: [
      { id: "is", label: "Contact type", hint: "Anonymous visitors, identified contacts, unsubscribed, bounced…",
        fields: [{ key: "is", label: "Is", kind: "select", options: [
          { value: "identified", label: "Identified contacts" }, { value: "anonymous", label: "Anonymous visitors" },
          { value: "unsubscribed", label: "Unsubscribed from any channel" }, { value: "bounced", label: "Bounced (a channel stopped)" },
          { value: "dnc", label: "Do Not Contact" },
        ] }],
        build: (v) => [["is", v.is]] },
      { id: "segment", label: "In a segment", hint: "Dynamic or static", fields: [{ key: "name", label: "Segment", kind: "select", source: "segments" }], build: (v) => [["segment", v.name]] },
      { id: "not_in", label: "Not in a segment", hint: "Everyone outside it", fields: [{ key: "id", label: "Segment", kind: "select", source: "segmentIds" }], build: (v) => [["not_in", v.id]] },
      { id: "tag", label: "Has a tag", hint: "", fields: [{ key: "tag", label: "Tag", kind: "select", source: "tags" }], build: (v) => [["tag", v.tag]] },
      { id: "stage", label: "Lifecycle stage", hint: "", fields: [{ key: "stage", label: "Stage", kind: "select", source: "stages" }], build: (v) => [["stage", v.stage]] },
      { id: "value", label: "Customer value", hint: "From their purchases (RFM)", fields: [{ key: "g", label: "Group", kind: "select",
        options: VALUE_GROUPS.map((g) => ({ value: g, label: g })) }], build: (v) => [["value", v.g]] },
      { id: "churn", label: "Churn risk", hint: "How likely they are to stop buying", fields: [{ key: "r", label: "Risk", kind: "select",
        options: CHURN_RISKS.map((r) => ({ value: r, label: r[0].toUpperCase() + r.slice(1) })) }], build: (v) => [["churn", v.r]] },
      { id: "company", label: "Works at a company", hint: "", fields: [{ key: "company", label: "Company", kind: "select", source: "companies" }], build: (v) => [["company", v.company]] },
      { id: "owner", label: "Owner", hint: "The teammate who owns the contact", fields: [{ key: "owner", label: "Owner", kind: "select", source: "users" }], build: (v) => [["owner", v.owner]] },
      { id: "points", label: "Points", hint: "", fields: [
        { key: "op", label: "Points are", kind: "select", options: POINTS_OPS }, { key: "n", label: "Points", kind: "number", placeholder: "50" },
      ], build: (v) => [["points", `${v.op}${v.n}`]] },
      { id: "email", label: "Email contains", hint: "e.g. @acme.com", fields: [{ key: "q", label: "Email contains", kind: "text", placeholder: "@acme.com" }], build: (v) => [["email", v.q]] },
      { id: "name", label: "Name contains", hint: "", fields: [{ key: "q", label: "Name contains", kind: "text", placeholder: "asha" }], build: (v) => [["name", v.q]] },
    ],
  },
  {
    group: "What they did",
    items: [
      { id: "event", label: "Did an event", hint: "Between two dates, optionally for one product", fields: [
        { key: "event", label: "Event", kind: "select", source: "events" }, FROM, TO,
        { key: "product", label: "Product code", kind: "text", optional: true, placeholder: "any product" },
      ], build: (v) => [["event", v.event], ...dates(v), ...(v.product ? [["product", v.product] as ContactFilter] : [])] },
      { id: "no_event", label: "Didn't do an event", hint: "Between two dates", fields: [
        { key: "event", label: "Event", kind: "select", source: "events" }, FROM, TO,
      ], build: (v) => [["no_event", v.event], ...dates(v)] },
      { id: "active", label: "Was active", hint: "Visited or used the app between two dates", fields: [
        { key: "active", label: "Where", kind: "select", options: ACTIVE }, FROM, TO,
        { key: "utm", label: "Came from (utm_source)", kind: "text", optional: true, placeholder: "any source, e.g. google" },
      ], build: (v) => [["active", v.active], ...dates(v), ...(v.utm ? [["utm", v.utm] as ContactFilter] : [])] },
      { id: "form", label: "Submitted a form", hint: "Between two dates", fields: [
        { key: "form", label: "Form", kind: "select", source: "forms" }, FROM, TO,
      ], build: (v) => [["form", v.form], ...dates(v)] },
    ],
  },
  {
    group: "Campaigns and channels",
    items: [
      { id: "campaign", label: "Got a campaign", hint: "What happened with a campaign's message", fields: [
        { key: "campaign", label: "Campaign", kind: "select", source: "campaigns" },
        { key: "outcome", label: "And", kind: "select", options: OUTCOMES },
        { ...FROM, optional: true }, { ...TO, optional: true },
      ], build: (v) => [["campaign", v.campaign], ["outcome", v.outcome], ...dates(v)] },
      { id: "template", label: "Got a message template", hint: "What happened with one template's messages, in any campaign", fields: [
        { key: "template", label: "Template", kind: "select", source: "templates" },
        { key: "outcome", label: "And", kind: "select", options: OUTCOMES },
        { ...FROM, optional: true }, { ...TO, optional: true },
      ], build: (v) => [["template", v.template], ["outcome", v.outcome], ...dates(v)] },
      { id: "journey", label: "In a triggered or automation campaign", hint: "Who entered it, and where they are", fields: [
        { key: "journey", label: "Campaign", kind: "select", source: "journeys" },
        { key: "status", label: "And", kind: "select", options: [{ value: "", label: "Entered it" }, ...JOURNEY_STATUS], optional: true },
      ], build: (v) => [["journey", v.journey], ...(v.status ? [["journey_status", v.status] as ContactFilter] : [])] },
      { id: "delivery", label: "Bounced or complained on a channel", hint: "Any campaign's messages: delivered, hard or soft bounce, complaint", fields: [
        { key: "outcome", label: "Messages that", kind: "select", options: DELIVERY_OUTCOMES },
        { key: "ch", label: "On", kind: "select", options: CHANNELS },
        { ...FROM, optional: true }, { ...TO, optional: true },
      ], build: (v) => [["outcome", v.outcome], ["via", v.ch], ...dates(v)] },
      { id: "reachable", label: "Can be reached by", hint: "Has an address on the channel and hasn't opted out", fields: [{ key: "ch", label: "Channel", kind: "select", options: CHANNELS }], build: (v) => [["reachable", v.ch]] },
      { id: "unsubscribed", label: "Unsubscribed from", hint: "A channel, everything, or one of your topics", fields: [
        { key: "from", label: "From", kind: "select", source: "unsubscribed" },
      ], build: (v) => [["unsubscribed", v.from]] },
      { id: "subscribed", label: "Not unsubscribed from", hint: "", fields: [{ key: "ch", label: "Channel", kind: "select", options: CHANNELS }], build: (v) => [["subscribed", v.ch]] },
      { id: "under_cap", label: "Not held back by the frequency cap", hint: "Can still get messages today", fields: [{ key: "ch", label: "Channel", kind: "select", options: CHANNELS }], build: (v) => [["under_cap", v.ch]] },
    ],
  },
];

export const findItem = (id: string) => FILTER_MENU.flatMap((g) => g.items).find((i) => i.id === id);

/** defaults: a new filter's starting values — the first fixed option of each select, and the
 * last 30 days for dates (today in UTC, as the server counts days). */
export function defaults(item: MenuItem, today: string): Record<string, string> {
  const v: Record<string, string> = {};
  const monthAgo = new Date(Date.parse(today) - 29 * 86_400_000).toISOString().slice(0, 10);
  for (const f of item.fields) {
    if (f.kind === "date" && !f.optional) v[f.key] = f.key === "from" ? monthAgo : today;
    else if (f.kind === "select" && f.options && !f.optional) v[f.key] = f.options[0].value;
    else v[f.key] = "";
  }
  return v;
}

/** missing: the label of the first required field without a value, or "" when the filter is complete. */
export function missing(item: MenuItem, v: Record<string, string>): string {
  const f = item.fields.find((x) => !x.optional && !(v[x.key] ?? "").trim());
  if (f) return f.label;
  // Date ranges: only items with From and To date fields ("Unsubscribed from" has a From choice).
  if (!item.fields.some((x) => x.key === "to")) return "";
  if ((v.from && !v.to) || (!v.from && v.to)) return v.from ? "To" : "From";
  if (v.from && v.to && v.to < v.from) return "To (on or after From)";
  return "";
}

// Filters the server takes once per list: a new one replaces the old.
const ONCE = new Set(["period", "campaign", "outcome", "journey", "journey_status", "active", "utm", "form", "subscribed", "reachable", "under_cap", "product"]);

/** addFilters adds a filter's chips to the list, replacing the ones that may appear only once. */
export function addFilters(chips: string[], add: string[]): string[] {
  const keyOf = (c: string) => c.slice(0, Math.max(c.indexOf(":"), 0)).toLowerCase();
  const replaced = new Set(add.map(keyOf).filter((k) => ONCE.has(k)));
  const kept = chips.filter((c) => !replaced.has(keyOf(c)) && !add.includes(c));
  return [...kept, ...add];
}
