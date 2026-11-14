// The segment builder's filter catalog: every filter the "Add filter" picker offers, grouped like
// Mautic's and WebEngage's (Contact, Company, Location, Device, Consent & deliverability, Behaviour,
// Points & stage, Segments & tags), with search, recently used filters and the workspace's own
// attributes listed with their type.
import {
  FIELDS, fieldType, newAttr, newGroup, newSegmentRef, newTag,
  type AttrDefLike, type AttrOp, type EventOp, type FieldType, type Node, type Where,
} from "./segmentRules";

export type IconName =
  | "user" | "building" | "pin" | "device" | "shield" | "mail" | "chat" | "bell" | "globe" | "form" | "download" | "route"
  | "star" | "tag" | "layers" | "calendar" | "hash" | "toggle" | "text" | "cursor" | "bolt" | "clock" | "search";

export type GroupId = "recent" | "contact" | "company" | "location" | "device" | "consent" | "messages" | "web" | "forms" | "journeys" | "events" | "points" | "segments";

export const GROUPS: { id: GroupId; label: string; icon: IconName }[] = [
  { id: "contact", label: "Contact", icon: "user" },
  { id: "company", label: "Company", icon: "building" },
  { id: "location", label: "Location", icon: "pin" },
  { id: "device", label: "Device", icon: "device" },
  { id: "consent", label: "Consent & deliverability", icon: "shield" },
  { id: "messages", label: "Behaviour: messages", icon: "mail" },
  { id: "web", label: "Behaviour: web", icon: "globe" },
  { id: "forms", label: "Behaviour: forms & assets", icon: "form" },
  { id: "journeys", label: "Behaviour: journeys", icon: "route" },
  { id: "events", label: "Behaviour: any event", icon: "bolt" },
  { id: "points", label: "Points & stage", icon: "star" },
  { id: "segments", label: "Segments & tags", icon: "layers" },
];
export const groupLabel = (id: GroupId) => (id === "recent" ? "Recently used" : GROUPS.find((g) => g.id === id)?.label ?? id);

/** One filter the picker offers. key is the attribute it reads (for attribute filters); nests says
 * it inserts a small group of filters (so it needs room for one more level). */
export type CatalogItem = {
  id: string; group: GroupId; label: string; hint: string; icon: IconName;
  key?: string; type?: FieldType; custom?: boolean; keywords?: string; nests?: boolean;
  make: () => Node;
};

const TYPE_ICON: Record<FieldType, IconName> = { text: "text", number: "hash", boolean: "toggle", date: "calendar", list: "tag", enum: "layers" };
const TYPE_WORD: Record<FieldType, string> = { text: "text", number: "number", boolean: "yes/no", date: "date", list: "list", enum: "choice" };
export const typeWord = (t: FieldType) => TYPE_WORD[t];

/** The operator a field filter starts with. */
export function defaultOp(type: FieldType): AttrOp {
  return type === "date" ? "within_days" : type === "number" ? "gte" : type === "list" ? "has" : "eq";
}
const defaultValue = (type: FieldType) => (type === "date" ? "30" : type === "boolean" ? "true" : "");

/** fieldFilter is a new filter on an attribute, with the operator and value suited to its type. */
export function fieldFilter(key: string, type: FieldType): Node {
  return newAttr(key, defaultOp(type), defaultValue(type));
}

const ev = (event: string, days = 30, where: Where[] = [], op: EventOp = "gte", count = 1): Node => ({ kind: "event", event, op, count, days, where });
const pick = (prop: string): Where[] => [{ prop, op: "eq", value: "" }];
const text = (prop: string, op: AttrOp = "contains"): Where[] => [{ prop, op, value: "" }];

