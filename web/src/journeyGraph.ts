// The journey graph the API stores, and every edit the builder makes to it. The graph is the source
// of truth: canvas edges are derived from each node's next / yes / no / variants fields, so every
// edit on the canvas is an edit of the graph. Pure functions only (tested without a DOM): outputs,
// insert-at-handle, delete with healing, tidy layout, step numbering and the server's validation
// rules in words.
import type { Edge, Node as FlowNode } from "@xyflow/react";
import { deadlineProblem } from "./campaigns";
import { CHANNEL_NAMES, type Channel, type ChannelRule, type JourneyGraph, type JourneyNode } from "./api";

export const TRIGGER_ID = "__trigger";

/** The five kinds of step the chooser offers, each with what it means. */
export type StepGroup = "action" | "decision" | "condition" | "wait" | "split";
export const STEP_GROUPS: { group: StepGroup; label: string; hint: string }[] = [
  { group: "action", label: "Action", hint: "Do something: send a message, update the contact, call a webhook." },
  { group: "decision", label: "Decision", hint: "Wait for the contact to do something; Yes if they do within the time limit, No if not." },
  { group: "condition", label: "Condition", hint: "Check something about the contact right now; Yes or No." },
  { group: "wait", label: "Wait", hint: "Pause, for a time or until a date or time of day." },
  { group: "split", label: "Split", hint: "Send contacts down paths at random: A/B test or percentage split." },
];

/** How a step continues: one next step, Yes/No later (decision) or now (condition), split paths, or not at all. */
export type Shape = "next" | "decision" | "condition" | "split" | "end";

export type TypeInfo = { type: string; label: string; group: StepGroup; shape: Shape; hint: string; channel?: Channel };

/** Every step type the API stores. The chooser's catalog (journeyCatalog.ts) offers these, some
 * several times with different presets (e.g. "Opens email" and "Clicks email" are message decisions). */
export const NODE_TYPES: TypeInfo[] = [
  // Actions: messages
  { type: "send_email", label: "Send email", group: "action", shape: "next", hint: "Send an email template.", channel: "email" },
  { type: "send_sms", label: "Send SMS", group: "action", shape: "next", hint: "Send a text message.", channel: "sms" },
  { type: "send_whatsapp", label: "Send WhatsApp", group: "action", shape: "next", hint: "Send a WhatsApp template, with an SMS fallback.", channel: "whatsapp" },
  { type: "send_rcs", label: "Send RCS", group: "action", shape: "next", hint: "Send a rich message, with an SMS fallback.", channel: "rcs" },
  { type: "send_push", label: "Send push", group: "action", shape: "next", hint: "Send a web or mobile push notification.", channel: "push" },
  { type: "send_inbox", label: "Send to app inbox", group: "action", shape: "next", hint: "Put a message in the contact's app inbox.", channel: "inbox" },
  { type: "send_message", label: "Send marketing message", group: "action", shape: "next", hint: "Choose the channel by rules (an Indian number gets SMS, otherwise WhatsApp), or the first the contact can receive." },
  { type: "notify_user", label: "Send email to user", group: "action", shape: "next", hint: "Email a teammate or the contact's owner about this contact." },
  { type: "show_focus", label: "Show focus item", group: "action", shape: "next", hint: "Show a popup, bar or slide-in on the contact's next page view." },
  { type: "push_dwc", label: "Push dynamic content", group: "action", shape: "next", hint: "Set this contact's content for a Dynamic Web Content slot." },
  { type: "webhook", label: "Send a webhook", group: "action", shape: "next", hint: "Send the contact to another system." },
  { type: "push_integration", label: "Push contact to integration", group: "action", shape: "next", hint: "Send the contact to a connected CRM through its field mapping." },
  // Actions: the contact
  { type: "update_attr", label: "Update contact", group: "action", shape: "next", hint: "Set fields on the contact." },
  { type: "update_owner", label: "Update contact owner", group: "action", shape: "next", hint: "Assign the contact to a teammate." },
  { type: "update_tags", label: "Modify contact's tags", group: "action", shape: "next", hint: "Add or remove tags." },
  { type: "adjust_points", label: "Adjust contact points", group: "action", shape: "next", hint: "Add or take away points." },
  { type: "change_stage", label: "Change contact's stage", group: "action", shape: "next", hint: "Move the contact to a lifecycle stage, e.g. MQL." },
  { type: "update_list", label: "Modify contact's segments", group: "action", shape: "next", hint: "Add to or remove from static segments." },
  { type: "change_journey", label: "Change campaigns", group: "action", shape: "next", hint: "Add the contact to other journeys, or take them out." },
  { type: "dnc_add", label: "Add Do Not Contact", group: "action", shape: "next", hint: "Stop a channel (or everything) for this contact." },
  { type: "dnc_remove", label: "Remove Do Not Contact", group: "action", shape: "next", hint: "Allow a channel again." },
  // Actions: companies
  { type: "update_company", label: "Update company", group: "action", shape: "next", hint: "Set fields on the contact's company." },
  { type: "add_company", label: "Add to company", group: "action", shape: "next", hint: "Link the contact to a company." },
  { type: "company_points", label: "Add to company's score", group: "action", shape: "next", hint: "Add points to the company's score." },
  // Actions that end a path
  { type: "jump", label: "Jump to step", group: "action", shape: "end", hint: "Continue from another step of this journey." },
  { type: "delete_contact", label: "Delete contact", group: "action", shape: "end", hint: "Permanently delete the contact and their data." },
  { type: "exit", label: "Exit journey", group: "action", shape: "end", hint: "End the journey here." },
  // Decisions
  { type: "message_decision", label: "Message outcome", group: "decision", shape: "decision", hint: "Waits for an earlier message to be opened, clicked, replied to, delivered or read; else takes “No” after the time limit." },
  { type: "wait_for_event", label: "Does an event", group: "decision", shape: "decision", hint: "Waits for the contact to do an event, e.g. purchase; else takes “No” after the time limit." },
  { type: "on_form", label: "Submits form", group: "decision", shape: "decision", hint: "Waits for a form submission." },
  { type: "on_asset", label: "Downloads asset", group: "decision", shape: "decision", hint: "Waits for a file download." },
  { type: "on_page", label: "Visits a page", group: "decision", shape: "decision", hint: "Waits for a page visit." },
  { type: "on_device", label: "Device visit", group: "decision", shape: "decision", hint: "Waits for a visit from a device type or OS." },
  { type: "on_dwc", label: "Requests dynamic content", group: "decision", shape: "decision", hint: "Waits for the contact's page to ask for a Dynamic Web Content slot." },
  // Conditions
  { type: "condition", label: "Contact segments", group: "condition", shape: "condition", hint: "Is the contact in a segment?" },
  { type: "check", label: "Contact field value", group: "condition", shape: "condition", hint: "Does the contact match a rule, e.g. city is Pune?" },
  { type: "if_journey", label: "Contact campaigns", group: "condition", shape: "condition", hint: "Is the contact in, or done with, other journeys?" },
  { type: "if_form_field", label: "Form field value", group: "condition", shape: "condition", hint: "What did they answer on a form?" },
  { type: "if_notification", label: "Has active notification", group: "condition", shape: "condition", hint: "Can push reach them, or is an inbox message waiting?" },
  { type: "if_valid_email", label: "Has valid email address", group: "condition", shape: "condition", hint: "Is the address well formed with a working domain?" },
  { type: "if_dnc", label: "Marked as Do Not Contact", group: "condition", shape: "condition", hint: "Unsubscribed from or bounced on a channel?" },
  { type: "if_before", label: "Prevent action after date", group: "condition", shape: "condition", hint: "Yes until a date, then No." },
  { type: "if_visited", label: "Visited page", group: "condition", shape: "condition", hint: "Has the contact visited a page?" },
  // Split and waits
  { type: "split", label: "Split", group: "split", shape: "split", hint: "Send contacts down paths at random, by share." },
  { type: "wait", label: "Wait", group: "wait", shape: "next", hint: "Wait a set time, e.g. 2 days." },
  { type: "wait_until", label: "Wait until", group: "wait", shape: "next", hint: "Wait for a date, or a time of day in the contact's zone." },
];
const TYPE_BY_ID: Record<string, TypeInfo> = Object.fromEntries(NODE_TYPES.map((t) => [t.type, t]));
export const typeInfo = (type: string): TypeInfo | undefined => TYPE_BY_ID[type];
export const nodeLabel = (type: string) => TYPE_BY_ID[type]?.label ?? type;
export const nodeChannel = (type: string) => TYPE_BY_ID[type]?.channel;
export const shapeOf = (type: string): Shape => TYPE_BY_ID[type]?.shape ?? "next";
export const groupOf = (type: string): StepGroup => TYPE_BY_ID[type]?.group ?? "action";
/** isTerminal: the step ends its path (Exit, Jump to step, Delete contact). */
export const isTerminal = (type: string) => shapeOf(type) === "end";

