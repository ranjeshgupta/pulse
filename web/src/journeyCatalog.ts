// The journey builder's step chooser: every step it offers, by category (Action, Decision,
// Condition, Wait, Split), with Mautic's and WebEngage's names and one-line meanings. Several
// items store the same step type with a different preset (e.g. "Opens email" and "Clicks email"
// are message decisions). disabled() explains why an item can't go at an insertion point.
import type { JourneyGraph, JourneyNode } from "./api";
import {
  defaultsFor, insertProblem, nearestUpstream, nodeChannel, OUTCOMES, stepLabel, upstreamOfInsert, TRIGGER_ID,
  type InsertAt, type StepGroup,
} from "./journeyGraph";

export type JIconName =
  | "mail" | "chat" | "whatsapp" | "bell" | "inbox" | "send" | "user-mail" | "focus" | "layout" | "webhook"
  | "user" | "user-check" | "tag" | "star" | "flag" | "layers" | "route" | "shield" | "shield-off" | "building"
  | "building-plus" | "trend" | "jump" | "trash" | "plug" | "exit" | "clock" | "calendar" | "split" | "percent"
  | "eye" | "cursor" | "reply" | "download" | "form" | "globe" | "device" | "code" | "bolt" | "filter" | "check-mail"
  | "hourglass" | "play" | "segment";

/** What the chooser knows about the user, for permission-based reasons. */
export type CatalogCtx = { canEditContacts: boolean };

export type JourneyItem = {
  id: string; category: StepGroup; label: string; description: string; icon: JIconName;
  /** Extra words search matches. */
  keywords?: string;
  /** The step type it inserts (for the canvas colour); push_integration never inserts. */
  type: string;
  /** The new step's fields; given the graph and insertion point it can preselect, e.g. the email to watch. */
  make: (g?: JourneyGraph, at?: InsertAt) => Partial<JourneyNode> & { type: string };
  /** Why it can't be added at this point, or null. */
  disabled: (g: JourneyGraph, at: InsertAt, ctx: CatalogCtx) => string | null;
};

const SEND_TYPES = new Set(["send_email", "send_sms", "send_whatsapp", "send_rcs", "send_push", "send_inbox"]);
const hasUpstream = (g: JourneyGraph, at: InsertAt, pred: (type: string) => boolean) =>
  [...upstreamOfInsert(g, at)].some((id) => pred(g.nodes[id].type));

// A message decision watching the nearest earlier send step (email only when emailOnly).
function messageDecision(outcome: "opened" | "clicked" | "replied" | undefined, emailOnly: boolean) {
  return (g?: JourneyGraph, at?: InsertAt): Partial<JourneyNode> & { type: string } => {
    const node: Partial<JourneyNode> & { type: string } = { type: "message_decision", seconds: 2 * 86400 };
    if (!g || !at) return outcome ? { ...node, outcome } : node;
    const step = nearestUpstream(g, at, (n) => (emailOnly ? n.type === "send_email" : SEND_TYPES.has(n.type)));
    if (step) {
      const ch = nodeChannel(g.nodes[step].type)!;
      node.step = step;
      node.outcome = outcome && OUTCOMES[ch].includes(outcome) ? outcome : OUTCOMES[ch][0];
    } else if (outcome) node.outcome = outcome;
    return node;
  };
}

const ok = () => null;
const base = (type: string, extra: Partial<JourneyNode> = {}) => (g?: JourneyGraph) => ({ type, ...defaultsFor(g ?? { trigger: { type: "event" }, entry: "", nodes: {} }, type), ...extra });
// End steps may only go on a free handle or replace an Exit.
const endsPath = (type: string) => (g: JourneyGraph, at: InsertAt) => insertProblem(g, at, type);
// Steps that change consent or CRM data: the server refuses them without edit access to contacts.
const needsContacts = (_g: JourneyGraph, _at: InsertAt, ctx: CatalogCtx) => (ctx.canEditContacts ? null : "Needs edit access to contacts");
const needsEmail = (label: string) => (g: JourneyGraph, at: InsertAt) =>
  hasUpstream(g, at, (t) => t === "send_email") ? null : `${label}: add a Send email step above it first`;

type Def = Omit<JourneyItem, "disabled" | "make"> & Partial<Pick<JourneyItem, "disabled" | "make">>;