/** Behaviour, consent and membership filters: ready-made rules, worded the way marketers ask for them. */
const FIXED: Omit<CatalogItem, "icon">[] = [
  // Messages
  { id: "email_read_specific", group: "messages", label: "Read a specific email", hint: "Opened the email of a template", make: () => ev("email_opened", 30, pick("template_id")), keywords: "open opened" },
  { id: "email_read_any", group: "messages", label: "Read any email", hint: "Opened at least one email in the last N days", make: () => ev("email_opened"), keywords: "open opened" },
  { id: "email_read_times", group: "messages", label: "Read any email X number of times", hint: "Opened emails at least N times", make: () => ev("email_opened", 30, [], "gte", 3), keywords: "open count" },
  { id: "email_opened_campaign", group: "messages", label: "Opened an email from a campaign", hint: "A one-time campaign you sent", make: () => ev("email_opened", 30, pick("broadcast_id")) },
  { id: "email_opened_journey", group: "messages", label: "Opened an email from a journey", hint: "Any email the journey sent", make: () => ev("email_opened", 30, pick("journey_id")) },
  { id: "email_not_opened", group: "messages", label: "Didn't open an email from a campaign", hint: "Good for resending", make: () => ev("email_opened", 30, pick("broadcast_id"), "none") },
  { id: "email_clicked_any", group: "messages", label: "Clicked any link from any email", hint: "In the last N days", make: () => ev("email_clicked"), keywords: "click" },
  { id: "email_clicked_specific", group: "messages", label: "Clicked a link in a specific email", hint: "Any link in that email", make: () => ev("email_clicked", 30, pick("template_id")), keywords: "click" },
  { id: "sms_clicked_any", group: "messages", label: "Clicked any link from any text message", hint: "SMS links with link tracking on", make: () => ev("sms_clicked"), keywords: "click sms text" },
  {
    id: "clicked_any_channel", group: "messages", label: "Clicked any link from any channel", hint: "Email, SMS, RCS, push, app inbox or in-app", nests: true, keywords: "click",
    make: () => newGroup("any", ["email_clicked", "sms_clicked", "rcs_clicked", "push_clicked", "inbox_clicked", "inapp_clicked"].map((e) => ev(e))),
  },
  { id: "email_sent_any", group: "messages", label: "Sent any email", hint: "Was sent at least one email", make: () => ev("email_sent") },
  { id: "email_sent_specific", group: "messages", label: "Was sent a specific email", hint: "The email of a template", make: () => ev("email_sent", 30, pick("template_id")) },
  { id: "push_sent_any", group: "messages", label: "Has been sent any push notification", hint: "Mobile or web push", make: () => ev("push_sent") },
  { id: "sms_delivered", group: "messages", label: "Received an SMS", hint: "Delivered, from any campaign", make: () => ev("sms_delivered", 30, pick("broadcast_id")) },
  { id: "whatsapp_read", group: "messages", label: "Read a WhatsApp message", hint: "From a campaign or a journey", make: () => ev("whatsapp_read", 30, pick("broadcast_id")) },
  { id: "inbox_opened", group: "messages", label: "Opened an app inbox message", hint: "In the app's inbox", make: () => ev("inbox_opened") },
  // Web
  { id: "page_any", group: "web", label: "Visited any URL", hint: "Any page view in the last N days", make: () => ev("page_view"), keywords: "page view visit" },
  { id: "page_url", group: "web", label: "Visited a URL", hint: "A page whose URL contains some text", make: () => ev("page_view", 30, text("url")), keywords: "page view visit link" },
  { id: "page_times", group: "web", label: "Visited any URL X number of times", hint: "At least N page views", make: () => ev("page_view", 30, [], "gte", 5), keywords: "page view count" },
  { id: "page_referrer", group: "web", label: "Visited any URL from a referrer", hint: "The page they came from contains some text", make: () => ev("page_view", 30, text("referrer")), keywords: "referer source" },
  { id: "page_source", group: "web", label: "Visited any URL from a source", hint: "Campaign source (utm_source), e.g. newsletter", make: () => ev("page_view", 30, text("utm_source", "eq")), keywords: "utm" },
  { id: "page_source_id", group: "web", label: "Visited any URL from a campaign (source ID)", hint: "Campaign name (utm_campaign)", make: () => ev("page_view", 30, text("utm_campaign", "eq")), keywords: "utm source id" },
  { id: "page_title", group: "web", label: "Visited a page with a title", hint: "The page title contains some text", make: () => ev("page_view", 30, text("title")) },
  { id: "landing_page", group: "web", label: "Visited a specific landing page", hint: "One of your Pulse landing pages", make: () => ev("landing_page_viewed", 90, pick("page_id")) },
  { id: "sessions", group: "web", label: "Has X number of browsing sessions", hint: "Visits, counted after 30 minutes without activity", make: () => ev("session_start", 30, [], "gte", 3), keywords: "visit session" },
  // Forms and assets
  { id: "form", group: "forms", label: "Submitted a form", hint: "One of your forms", make: () => ev("form_submitted", 90, pick("form_id")) },
  { id: "asset", group: "forms", label: "Downloaded a specific asset", hint: "A file from Assets", make: () => ev("asset_downloaded", 90, pick("asset_id")), keywords: "download file" },
  // Journeys
  { id: "journey_entered", group: "journeys", label: "Entered a journey", hint: "Started it in the last N days (campaign membership)", make: () => ev("journey_entered", 90, pick("journey_id")), keywords: "campaign membership" },
  { id: "journey_completed", group: "journeys", label: "Completed a journey", hint: "Reached its end", make: () => ev("journey_completed", 90, pick("journey_id")), keywords: "campaign" },
  // Any event
  { id: "did", group: "events", label: "Did an event", hint: "Any event, counted over the last N days", make: () => ev("") },
  { id: "didnt", group: "events", label: "Didn't do an event", hint: "Never did it in the last N days", make: () => ev("", 30, [], "none") },
  // Consent (events and combinations; the opt-out fields themselves come from FIELDS)
  {
    id: "unsub_manual_email", group: "consent", label: "Unsubscribed manually: email", hint: "Opted out of email by your team or the API, not by themselves", nests: true,
    make: () => newGroup("all", [newAttr("optout_email", "eq", "true"), ev("unsubscribed", 365, [{ prop: "channel", op: "eq", value: "email" }, { prop: "method", op: "eq", value: "manual" }])]),
  },
  {
    id: "unsub_manual_sms", group: "consent", label: "Unsubscribed manually: SMS", hint: "Opted out of SMS by your team or the API", nests: true,
    make: () => newGroup("all", [newAttr("optout_sms", "eq", "true"), ev("unsubscribed", 365, [{ prop: "channel", op: "eq", value: "sms" }, { prop: "method", op: "eq", value: "manual" }])]),
  },
  { id: "unsub_event", group: "consent", label: "Unsubscribed recently", hint: "From a channel, in the last N days", make: () => ev("unsubscribed", 30, [{ prop: "channel", op: "eq", value: "email" }]) },
  {
    id: "reachable_email", group: "consent", label: "Reachable by email", hint: "Has an email, not unsubscribed, not bounced", nests: true,
    make: () => newGroup("all", [newAttr("email", "not_empty"), newAttr("optout_email", "ne", "true"), newAttr("optout_all", "ne", "true"), newAttr("email_bounced", "ne", "true")]),
  },
  {
    id: "reachable_sms", group: "consent", label: "Reachable by SMS", hint: "Has a mobile number, not unsubscribed, not bounced", nests: true,
    make: () => newGroup("all", [newAttr("phone", "not_empty"), newAttr("optout_sms", "ne", "true"), newAttr("optout_all", "ne", "true"), newAttr("sms_bounced", "ne", "true")]),
  },
  { id: "email_bounce_event", group: "consent", label: "An email bounced recently", hint: "A bounce in the last N days", make: () => ev("email_bounced", 90) },
  // Points and stage
  { id: "stage_recent", group: "points", label: "Changed stage recently", hint: "Moved to their stage in the last 7 days", make: () => newAttr("stage_changed_at", "within_days", "7") },
  // Segments and tags
  { id: "tag", group: "segments", label: "Tags", hint: "Is tagged, or isn't", make: newTag },
  { id: "segment", group: "segments", label: "Segment membership", hint: "In a segment (dynamic or static), or not in it", make: newSegmentRef },
  // Company
  { id: "company_attr", group: "company", label: "Other company field", hint: "Any field of their company, e.g. industry", make: () => newAttr("company_", "eq", "") },
];