/** What a message decision can wait for, by the channel of the step it watches (mirrors the backend). */
export type Outcome = NonNullable<JourneyNode["outcome"]>;
export const OUTCOMES: Record<Channel, Outcome[]> = {
  email: ["opened", "clicked", "replied", "delivered"],
  sms: ["delivered"],
  whatsapp: ["delivered", "read"],
  rcs: ["delivered", "read", "replied"],
  push: ["delivered", "clicked"],
  inbox: ["opened", "clicked"],
};
/** Outcomes in words: for the inspector ("the message …") and the canvas. */
export const OUTCOME_WORDS: Record<Outcome, { long: string; short: string }> = {
  opened: { long: "is opened", short: "opened" },
  clicked: { long: "a link is clicked", short: "clicked" },
  replied: { long: "gets a reply", short: "replied to" },
  delivered: { long: "is delivered", short: "delivered" },
  read: { long: "is read", short: "read" },
};
/** The shortest and longest time limit of a decision, in seconds. */
export const DECISION_MIN = 60;
export const MAX_WAIT = 365 * 86400;
/** Most times one enrollment may pass through Jump to step steps before it leaves. */
export const MAX_JUMPS = 25;

/** Channels a Do Not Contact step or condition names. */
export const DNC_CHANNELS = ["email", "sms", "whatsapp", "rcs", "push", "inbox"] as const;
export const FORM_FIELD_OPS = ["eq", "ne", "contains", "not_contains", "starts_with", "ends_with", "gt", "gte", "lt", "lte", "exists", "not_exists"] as const;
export const DEVICE_TYPES = ["desktop", "mobile", "tablet"] as const;

const EVENT_RE = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/;
export const SLOT_RE = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const COMPANY_KEY_RE = /^[a-zA-Z0-9_]{1,64}$/;
const TAG_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/;

// ---- outputs and edges ----

/** Which outgoing handles a step has: [handle id, label]. Decisions and conditions: Yes then No;
 * splits: one per path ("A · 50%"); end steps: none. */
export function outputs(n: JourneyNode): [string, string | undefined][] {
  switch (shapeOf(n.type)) {
    case "end": return [];
    case "decision": case "condition": return [["yes", "Yes"], ["no", "No"]];
    case "split": return (n.variants ?? []).map((_, i) => [`v${i}`, `${pathName(n, i)} · ${share(n, i)}%`]);
    default: return [["next", undefined]];
  }
}

/** The label drawn on an edge: decisions say how long No waits ("No — after 2 days"). */
export function edgeLabel(n: JourneyNode, handle: string): string | undefined {
  const label = outputs(n).find(([h]) => h === handle)?.[1];
  if (handle === "no" && shapeOf(n.type) === "decision") return `No — after ${formatWait(n.seconds)}`;
  return label;
}

export const pathName = (n: JourneyNode, i: number) => n.variants?.[i]?.label || String.fromCharCode(65 + i);

export function share(n: JourneyNode, i: number) {
  const total = (n.variants ?? []).reduce((a, v) => a + (v.weight || 0), 0) || 1;
  return Math.round(((n.variants?.[i]?.weight ?? 0) / total) * 100);
}

/** The step a handle of a node leads to. */
export function targetOf(n: JourneyNode, handle: string): string | undefined {
  if (handle.startsWith("v")) return n.variants?.[Number(handle.slice(1))]?.next;
  if (handle !== "next" && handle !== "yes" && handle !== "no") return undefined;
  return n[handle];
}

/** The handle a newly inserted step continues the old path on: next, yes, or the first split path. */
export const firstOutput = (n: JourneyNode): string | undefined => outputs(n)[0]?.[0];

/** Where a new step goes: after a node's (or the trigger's) output handle. */
export type InsertAt = { source: string; handle: string };

/** targetAt: what an insertion point currently leads to (undefined: the handle is free). */
export function targetAt(g: JourneyGraph, at: InsertAt): string | undefined {
  const t = at.source === TRIGGER_ID ? g.entry : g.nodes[at.source] ? targetOf(g.nodes[at.source], at.handle) : undefined;
  return t && g.nodes[t] ? t : undefined;
}

/** Children of a step in handle order (existing steps only, no repeats). */
export function childrenOf(g: JourneyGraph, id: string): string[] {
  const out: string[] = [];
  if (id === TRIGGER_ID) return g.nodes[g.entry] ? [g.entry] : [];
  const n = g.nodes[id];
  if (!n) return out;
  for (const [h] of outputs(n)) {
    const t = targetOf(n, h);
    if (t && g.nodes[t] && !out.includes(t)) out.push(t);
  }
  return out;
}