const DEFS: Def[] = [
  // ---- Decisions ----
  { id: "opens_email", category: "decision", type: "message_decision", label: "Opens email", icon: "eye", keywords: "read open message decision",
    description: "Yes as soon as the contact opens an email sent earlier on this path; No after the time limit.",
    make: messageDecision("opened", true), disabled: needsEmail("Opens email") },
  { id: "clicks_email", category: "decision", type: "message_decision", label: "Clicks email", icon: "cursor", keywords: "link click url",
    description: "Yes when they click any link, or a specific URL, in an email sent earlier on this path.",
    make: messageDecision("clicked", true), disabled: needsEmail("Clicks email") },
  { id: "replies_email", category: "decision", type: "message_decision", label: "Replies to email", icon: "reply", keywords: "answer respond",
    description: "Yes when they reply to an email sent earlier on this path (needs reply tracking in delivery settings).",
    make: messageDecision("replied", true), disabled: needsEmail("Replies to email") },
  { id: "downloads_asset", category: "decision", type: "on_asset", label: "Downloads asset", icon: "download", keywords: "file pdf",
    description: "Yes when the contact downloads a file from Assets (a specific one, or any)." },
  { id: "submits_form", category: "decision", type: "on_form", label: "Submits form", icon: "form", keywords: "signup lead",
    description: "Yes when the contact submits one of your forms (a specific one, or any)." },
  { id: "visits_page", category: "decision", type: "on_page", label: "Visits a page", icon: "globe", keywords: "url landing page view",
    description: "Yes when the contact visits a page whose URL contains some text, or a landing page." },
  { id: "device_visit", category: "decision", type: "on_device", label: "Device visit", icon: "device", keywords: "mobile desktop tablet ios android os",
    description: "Yes when the contact visits from a device type (desktop, mobile, tablet) or operating system." },
  { id: "requests_dwc", category: "decision", type: "on_dwc", label: "Requests dynamic content", icon: "layout", keywords: "dwc slot web content personalise",
    description: "The contact's page asks for a Dynamic Web Content slot: put Push dynamic content on Yes." },
  { id: "message_outcome", category: "decision", type: "message_decision", label: "Message outcome", icon: "check-mail", keywords: "sms whatsapp rcs push inbox delivered read",
    description: "Yes when an earlier SMS, WhatsApp, RCS, push or inbox message is delivered or read.",
    make: messageDecision(undefined, false),
    disabled: (g, at) => (hasUpstream(g, at, (t) => SEND_TYPES.has(t)) ? null : "Message outcome: add a send step above it first") },
  { id: "does_event", category: "decision", type: "wait_for_event", label: "Does an event", icon: "bolt", keywords: "purchase custom event wait for",
    description: "Yes when the contact does an event, e.g. purchase; No if they haven't within the time limit." },

  // ---- Conditions ----
  { id: "field_value", category: "condition", type: "check", label: "Contact field value", icon: "filter", keywords: "attribute rule city filter",
    description: "Check any contact field or behaviour rule, e.g. city is Pune.", make: base("check", { preset: "field" }) },
  { id: "segments", category: "condition", type: "condition", label: "Contact segments", icon: "segment", keywords: "segment list member",
    description: "Is the contact in a segment or list right now?" },
  { id: "tags", category: "condition", type: "check", label: "Contact tags", icon: "tag", keywords: "tag",
    description: "Does the contact have any (or all) of some tags?", make: base("check", { preset: "tags" }) },
  { id: "stages", category: "condition", type: "check", label: "Contact stages", icon: "flag", keywords: "lifecycle mql sql",
    description: "Is the contact in one of some lifecycle stages?", make: base("check", { preset: "stage" }) },
  { id: "points", category: "condition", type: "check", label: "Contact points", icon: "star", keywords: "score",
    description: "Compare the contact's points with a number.", make: base("check", { preset: "points" }) },
  { id: "owner", category: "condition", type: "check", label: "Contact owner", icon: "user-check", keywords: "assigned teammate",
    description: "Is the contact owned by some teammates, or by anyone?", make: base("check", { preset: "owner" }) },
  { id: "device", category: "condition", type: "check", label: "Contact device", icon: "device", keywords: "mobile desktop os",
    description: "The device type or OS the contact was last seen on.", make: base("check", { preset: "device" }) },
  { id: "campaigns", category: "condition", type: "if_journey", label: "Contact campaigns", icon: "route", keywords: "journey campaign member",
    description: "Is the contact active in, done with, or ever in other journeys?" },
  { id: "form_field", category: "condition", type: "if_form_field", label: "Form field value", icon: "form", keywords: "answer submission",
    description: "Check an answer on the form submission that brought them here (or their latest)." },
  { id: "notification", category: "condition", type: "if_notification", label: "Has active notification", icon: "bell", keywords: "push inbox unread",
    description: "Can push reach them, or is an unread app inbox message waiting?" },
  { id: "valid_email", category: "condition", type: "if_valid_email", label: "Has valid email address", icon: "check-mail", keywords: "verify mx disposable",
    description: "Is the address well formed with a domain that takes mail?" },
  { id: "dnc", category: "condition", type: "if_dnc", label: "Marked as Do Not Contact", icon: "shield", keywords: "unsubscribed bounced dnc consent",
    description: "Has the contact unsubscribed from, or bounced on, a channel?" },
  { id: "before_date", category: "condition", type: "if_before", label: "Prevent action after date", icon: "calendar", keywords: "deadline until expiry",
    description: "Yes until a date, No after it: wire the time-limited action on Yes." },
  { id: "visited", category: "condition", type: "if_visited", label: "Visited page", icon: "globe", keywords: "url history page view",
    description: "Has the contact ever (or lately) visited a page?" },

  // ---- Actions ----
  { id: "send_email", category: "action", type: "send_email", label: "Send email", icon: "mail", description: "Send an email template." },
  { id: "send_sms", category: "action", type: "send_sms", label: "Send SMS", icon: "chat", keywords: "text", description: "Send a text message." },
  { id: "send_whatsapp", category: "action", type: "send_whatsapp", label: "Send WhatsApp", icon: "whatsapp", description: "Send a WhatsApp template, with an SMS fallback." },
  { id: "send_rcs", category: "action", type: "send_rcs", label: "Send RCS", icon: "chat", keywords: "rich", description: "Send a rich message, with an SMS fallback." },
  { id: "send_push", category: "action", type: "send_push", label: "Send push", icon: "bell", keywords: "notification", description: "Send a web or mobile push notification." },
  { id: "send_inbox", category: "action", type: "send_inbox", label: "Send to app inbox", icon: "inbox", description: "Put a message in the contact's app inbox." },
  { id: "notify_user", category: "action", type: "notify_user", label: "Send email to user", icon: "user-mail", keywords: "notify alert teammate owner internal",
    description: "Email a teammate or the contact's owner about this contact." },
  { id: "send_message", category: "action", type: "send_message", label: "Send marketing message", icon: "send", keywords: "channel priority fallback",
    description: "Send on the first channel the contact can receive and has agreed to, in your order." },
  { id: "webhook", category: "action", type: "webhook", label: "Send a webhook", icon: "webhook", keywords: "http api", description: "Send the contact to another system." },
  { id: "show_focus", category: "action", type: "show_focus", label: "Show focus item", icon: "focus", keywords: "popup modal bar",
    description: "Show a popup, bar or slide-in on the contact's next page view." },
  { id: "push_dwc", category: "action", type: "push_dwc", label: "Push dynamic content", icon: "code", keywords: "dwc slot",
    description: "Set this contact's content for a Dynamic Web Content slot." },
  { id: "update_contact", category: "action", type: "update_attr", label: "Update contact", icon: "user", keywords: "field attribute set", description: "Set fields on the contact." },
  { id: "update_owner", category: "action", type: "update_owner", label: "Update contact owner", icon: "user-check", keywords: "assign", description: "Assign the contact to a teammate, or unassign them.",
    disabled: needsContacts },
  { id: "update_company", category: "action", type: "update_company", label: "Update company", icon: "building", description: "Set fields on the contact's company.",
    disabled: needsContacts },
  { id: "add_company", category: "action", type: "add_company", label: "Add to company", icon: "building-plus", keywords: "link account", description: "Link the contact to a company.",
    disabled: needsContacts },
  { id: "primary_company", category: "action", type: "add_company", label: "Update contact's primary company", icon: "building-plus", keywords: "link account",
    description: "Make a company the contact's company (a contact has one company).",
    disabled: needsContacts },
  { id: "company_points", category: "action", type: "company_points", label: "Add to company's score", icon: "trend", keywords: "points", description: "Add points to the company's score.",
    disabled: needsContacts },
  { id: "adjust_points", category: "action", type: "adjust_points", label: "Adjust contact points", icon: "star", keywords: "score", description: "Add or take away points." },
  { id: "change_stage", category: "action", type: "change_stage", label: "Change contact's stage", icon: "flag", keywords: "lifecycle", description: "Move the contact to a lifecycle stage, e.g. MQL." },
  { id: "update_tags", category: "action", type: "update_tags", label: "Modify contact's tags", icon: "tag", description: "Add or remove tags." },
  { id: "update_list", category: "action", type: "update_list", label: "Modify contact's segments", icon: "layers", keywords: "list static add remove",
    description: "Add to or remove from static lists (dynamic segments follow their rules)." },
  { id: "change_journey", category: "action", type: "change_journey", label: "Change campaigns", icon: "route", keywords: "journey enroll remove",
    description: "Add the contact to other journeys, or take them out." },
  { id: "jump", category: "action", type: "jump", label: "Jump to step", icon: "jump", keywords: "goto loop repeat",
    description: "Continue from another step of this journey (at most 25 times per contact).",
    disabled: (g, at) => endsPath("jump")(g, at) ?? (Object.values(g.nodes).some((n) => n.type !== "jump") ? null : "Add the step to jump to first") },
  { id: "dnc_add", category: "action", type: "dnc_add", label: "Add Do Not Contact", icon: "shield", keywords: "unsubscribe dnc",
    description: "Stop a channel, or everything, for this contact." },
  { id: "dnc_remove", category: "action", type: "dnc_remove", label: "Remove Do Not Contact", icon: "shield-off", keywords: "resubscribe dnc",
    description: "Allow a channel again.",
    disabled: needsContacts },
  { id: "delete_contact", category: "action", type: "delete_contact", label: "Delete contact", icon: "trash", keywords: "remove erase gdpr",
    description: "Permanently delete the contact and all their data.",
    disabled: (g, at, ctx) => needsContacts(g, at, ctx) ?? endsPath("delete_contact")(g, at) },
  { id: "push_integration", category: "action", type: "push_integration", label: "Push contact to integration", icon: "plug", keywords: "crm salesforce hubspot",
    description: "Send the contact to a connected CRM or tool.", make: () => ({ type: "push_integration" }),
    disabled: () => "Coming with integrations (batch 8)" },
  { id: "exit", category: "action", type: "exit", label: "Exit journey", icon: "exit", keywords: "end stop finish", description: "End the journey here." },

  // ---- Waits ----
  { id: "wait", category: "wait", type: "wait", label: "Wait", icon: "hourglass", keywords: "delay duration pause", description: "Wait a set time, e.g. 2 days." },
  { id: "wait_until", category: "wait", type: "wait_until", label: "Wait until", icon: "clock", keywords: "date time of day",
    description: "Wait for a date, or the next time of day in the contact's time zone." },

  // ---- Splits ----
  { id: "ab_split", category: "split", type: "split", label: "A/B split", icon: "split", keywords: "test variant winner experiment",
    description: "Test paths against each other, optionally picking a winner automatically." },
  { id: "percent_split", category: "split", type: "split", label: "Percentage split", icon: "percent", keywords: "random share",
    description: "Send a share of contacts down each path, e.g. 70% and 30%.",
    make: () => ({ type: "split", variants: [{ weight: 70, label: "Path 1" }, { weight: 30, label: "Path 2" }] }) },
];