const ICONS: Record<GroupId, IconName> = Object.fromEntries(GROUPS.map((g) => [g.id, g.icon])) as Record<GroupId, IconName>;
const EVENT_ICON: Record<string, IconName> = { messages: "mail", web: "globe", forms: "form", journeys: "route", events: "bolt", consent: "shield", segments: "layers", points: "star", company: "building" };

function fixedIcon(it: Omit<CatalogItem, "icon">): IconName {
  if (it.id === "tag") return "tag";
  if (it.id.startsWith("sms")) return "chat";
  if (it.id.startsWith("push")) return "bell";
  if (it.id.includes("click")) return "cursor";
  if (it.id === "asset") return "download";
  if (it.id === "sessions") return "clock";
  return EVENT_ICON[it.group] ?? ICONS[it.group];
}

const labelOfKey = (key: string) => key.replace(/^company_/, "").replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase());

/** groupOfKey places a workspace attribute: company copies, opt-outs, location-ish names, or Contact. */
function groupOfKey(key: string): GroupId {
  if (key.startsWith("company_")) return "company";
  if (key.startsWith("optout_")) return "consent";
  if (key.startsWith("geo_")) return "location";
  return "contact";
}

/** The attribute definitions and other keys seen on contacts (GET /v1/attributes). */
export type AttrSource = {
  defs: (AttrDefLike & { label?: string; description?: string })[]; seen: string[];
  // The workspace's own events (sent recently or described under Events), each a "Did …" filter.
  events?: { name: string; label?: string }[];
};