/** parentsOf maps each step to the handles pointing at it (the trigger included). */
export function parentsOf(g: JourneyGraph): Map<string, InsertAt[]> {
  const m = new Map<string, InsertAt[]>();
  const add = (t: string | undefined, at: InsertAt) => { if (t && g.nodes[t]) m.set(t, [...(m.get(t) ?? []), at]); };
  add(g.entry, { source: TRIGGER_ID, handle: "next" });
  for (const [id, n] of Object.entries(g.nodes)) for (const [h] of outputs(n)) add(targetOf(n, h), { source: id, handle: h });
  return m;
}

/** sendSteps lists the journey's send steps, which a message decision can watch. */
export function sendSteps(g: JourneyGraph): [string, JourneyNode, Channel][] {
  return Object.entries(g.nodes).flatMap(([id, n]) => {
    const ch = nodeChannel(n.type);
    return ch ? [[id, n, ch] as [string, JourneyNode, Channel]] : [];
  });
}

/** upstream lists the steps a contact can pass through before reaching id (the trigger excluded). */
export function upstream(g: JourneyGraph, id: string): Set<string> {
  const parents = parentsOf(g);
  const seen = new Set<string>();
  const stack = (parents.get(id) ?? []).map((p) => p.source);
  while (stack.length) {
    const p = stack.pop()!;
    if (p === id || p === TRIGGER_ID || seen.has(p)) continue;
    seen.add(p);
    stack.push(...(parents.get(p) ?? []).map((x) => x.source));
  }
  return seen;
}

/** Steps a contact has passed through when they reach an insertion point (its source included). */
export function upstreamOfInsert(g: JourneyGraph, at: InsertAt): Set<string> {
  if (at.source === TRIGGER_ID || !g.nodes[at.source]) return new Set();
  const s = upstream(g, at.source);
  s.add(at.source);
  return s;
}

/** nearestUpstream finds the closest step before an insertion point that matches, walking back
 * through parents breadth first (handle order), or undefined. */
export function nearestUpstream(g: JourneyGraph, at: InsertAt, match: (n: JourneyNode, id: string) => boolean): string | undefined {
  if (at.source === TRIGGER_ID || !g.nodes[at.source]) return undefined;
  const parents = parentsOf(g);
  const seen = new Set<string>();
  const queue = [at.source];
  while (queue.length) {
    const id = queue.shift()!;
    if (id === TRIGGER_ID || seen.has(id) || !g.nodes[id]) continue;
    seen.add(id);
    if (match(g.nodes[id], id)) return id;
    queue.push(...(parents.get(id) ?? []).map((p) => p.source));
  }
  return undefined;
}

/** decisionText words a message decision for the canvas, e.g. "Email ‘Welcome’ opened within 2 days?".
 * templateName names the watched step's template, when known. */
export function decisionText(g: JourneyGraph, n: JourneyNode, templateName?: (id: number) => string | undefined): string {
  const step = n.step ? g.nodes[n.step] : undefined;
  const ch = step ? nodeChannel(step.type) : undefined;
  if (!step || !ch) return "Choose a message";
  const name = (step.template_id && templateName?.(step.template_id)) || n.step;
  const what = n.outcome && OUTCOMES[ch].includes(n.outcome) ? OUTCOME_WORDS[n.outcome].short : "…";
  const link = n.outcome === "clicked" && n.url ? ` (link contains ‘${n.url}’)` : "";
  return `${CHANNEL_NAMES[ch]} ‘${name}’ ${what}${link} within ${formatWait(n.seconds)}?`;
}

/** stepLabel names a step the way the chooser offered it: a check made with the tags preset is
 * "Contact tags", a message decision on an email's clicks is "Clicks email". */
export function stepLabel(g: JourneyGraph, n: JourneyNode): string {
  if (n.type === "check") {
    const p: Record<string, string> = { tags: "Contact tags", stage: "Contact stages", points: "Contact points", owner: "Contact owner", device: "Contact device" };
    return (n.preset && p[n.preset]) || "Contact field value";
  }
  if (n.type === "message_decision") {
    const ch = n.step && g.nodes[n.step] ? nodeChannel(g.nodes[n.step].type) : undefined;
    if (ch === "email" && n.outcome === "opened") return "Opens email";
    if (ch === "email" && n.outcome === "clicked") return "Clicks email";
    if (ch === "email" && n.outcome === "replied") return "Replies to email";
    return "Message outcome";
  }
  if (n.type === "split") {
    const vs = n.variants ?? [];
    return n.winner || vs.every((v, i) => !v.label || /^[A-Z]$/.test(v.label) || v.label === String.fromCharCode(65 + i)) ? "A/B split" : "Percentage split";
  }
  return nodeLabel(n.type);
}

// ---- React Flow ----

export type FlowData = { id: string; node?: JourneyNode; isTrigger?: boolean };
/** Data on a canvas edge: the output it draws and its label; jump edges are drawn but not editable. */
export type EdgeData = { source: string; handle: string; label?: string; kind: "next" | "yes" | "no" | "path" | "jump" };

export function emptyGraph(): JourneyGraph {
  return {
    trigger: { type: "segment_entered" },
    entry: "exit",
    nodes: { exit: { type: "exit", x: 0, y: ROW } },
  };
}

/** Layout grid: a column per tree leaf, a row per step depth (node cards are 240px wide). */
export const COL = 280;
export const ROW = 210;

/** positions of every step, with a fallback for steps saved without one. */
export function positions(g: JourneyGraph): Record<string, { x: number; y: number }> {
  const out: Record<string, { x: number; y: number }> = {};
  let i = 0;
  for (const [id, n] of Object.entries(g.nodes)) {
    out[id] = { x: n.x ?? 300, y: n.y ?? 140 + i * 120 };
    i++;
  }
  const e = out[g.entry];
  out[TRIGGER_ID] = e ? { x: e.x, y: e.y - ROW } : { x: 300, y: 20 };
  return out;
}