export const JOURNEY_CATALOG: JourneyItem[] = DEFS.map((d) => {
  const terminal = d.type === "exit" || d.type === "jump" || d.type === "delete_contact";
  return {
    ...d,
    make: d.make ?? base(d.type),
    disabled: d.disabled ?? (terminal ? endsPath(d.type) : ok),
  };
});

export const catalogItem = (id: string) => JOURNEY_CATALOG.find((it) => it.id === id);

/** itemsFor lists a category's items in catalog order. */
export const itemsFor = (category: StepGroup) => JOURNEY_CATALOG.filter((it) => it.category === category);

/** searchItems filters items by every word of the query (label, description, keywords). */
export function searchItems(items: JourneyItem[], q: string): JourneyItem[] {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return items;
  return items.filter((it) => {
    const hay = `${it.label} ${it.description} ${it.keywords ?? ""} ${it.category}`.toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

/** reasonAt is an item's disabled reason at a point; an insertion point that no longer exists
 * disables everything. */
export function reasonAt(it: JourneyItem, g: JourneyGraph, at: InsertAt, ctx: CatalogCtx): string | null {
  if (at.source !== TRIGGER_ID && !g.nodes[at.source]) return "That step no longer exists.";
  return it.disabled(g, at, ctx);
}

/** itemForNode finds the catalog item a stored step reads as (for its icon), e.g. a check with
 * the tags preset is "Contact tags". */
export function itemForNode(g: JourneyGraph, n: JourneyNode): JourneyItem | undefined {
  const label = stepLabel(g, n);
  return JOURNEY_CATALOG.find((it) => it.type === n.type && it.label === label) ?? JOURNEY_CATALOG.find((it) => it.type === n.type);
}