/** buildCatalog lists every filter: Pulse's own fields, every attribute the workspace defined or its
 * contacts carry (typed by its definition), and the behaviour filters. */
export function buildCatalog(src: AttrSource = { defs: [], seen: [] }): CatalogItem[] {
  const out: CatalogItem[] = [];
  const known = new Set<string>();
  for (const f of FIELDS) {
    known.add(f.key);
    const type = f.type;
    out.push({
      id: `field:${f.key}`, group: f.group as GroupId, label: f.label, hint: f.hint, icon: f.pick === "owner" ? "user" : f.pick === "company" ? "building" : TYPE_ICON[type],
      key: f.key, type, make: () => fieldFilter(f.key, type),
    });
  }
  const custom = new Map<string, { label?: string; description?: string }>();
  for (const d of src.defs) custom.set(d.key, d);
  for (const k of src.seen) if (!custom.has(k)) custom.set(k, {});
  for (const [key, d] of [...custom.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    if (known.has(key) || IGNORED_KEYS.has(key)) continue;
    const type = fieldType(key, src.defs);
    out.push({
      id: `field:${key}`, group: groupOfKey(key), label: d.label || labelOfKey(key), hint: d.description || `${src.defs.some((x) => x.key === key) ? "Custom field" : "Seen on contacts"} · ${TYPE_WORD[type]}`,
      icon: TYPE_ICON[type], key, type, custom: true, make: () => fieldFilter(key, type),
    });
  }
  for (const it of FIXED) out.push({ ...it, icon: fixedIcon(it) });
  for (const e of src.events ?? []) {
    out.push({
      id: `event:${e.name}`, group: "events", label: `Did ${e.label || e.name}`, hint: `The ${e.name} event, in the last N days or between dates`,
      icon: "bolt", keywords: e.name.replace(/_/g, " "), make: () => ev(e.name, 30),
    });
  }
  return out;
}

/** Keys that aren't useful as filters (tags have their own filter; merged contacts never match). */
const IGNORED_KEYS = new Set(["tags", "merged_into", "owner_name", "first_referrer", "last_referrer"]);

/** searchCatalog keeps the items matching every word of q in their label, hint, attribute key,
 * keywords or group name (case-insensitive). */
export function searchCatalog(items: CatalogItem[], q: string): CatalogItem[] {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return items;
  return items.filter((it) => {
    const hay = `${it.label} ${it.hint} ${it.key ?? ""} ${it.keywords ?? ""} ${groupLabel(it.group)} ${it.type ? TYPE_WORD[it.type] : ""}`.toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

/** groupItems arranges items in the picker's order: recently used first (when not searching), then
 * each group in GROUPS order. Items keep their catalog order inside a group. */
export function groupItems(items: CatalogItem[], recent: string[] = []): { id: GroupId; label: string; items: CatalogItem[] }[] {
  const out: { id: GroupId; label: string; items: CatalogItem[] }[] = [];
  const byId = new Map(items.map((it) => [it.id, it]));
  const rec = recent.map((id) => byId.get(id)).filter((x): x is CatalogItem => !!x);
  if (rec.length) out.push({ id: "recent", label: groupLabel("recent"), items: rec });
  for (const g of GROUPS) {
    const xs = items.filter((it) => it.group === g.id);
    if (xs.length) out.push({ id: g.id, label: g.label, items: xs });
  }
  return out;
}

// ---- recently used ----

const RECENT_KEY = "pulse.segment.recentFilters";
export const MAX_RECENT = 6;

/** pushRecent puts id first in the recently used list (no duplicates, at most MAX_RECENT). */
export function pushRecent(list: string[], id: string): string[] {
  return [id, ...list.filter((x) => x !== id)].slice(0, MAX_RECENT);
}

export function loadRecent(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]") as unknown;
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string").slice(0, MAX_RECENT) : [];
  } catch { return []; }
}

export function saveRecent(list: string[]) {
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(list)); } catch { /* private mode */ }
}

/** catalogIdOf finds the catalog item a filter came from, so a field can be shown with its label. */
export function itemForKey(items: CatalogItem[], key: string): CatalogItem | undefined {
  return items.find((it) => it.key === key.trim());
}