export function toFlow(g: JourneyGraph): { nodes: FlowNode<FlowData>[]; edges: Edge<EdgeData>[] } {
  const pos = positions(g);
  const nodes: FlowNode<FlowData>[] = [
    { id: TRIGGER_ID, type: "pulse", position: pos[TRIGGER_ID], data: { id: TRIGGER_ID, isTrigger: true }, deletable: false },
  ];
  const edges: Edge<EdgeData>[] = [];
  const edge = (source: string, target: string | undefined, handle: string, kind: EdgeData["kind"], label?: string) => {
    if (target && g.nodes[target]) {
      edges.push({ id: `${source}:${handle}`, source, sourceHandle: handle, target, label, type: "add", data: { source, handle, label, kind } });
    }
  };
  edge(TRIGGER_ID, g.entry, "next", "next");
  for (const [id, n] of Object.entries(g.nodes)) {
    nodes.push({ id, type: "pulse", position: pos[id], data: { id, node: n } });
    for (const [h] of outputs(n)) edge(id, targetOf(n, h), h, h === "next" ? "next" : h === "yes" ? "yes" : h === "no" ? "no" : "path", edgeLabel(n, h));
    if (n.type === "jump" && n.target && g.nodes[n.target] && n.target !== id) {
      edges.push({
        id: `jump:${id}`, source: id, sourceHandle: "jump", target: n.target, type: "jump",
        deletable: false, selectable: false, focusable: false, data: { source: id, handle: "jump", kind: "jump" },
      });
    }
  }
  return { nodes, edges };
}

// ---- edits ----

/** wouldCycle reports whether pointing source at target would create a loop (jumps aren't edges). */
export function wouldCycle(g: JourneyGraph, source: string, target: string): boolean {
  if (source === target) return true;
  if (source === TRIGGER_ID) return false;
  const seen = new Set<string>();
  const stack = [target];
  while (stack.length) {
    const id = stack.pop()!;
    if (id === source) return true;
    if (seen.has(id)) continue;
    seen.add(id);
    stack.push(...childrenOf(g, id));
  }
  return false;
}

// connect points a node's outgoing edge (by handle) at a target node.
export function connect(g: JourneyGraph, source: string, handle: string | null | undefined, target: string): JourneyGraph {
  if (source === target || target === TRIGGER_ID) return g;
  if (source === TRIGGER_ID) return { ...g, entry: target };
  const n = g.nodes[source];
  if (!n) return g;
  if (handle?.startsWith("v")) {
    const i = Number(handle.slice(1));
    const variants = (n.variants ?? []).map((v, j) => (j === i ? { ...v, next: target } : v));
    return { ...g, nodes: { ...g.nodes, [source]: { ...n, variants } } };
  }
  const field = handle === "yes" ? "yes" : handle === "no" ? "no" : "next";
  return { ...g, nodes: { ...g.nodes, [source]: { ...n, [field]: target } } };
}

export function disconnect(g: JourneyGraph, edgeId: string): JourneyGraph {
  const [source, handle] = edgeId.split(":");
  if (source === "jump") return g;
  if (source === TRIGGER_ID) return { ...g, entry: "" };
  const n = g.nodes[source];
  if (!n || !handle) return g;
  const copy = { ...n };
  if (handle.startsWith("v")) {
    const i = Number(handle.slice(1));
    copy.variants = (n.variants ?? []).map((v, j) => (j === i ? { ...v, next: undefined } : v));
  } else delete copy[handle as "next" | "yes" | "no"];
  return { ...g, nodes: { ...g.nodes, [source]: copy } };
}

/** removeNode deletes a step and every reference to it (paths into it become free). */
export function removeNode(g: JourneyGraph, id: string): JourneyGraph {
  const nodes: Record<string, JourneyNode> = {};
  for (const [k, n] of Object.entries(g.nodes)) {
    if (k === id) continue;
    const copy = { ...n };
    for (const f of ["next", "yes", "no", "step", "target"] as const) if (copy[f] === id) delete copy[f];
    if (copy.variants) copy.variants = copy.variants.map((v) => (v.next === id ? { ...v, next: undefined } : v));
    nodes[k] = copy;
  }
  return { ...g, entry: g.entry === id ? "" : g.entry, nodes };
}

/** deleteStep removes a step the way builders do: a step with one way on (an action or a wait) is
 * healed over, so what pointed at it now points at its next step; other steps leave their paths free. */
export function deleteStep(g: JourneyGraph, id: string): JourneyGraph {
  const n = g.nodes[id];
  if (!n) return g;
  const outs = outputs(n);
  const next = outs.length === 1 && outs[0][0] === "next" ? targetOf(n, "next") : undefined;
  const parents = parentsOf(g).get(id) ?? [];
  let out = removeNode(g, id);
  if (next && out.nodes[next]) for (const p of parents) if (p.source === TRIGGER_ID || out.nodes[p.source]) out = connect(out, p.source, p.handle, next);
  return out;
}

/** descendants lists a step and every step after it. */
export function descendants(g: JourneyGraph, id: string): Set<string> {
  const seen = new Set<string>();
  const stack = [id];
  while (stack.length) {
    const x = stack.pop()!;
    if (seen.has(x) || !g.nodes[x]) continue;
    seen.add(x);
    stack.push(...childrenOf(g, x));
  }
  return seen;
}

/** removeBranch deletes a step and the steps after it that nothing else leads to. */
export function removeBranch(g: JourneyGraph, id: string): JourneyGraph {
  const below = descendants(g, id);
  const parents = parentsOf(g);
  // Keep any step that is also reached from outside the branch.
  const outside = (x: string) => (parents.get(x) ?? []).some((p) => p.source === TRIGGER_ID ? x !== id : !below.has(p.source));
  const keep = new Set<string>();
  const stack = [...below].filter((x) => x !== id && outside(x));
  while (stack.length) {
    const x = stack.pop()!;
    if (keep.has(x)) continue;
    keep.add(x);
    stack.push(...childrenOf(g, x).filter((c) => below.has(c)));
  }
  let out = g;
  for (const x of below) if (!keep.has(x)) out = removeNode(out, x);
  return out;
}

const ID_BASE: Record<string, string> = {
  message_decision: "decision", change_stage: "stage", wait_for_event: "event", update_attr: "update", update_tags: "tags",
  adjust_points: "points", update_list: "lists", change_journey: "journeys", delete_contact: "delete", notify_user: "notify",
  send_message: "message", show_focus: "focus", push_dwc: "dwc", update_owner: "owner", update_company: "company",
  add_company: "addcompany", company_points: "companypoints", condition: "segment", push_integration: "push",
};

/** newId names a new step uniquely, e.g. email1, email2. */
export function newId(g: JourneyGraph, type: string): string {
  const base = ID_BASE[type] ?? type.replace(/^(send|on|if)_/, "");
  let n = 1;
  while (g.nodes[`${base}${n}`] || `${base}${n}` === TRIGGER_ID) n++;
  return `${base}${n}`;
}

/** defaults a new step of a type starts with. */
export function defaultsFor(g: JourneyGraph, type: string): Partial<JourneyNode> {
  switch (type) {
    case "wait": return { seconds: 86400 };
    case "wait_until": return { time_of_day: "09:00", timezone: "Asia/Kolkata" };
    case "wait_for_event": case "on_form": case "on_asset": case "on_page": case "on_dwc": return { seconds: 86400 };
    case "on_device": return { device_type: "mobile", seconds: 86400 };
    case "split": return { variants: [{ weight: 50, label: "A" }, { weight: 50, label: "B" }] };
    case "update_attr": return { attrs: {} };
    case "update_tags": return { add_tags: [] };
    case "adjust_points": case "company_points": return { points: 10 };
    case "update_list": return { add_lists: [] };
    case "change_journey": return { add_journeys: [] };
    case "update_company": return { company_attrs: {} };
    case "if_journey": return { journey_ids: [], journey_state: "active" };
    case "if_form_field": return { field: "", op: "eq", value: "" };
    case "if_notification": return { notify: "any" };
    case "if_dnc": return { channel: "any" };
    case "dnc_add": case "dnc_remove": return { channel: "email" };
    case "if_visited": return { url: "", days: 30 };
    case "notify_user": return { to_owner: true };
    case "send_message": return { channels: [{ channel: "push" }, { channel: "email" }] };
    case "push_dwc": return { slot: "", html: "", days: 30 };
    case "message_decision": {
      // With a single send step there's only one message to watch, so choose it.
      const sends = sendSteps(g);
      return sends.length === 1 ? { seconds: 2 * 86400, step: sends[0][0], outcome: OUTCOMES[sends[0][2]][0] } : { seconds: 2 * 86400 };
    }
  }
  return {};
}

/** addStep adds an unconnected step at a canvas position (a palette drop). */
export function addStep(g: JourneyGraph, node: Partial<JourneyNode> & { type: string }, x: number, y: number): { graph: JourneyGraph; id: string } {
  const id = newId(g, node.type);
  return { graph: { ...g, nodes: { ...g.nodes, [id]: { ...node, x: Math.round(x), y: Math.round(y) } } }, id };
}

export function addNode(g: JourneyGraph, type: string, x: number, y: number): { graph: JourneyGraph; id: string } {
  return addStep(g, { type, ...defaultsFor(g, type) }, x, y);
}

export function moveNode(g: JourneyGraph, id: string, x: number, y: number): JourneyGraph {
  const n = g.nodes[id];
  return n ? { ...g, nodes: { ...g.nodes, [id]: { ...n, x: Math.round(x), y: Math.round(y) } } } : g;
}

/** shiftDown moves a step and every step after it down by dy. */
function shiftDown(g: JourneyGraph, from: string, dy: number): JourneyGraph {
  const pos = positions(g);
  const nodes = { ...g.nodes };
  for (const id of descendants(g, from)) nodes[id] = { ...nodes[id], x: pos[id].x, y: pos[id].y + dy };
  return { ...g, nodes };
}

/** insertProblem explains why a step type can't go at an insertion point, or null. */
export function insertProblem(g: JourneyGraph, at: InsertAt, type: string): string | null {
  if (at.source !== TRIGGER_ID && !g.nodes[at.source]) return "That step no longer exists.";
  if (at.source !== TRIGGER_ID && !outputs(g.nodes[at.source]).some(([h]) => h === at.handle)) return "That path no longer exists.";
  const b = targetAt(g, at);
  if (isTerminal(type) && b && g.nodes[b].type !== "exit") return "Ends the path: add it at the end of a path";
  return null;
}

/** insertStep puts a new step after an output handle. On an edge A→B the step goes between: A→new
 * and new's first output (next, Yes, or the first split path)→B, its other outputs left free, and
 * B's branch moves down a row to make room. A step that ends the path (Exit, Jump, Delete contact)
 * can only go on a free handle or replace an Exit. Returns null when the insertion isn't allowed. */
export function insertStep(g: JourneyGraph, at: InsertAt, node: Partial<JourneyNode> & { type: string }): { graph: JourneyGraph; id: string } | null {
  if (insertProblem(g, at, node.type)) return null;
  const b = targetAt(g, at);
  const id = newId(g, node.type);
  const pos = positions(g);
  let out = g;
  let x: number, y: number;
  const terminal = isTerminal(node.type);
  if (b) {
    ({ x, y } = pos[b]);
    if (!terminal) out = shiftDown(out, b, ROW);
  } else {
    const src = pos[at.source] ?? pos[TRIGGER_ID];
    const outs = at.source === TRIGGER_ID ? [["next"]] : outputs(g.nodes[at.source]);
    const i = Math.max(0, outs.findIndex(([h]) => h === at.handle));
    x = src.x + (i - (outs.length - 1) / 2) * COL;
    y = src.y + ROW;
  }
  const fresh: JourneyNode = { ...node, x: Math.round(x), y: Math.round(y) };
  const first = firstOutput(fresh);
  out = { ...out, nodes: { ...out.nodes, [id]: fresh } };
  if (b && !terminal && first) out = connect(out, id, first, b);
  out = connect(out, at.source, at.handle, id);
  if (b && terminal && !parentsOf(out).has(b)) out = removeNode(out, b);
  return { graph: out, id };
}

/** freeOutputs lists a step's handles that lead nowhere yet. */
export function freeOutputs(g: JourneyGraph, id: string): string[] {
  if (id === TRIGGER_ID) return g.nodes[g.entry] ? [] : ["next"];
  const n = g.nodes[id];
  return n ? outputs(n).filter(([h]) => !targetAt(g, { source: id, handle: h })).map(([h]) => h) : [];
}

/** defaultInsertPoint: where the palette adds a step: the selected step's first free output, else
 * right after it; with nothing (or an end step) selected, the first free output in step order, else
 * right after Start. */
export function defaultInsertPoint(g: JourneyGraph, selected: string | null): InsertAt {
  if (selected && (selected === TRIGGER_ID || g.nodes[selected])) {
    const free = freeOutputs(g, selected);
    if (free.length) return { source: selected, handle: free[0] };
    const first = selected === TRIGGER_ID ? "next" : firstOutput(g.nodes[selected]);
    if (first) return { source: selected, handle: first };
  }
  if (freeOutputs(g, TRIGGER_ID).length) return { source: TRIGGER_ID, handle: "next" };
  for (const id of numberedOrder(g)) {
    const free = freeOutputs(g, id);
    if (free.length) return { source: id, handle: free[0] };
  }
  return { source: TRIGGER_ID, handle: "next" };
}

// ---- step numbers and layout ----

/** numberedOrder lists steps breadth first from Start (handles in order), then steps no path
 * reaches, in the order they were added. Jump targets aren't followed. */
export function numberedOrder(g: JourneyGraph): string[] {
  const order: string[] = [];
  const seen = new Set<string>();
  const queue = g.nodes[g.entry] ? [g.entry] : [];
  while (queue.length) {
    const id = queue.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    order.push(id);
    queue.push(...childrenOf(g, id));
  }
  for (const id of Object.keys(g.nodes)) if (!seen.has(id)) order.push(id);
  return order;
}

/** stepNumbers: 1, 2, 3… in numberedOrder, as shown on the canvas and in Jump to step pickers. */
export function stepNumbers(g: JourneyGraph): Record<string, number> {
  return Object.fromEntries(numberedOrder(g).map((id, i) => [id, i + 1]));
}

/** tidy lays the journey out top-down: each step one row below its lowest parent, children spread
 * left to right in handle order (Yes before No, path A before B) under their parent, and branches
 * side by side so no two cards overlap. Steps no path reaches go to the right. Deterministic. */
/** overlapping reports whether two steps' cards would cover each other (cards are about 230 x 90). */
export function overlapping(g: JourneyGraph): boolean {
  const ps = Object.values(g.nodes).map((n) => [n.x ?? 0, n.y ?? 0]);
  for (let i = 0; i < ps.length; i++) {
    for (let j = i + 1; j < ps.length; j++) {
      if (Math.abs(ps[i][0] - ps[j][0]) < 240 && Math.abs(ps[i][1] - ps[j][1]) < 100) return true;
    }
  }
  return false;
}

export function tidy(g: JourneyGraph): JourneyGraph {
  const ids = Object.keys(g.nodes);
  if (ids.length === 0) return g;
  // Depth: the longest path from a root (Kahn's algorithm; edges only go down).
  const parents = parentsOf(g);
  const indeg = new Map(ids.map((id) => [id, (parents.get(id) ?? []).filter((p) => p.source !== TRIGGER_ID).length]));
  const depth = new Map<string, number>();
  const roots = [g.entry, ...ids.filter((id) => id !== g.entry)].filter((id) => g.nodes[id] && indeg.get(id) === 0);
  const queue = [...roots];
  for (const r of roots) depth.set(r, 0);
  while (queue.length) {
    const id = queue.shift()!;
    for (const c of childrenOf(g, id)) {
      depth.set(c, Math.max(depth.get(c) ?? 0, (depth.get(id) ?? 0) + 1));
      indeg.set(c, (indeg.get(c) ?? 0) - 1);
      if (indeg.get(c) === 0) queue.push(c);
    }
  }
  // Steps on a loop (the server refuses these) go on rows of their own at the bottom.
  let bottom = Math.max(0, ...depth.values()) + 1;
  for (const id of ids) if (!depth.has(id)) depth.set(id, bottom++);
  // A spanning tree: each step hangs under the first parent that reaches it, depth first.
  const kids = new Map<string, string[]>();
  const placed = new Set<string>();
  const visit = (id: string) => {
    placed.add(id);
    const mine: string[] = [];
    for (const c of childrenOf(g, id)) {
      if (placed.has(c) || (depth.get(c) ?? 0) <= (depth.get(id) ?? 0)) continue;
      placed.add(c);
      mine.push(c);
    }
    kids.set(id, mine);
    for (const c of mine) visit(c);
  };
  const treeRoots: string[] = [];
  for (const r of [g.entry, ...roots, ...ids]) {
    if (!g.nodes[r] || placed.has(r)) continue;
    treeRoots.push(r);
    visit(r);
  }
  // Columns: leaves take the next slot; a parent sits midway over its first and last child.
  const col = new Map<string, number>();
  let slot = 0;
  const place = (id: string) => {
    const ks = kids.get(id) ?? [];
    if (ks.length === 0) { col.set(id, slot++); return; }
    for (const k of ks) place(k);
    col.set(id, (col.get(ks[0])! + col.get(ks[ks.length - 1])!) / 2);
  };
  treeRoots.forEach((r, i) => { if (i > 0) slot++; place(r); });
  const nodes: Record<string, JourneyNode> = {};
  for (const id of ids) nodes[id] = { ...g.nodes[id], x: Math.round(col.get(id)! * COL), y: (depth.get(id)! + 1) * ROW };
  return { ...g, nodes };
}

// ---- validation: what the server would refuse, in words ----

export type Issue = { step?: string; text: string };

/** issues lists what the server would reject, so the canvas can show them before saving; each
 * names its step, if any, so the list can select it. journeyId is the journey being edited. */
export function issues(g: JourneyGraph, opts: { journeyId?: number } = {}): Issue[] {
  const out: Issue[] = [];
  const top = (text: string) => out.push({ text });
  if (g.trigger.type === "segment_entered" && !g.trigger.segment_id) top("Choose the segment that starts this journey.");
  if (g.trigger.type === "event" && !EVENT_RE.test(g.trigger.event ?? "")) top("Enter the event that starts this journey.");
  if (!g.entry || !g.nodes[g.entry]) top("Connect Start to the first step.");
  if (g.trigger.reentry === "after_days" && !(g.trigger.days && g.trigger.days >= 1 && g.trigger.days <= 3650)) top("Set after how many days (1 to 3650) a contact can enter again.");
  if (g.goal && !EVENT_RE.test(g.goal.event)) top("Enter the goal event, or remove the goal.");
  const nums = stepNumbers(g);
  const self = opts.journeyId;
  for (const [id, n] of Object.entries(g.nodes)) {
    const add = (text: string) => out.push({ step: id, text });
    for (const [h, label] of outputs(n)) {
      if (!targetAt(g, { source: id, handle: h })) add(label ? `connect the "${label.replace(/ · \d+%$/, "")}" path.` : "connect it to the next step.");
    }
    const shape = shapeOf(n.type);
    // Same limits as the server: a wait for any event may be as short as 1 second (older journeys use it).
    if (shape === "decision" && n.type === "wait_for_event" && !(n.seconds !== undefined && n.seconds >= 1 && n.seconds <= MAX_WAIT)) add("wait between 1 second and 365 days.");
    if (shape === "decision" && n.type !== "wait_for_event" && !(n.seconds !== undefined && n.seconds >= DECISION_MIN && n.seconds <= MAX_WAIT)) add("wait between 1 minute and 365 days.");
    if (!typeInfo(n.type)) add(`"${n.type}" isn't a step this version knows.`);
    const late = deadlineProblem(n.deadline);
    if (late) add(late[0].toLowerCase() + late.slice(1));
    switch (n.type) {
      case "condition": if (!n.segment_id) add("choose a segment to check."); break;
      case "wait_until": if (!n.until && !n.time_of_day) add("choose when to continue."); break;
      case "wait": if (!(n.seconds && n.seconds > 0 && n.seconds <= MAX_WAIT)) add("set how long to wait (up to 365 days)."); break;
      case "wait_for_event": if (!EVENT_RE.test(n.event ?? "")) add("enter the event to wait for."); break;
      case "check": if (!n.rule) add('describe who should take the "Yes" path.'); break;
      case "update_attr": if (Object.keys(n.attrs ?? {}).length === 0) add("set at least one field."); break;
      case "webhook": if (!/^https?:\/\/.+/.test(n.url ?? "")) add("enter the webhook's https:// address."); break;
      case "update_tags":
        if ((n.add_tags?.length ?? 0) + (n.remove_tags?.length ?? 0) === 0) add("add or remove at least one tag.");
        if ([...(n.add_tags ?? []), ...(n.remove_tags ?? [])].some((t) => !TAG_RE.test(t))) add("tags use letters, digits, - and _.");
        break;
      case "adjust_points": case "company_points":
        if (!(Number.isInteger(n.points) && n.points !== 0 && Math.abs(n.points!) <= 100000)) add("enter a whole number of points other than 0 (up to 100,000 either way).");
        break;
      case "update_list": {
        const a = n.add_lists ?? [], r = n.remove_lists ?? [];
        if (n.list_id !== undefined || n.list_action !== undefined) {
          if (!n.list_id || (n.list_action !== "add" && n.list_action !== "remove")) add("choose a static segment.");
        } else if (a.length + r.length === 0) add("choose at least one static segment to add to or remove from.");
        else if (a.some((x) => r.includes(x))) add("a static segment can't be both added to and removed from.");
        break;
      }
      case "change_stage": if (!n.stage?.trim()) add("choose the stage to move the contact to."); break;
      case "split":
        if ((n.variants ?? []).length < 2 || (n.variants ?? []).length > 5) add("a split needs 2 to 5 paths.");
        if ((n.variants ?? []).some((v) => !(v.weight >= 1))) add("every path needs a share of at least 1.");
        if (winnerProblem(g, id)) add(winnerProblem(g, id)!);
        break;
      case "message_decision": {
        const step = n.step ? g.nodes[n.step] : undefined;
        const ch = step ? nodeChannel(step.type) : undefined;
        if (!ch) add("choose the message it waits on.");
        else {
          if (!upstream(g, id).has(n.step!)) add("the message it waits on must be sent earlier on this path.");
          if (!n.outcome || !OUTCOMES[ch].includes(n.outcome)) add(`choose what should happen to the ${CHANNEL_NAMES[ch]} message.`);
        }
        if (n.url && n.outcome !== "clicked") add('a link only applies to "a link is clicked".');
        break;
      }
      case "on_page": if (n.url && n.page_id) add("choose a URL or a landing page, not both."); break;
      case "on_device":
        if (!n.device_type && !n.os?.trim()) add("choose a device type or an operating system.");
        if (n.device_type && !(DEVICE_TYPES as readonly string[]).includes(n.device_type)) add("the device type is desktop, mobile or tablet.");
        break;
      case "on_dwc": if (!SLOT_RE.test(n.slot ?? "")) add("choose the dynamic content slot (lowercase letters, digits, - and _)."); break;
      case "if_journey": {
        const ids = n.journey_ids ?? [];
        if (ids.length === 0) add("choose at least one journey.");
        if (ids.length > 20) add("choose at most 20 journeys.");
        if (self !== undefined && ids.includes(self)) add("check other journeys, not this one.");
        if (!["active", "completed", "entered"].includes(n.journey_state ?? "")) add("choose whether they're in it, finished it or ever entered it.");
        break;
      }
      case "if_form_field":
        if (!n.field?.trim()) add("choose the form field to check.");
        if (!(FORM_FIELD_OPS as readonly string[]).includes(n.op ?? "")) add("choose how to compare the field.");
        else if (n.op !== "exists" && n.op !== "not_exists" && !(n.value ?? "").trim()) add("enter the value to compare with.");
        break;
      case "if_notification": if (!["push", "inbox", "any"].includes(n.notify ?? "")) add("choose push, app inbox or either."); break;
      case "if_dnc": if (![...DNC_CHANNELS, "any"].includes(n.channel ?? "")) add("choose the channel to check."); break;
      case "if_before": if (!n.until || Number.isNaN(Date.parse(n.until))) add("choose the date after which contacts take No."); break;
      case "if_visited": if (!(Number.isInteger(n.days) && n.days! >= 0 && n.days! <= 365)) add("choose how far back to look (0 for ever, or 1 to 365 days)."); break;
      case "notify_user": {
        if (!n.template_id) add("choose the email template.");
        const users = n.user_ids ?? [], emails = n.emails ?? [];
        if (users.length === 0 && emails.length === 0 && !n.to_owner) add("choose who gets the email.");
        if (users.length > 20 || emails.length > 20) add("send to at most 20 teammates and 20 addresses.");
        if (emails.some((e) => !EMAIL_RE.test(e))) add("check the email addresses.");
        break;
      }
      case "send_message": for (const p of channelRuleProblems(n.channels ?? [])) add(p); break;
      case "show_focus": if (!n.focus_id) add("choose the focus item to show."); break;
      case "push_integration": if (!n.integration_id) add("choose the CRM to push the contact to."); break;
      case "push_dwc":
        if (!SLOT_RE.test(n.slot ?? "")) add("choose the dynamic content slot (lowercase letters, digits, - and _).");
        if (!(n.html ?? "").trim()) add("enter the content to show.");
        else if (new TextEncoder().encode(n.html).length > 64 * 1024) add("the content can be at most 64 KB.");
        if (n.days !== undefined && !(Number.isInteger(n.days) && n.days >= 1 && n.days <= 365)) add("keep it for 1 to 365 days.");
        break;
      case "update_owner": if (n.owner_id === undefined || n.owner_id < 0) add("choose the new owner, or to unassign."); break;
      case "update_company": {
        const keys = Object.keys(n.company_attrs ?? {});
        if (keys.length === 0 || keys.length > 50) add("set 1 to 50 company fields.");
        if (keys.some((k) => !COMPANY_KEY_RE.test(k) || k === "id" || k === "name")) add("company fields use letters, digits and _, and can't be id or name.");
        break;
      }
      case "add_company": if (!n.company_id) add("choose the company."); break;
      case "change_journey": {
        const a = n.add_journeys ?? [], r = n.remove_journeys ?? [];
        if (a.length + r.length === 0) add("choose a journey to add the contact to or remove them from.");
        if (a.length > 20 || r.length > 20) add("choose at most 20 journeys each way.");
        if (a.some((x) => r.includes(x))) add("a journey can't be both added and removed.");
        if (self !== undefined && (a.includes(self) || r.includes(self))) add("choose other journeys, not this one.");
        break;
      }
      case "jump": {
        const t = n.target ? g.nodes[n.target] : undefined;
        if (!t) add("choose the step to jump to.");
        else if (n.target === id) add("a step can't jump to itself.");
        else if (t.type === "jump") add("jump to a step that isn't another jump.");
        break;
      }
      case "dnc_add": case "dnc_remove":
        if (![...DNC_CHANNELS, "all"].includes(n.channel ?? "")) add("choose the channel.");
        if ((n.reason ?? "").length > 200) add("keep the reason under 200 characters.");
        break;
      case "delete_contact": if (n.confirm !== "DELETE") add("type DELETE to confirm it permanently deletes contacts."); break;
    }
    if (nodeChannel(n.type) && !n.template_id) add("choose a template.");
  }
  const loop = cycleAt(g);
  if (loop) out.push({ step: loop, text: "the journey loops back here. Use Jump to step to repeat steps." });
  // Prefix step problems with the step's number and name.
  return out.map((p) => (p.step ? { step: p.step, text: `Step ${nums[p.step]} · ${stepLabel(g, g.nodes[p.step])}: ${p.text}` } : p));
}

/** problems lists issues as sentences. */
export function problems(g: JourneyGraph, opts: { journeyId?: number } = {}): string[] {
  return issues(g, opts).map((p) => p.text);
}

/** cycleAt returns a step on a loop (jump targets aren't edges), or undefined. */
export function cycleAt(g: JourneyGraph): string | undefined {
  const state = new Map<string, 1 | 2>();
  let found: string | undefined;
  const visit = (id: string): boolean => {
    const s = state.get(id);
    if (s === 1) { found = id; return true; }
    if (s === 2) return false;
    state.set(id, 1);
    for (const c of childrenOf(g, id)) if (visit(c)) return true;
    state.set(id, 2);
    return false;
  };
  for (const id of Object.keys(g.nodes)) if (visit(id)) return found;
  return undefined;
}

/** trackedSendsAfter lists the email and app inbox steps reachable from a split: what an A/B winner
 * by opens or clicks is measured on (same rule as the server). */
export function trackedSendsAfter(g: JourneyGraph, split: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  const walk = (id?: string) => {
    if (!id || seen.has(id) || !g.nodes[id]) return;
    seen.add(id);
    const n = g.nodes[id];
    if (n.type === "send_email" || n.type === "send_inbox") out.push(id);
    for (const [h] of outputs(n)) walk(targetOf(n, h));
  };
  for (const [h] of outputs(g.nodes[split] ?? { type: "exit" })) walk(targetOf(g.nodes[split], h));
  return out.sort();
}

/** winnerProblem explains why a split's automatic winner setting would be refused, or null. */
export function winnerProblem(g: JourneyGraph, id: string): string | null {
  const w = g.nodes[id]?.winner;
  if (!w) return null;
  if (w.metric === "converted" && !g.goal) return "A winner by conversions needs the journey's goal event (set it on Start).";
  if (w.metric !== "converted" && trackedSendsAfter(g, id).length === 0) return `A winner by ${w.metric === "opened" ? "opens" : "clicks"} needs an email or app inbox step after the split.`;
  if (!(w.after_seconds >= 1 && w.after_seconds <= 90 * 86400)) return "Test for between 1 second and 90 days.";
  if (!(Number.isInteger(w.min_per_variant) && w.min_per_variant >= 1 && w.min_per_variant <= 1_000_000)) return "Each path needs 1 to 1,000,000 contacts before a winner is picked.";
  return null;
}

export function formatWait(seconds = 0): string {
  if (seconds % 86400 === 0) return `${seconds / 86400} day${seconds === 86400 ? "" : "s"}`;
  if (seconds % 3600 === 0) return `${seconds / 3600} hour${seconds === 3600 ? "" : "s"}`;
  if (seconds % 60 === 0) return `${seconds / 60} min`;
  return `${seconds} s`;
}

// ---- the journey list ----

export type ListAction = "delete" | "archive" | "restore";

/** listActions: what the journey list offers for a journey. A live journey is paused first; one
 * that never ran can be deleted; one that ran is archived instead (its enrollments are kept). */
export function listActions(j: { status: string; has_run?: boolean; archived_at?: string | null }): ListAction[] {
  if (j.archived_at) return ["restore"];
  if (j.status === "active") return [];
  return j.has_run ? ["archive"] : ["delete"];
}

/** visibleJourneys hides archived journeys unless asked to show them. */
export function visibleJourneys<T extends { archived_at?: string | null }>(js: T[], showArchived: boolean): T[] {
  return showArchived ? js : js.filter((j) => !j.archived_at);
}

// ---- send_message rules ----

const RULE_OP_WORDS: Record<string, string> = { eq: "is", ne: "is not", contains: "contains", starts_with: "starts with", gt: ">", lt: "<", exists: "is set", not_exists: "is not set" };

/** channelRuleProblems: what a "Send marketing message" step's rules still need (as the server checks). */
export function channelRuleProblems(cs: ChannelRule[]): string[] {
  const out: string[] = [];
  if (cs.length === 0 || cs.length > 10) out.push("add 1 to 10 rules.");
  const plain = cs.filter((c) => !c.when?.length).map((c) => c.channel);
  if (new Set(plain).size !== plain.length) out.push("use each channel once in rules without conditions.");
  if (cs.some((c) => !c.template_id)) out.push("choose a template for every rule.");
  for (const c of cs) for (const w of c.when ?? []) {
    if (!/^(contact|event)\.[A-Za-z0-9_]+(\[\])?(\.[A-Za-z0-9_]+(\[\])?){0,2}$/.test(w.prop)) { out.push(`conditions read contact.<field> or event.<property>, not “${w.prop || "(empty)"}”.`); break; }
    if (!(w.op in RULE_OP_WORDS)) { out.push(`choose how to compare ${w.prop}.`); break; }
    if (w.op !== "exists" && w.op !== "not_exists" && (w.value === undefined || w.value === "")) { out.push(`enter the value ${w.prop} is compared with.`); break; }
  }
  return [...new Set(out)];
}

/** rulesText describes the rules: "phone_country is IN → SMS; otherwise WhatsApp". */
export function rulesText(cs: ChannelRule[], channelName: (c: string) => string): string {
  if (!cs.length) return "Choose channels";
  if (!cs.some((c) => c.when?.length)) return `Send by ${cs.map((c) => channelName(c.channel)).join(", then ")}`;
  return cs.map((c, i) => {
    if (!c.when?.length) return `${i ? "otherwise " : ""}${channelName(c.channel)}`;
    const cond = c.when.map((w) => `${w.prop.replace(/^contact\./, "")} ${RULE_OP_WORDS[w.op] ?? w.op}${w.op === "exists" || w.op === "not_exists" ? "" : ` ${String(w.value ?? "")}`}`).join(" and ");
    return `${cond} → ${channelName(c.channel)}`;
  }).join("; ");
}
