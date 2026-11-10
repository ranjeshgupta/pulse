// Converts between the journey graph the API stores and React Flow's nodes and edges. The graph
// is the source of truth: edges are derived from each node's next / yes / no fields, so every edit
// on the canvas is an edit of the graph.
import type { Edge, Node as FlowNode } from "@xyflow/react";
import type { Channel, JourneyGraph, JourneyNode } from "./api";

export const TRIGGER_ID = "__trigger";

export const NODE_TYPES: { type: string; label: string; channel?: Channel }[] = [
  { type: "send_email", label: "Send email", channel: "email" },
  { type: "send_sms", label: "Send SMS", channel: "sms" },
  { type: "send_whatsapp", label: "Send WhatsApp", channel: "whatsapp" },
  { type: "send_rcs", label: "Send RCS", channel: "rcs" },
  { type: "send_push", label: "Send push", channel: "push" },
  { type: "wait", label: "Wait" },
  { type: "wait_until", label: "Wait until" },
  { type: "wait_for_event", label: "Wait for event" },
  { type: "condition", label: "Check segment" },
  { type: "check", label: "Check contact" },
  { type: "split", label: "A/B split" },
  { type: "update_attr", label: "Update profile" },
  { type: "update_tags", label: "Tag contact" },
  { type: "adjust_points", label: "Adjust points" },
  { type: "update_list", label: "Add to list" },
  { type: "webhook", label: "Call webhook" },
  { type: "exit", label: "Exit" },
];

// Which outgoing handles a step has: [handle id, label].
export function outputs(n: JourneyNode): [string, string | undefined][] {
  switch (n.type) {
    case "exit": return [];
    case "condition": return [["yes", "in segment"], ["no", "not in segment"]];
    case "check": return [["yes", "matches"], ["no", "doesn't match"]];
    case "wait_for_event": return [["yes", "did it"], ["no", "timed out"]];
    case "split": return (n.variants ?? []).map((v, i) => [`v${i}`, `${v.label || String.fromCharCode(65 + i)} ${share(n, i)}%`]);
    default: return [["next", undefined]];
  }
}

export function share(n: JourneyNode, i: number) {
  const total = (n.variants ?? []).reduce((a, v) => a + (v.weight || 0), 0) || 1;
  return Math.round(((n.variants?.[i]?.weight ?? 0) / total) * 100);
}

function target(n: JourneyNode, handle: string): string | undefined {
  if (handle.startsWith("v")) return n.variants?.[Number(handle.slice(1))]?.next;
  return n[handle as "next" | "yes" | "no"];
}

export const nodeLabel = (type: string) => NODE_TYPES.find((n) => n.type === type)?.label ?? type;
export const nodeChannel = (type: string) => NODE_TYPES.find((n) => n.type === type)?.channel;

export type FlowData = { id: string; node?: JourneyNode; isTrigger?: boolean };

export function emptyGraph(): JourneyGraph {
  return {
    trigger: { type: "segment_entered" },
    entry: "exit",
    nodes: { exit: { type: "exit", x: 300, y: 220 } },
  };
}

export function toFlow(g: JourneyGraph): { nodes: FlowNode<FlowData>[]; edges: Edge[] } {
  const nodes: FlowNode<FlowData>[] = [
    { id: TRIGGER_ID, type: "pulse", position: { x: 300, y: 20 }, data: { id: TRIGGER_ID, isTrigger: true }, deletable: false },
  ];
  const edges: Edge[] = [];
  const edge = (source: string, target: string | undefined, handle: string, label?: string) => {
    if (target && (g.nodes[target] || target === TRIGGER_ID)) {
      edges.push({ id: `${source}:${handle}`, source, sourceHandle: handle, target, label, type: "smoothstep" });
    }
  };
  edge(TRIGGER_ID, g.entry, "next");
  let i = 0;
  for (const [id, n] of Object.entries(g.nodes)) {
    nodes.push({ id, type: "pulse", position: { x: n.x ?? 300, y: n.y ?? 140 + i++ * 120 }, data: { id, node: n } });
    for (const [h, label] of outputs(n)) edge(id, target(n, h), h, label);
  }
  return { nodes, edges };
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
  if (source === TRIGGER_ID) return { ...g, entry: "" };
  const n = g.nodes[source];
  if (!n) return g;
  const copy = { ...n };
  if (handle.startsWith("v")) {
    const i = Number(handle.slice(1));
    copy.variants = (n.variants ?? []).map((v, j) => (j === i ? { ...v, next: undefined } : v));
  } else delete copy[handle as "next" | "yes" | "no"];
  return { ...g, nodes: { ...g.nodes, [source]: copy } };
}

export function removeNode(g: JourneyGraph, id: string): JourneyGraph {
  const nodes: Record<string, JourneyNode> = {};
  for (const [k, n] of Object.entries(g.nodes)) {
    if (k === id) continue;
    const copy = { ...n };
    for (const f of ["next", "yes", "no"] as const) if (copy[f] === id) delete copy[f];
    if (copy.variants) copy.variants = copy.variants.map((v) => (v.next === id ? { ...v, next: undefined } : v));
    nodes[k] = copy;
  }
  return { ...g, entry: g.entry === id ? "" : g.entry, nodes };
}

export function addNode(g: JourneyGraph, type: string, x: number, y: number): { graph: JourneyGraph; id: string } {
  let n = 1;
  const base = type.replace("send_", "");
  while (g.nodes[`${base}${n}`]) n++;
  const id = `${base}${n}`;
  const node: JourneyNode = { type, x, y };
  if (type === "wait") node.seconds = 86400;
  if (type === "wait_until") Object.assign(node, { time_of_day: "09:00", timezone: "Asia/Kolkata" });
  if (type === "wait_for_event") Object.assign(node, { seconds: 86400 });
  if (type === "split") node.variants = [{ weight: 50, label: "A" }, { weight: 50, label: "B" }];
  if (type === "update_attr") node.attrs = {};
  if (type === "update_tags") node.add_tags = [];
  if (type === "adjust_points") node.points = 10;
  if (type === "update_list") node.list_action = "add";
  return { graph: { ...g, nodes: { ...g.nodes, [id]: node } }, id };
}

export function moveNode(g: JourneyGraph, id: string, x: number, y: number): JourneyGraph {
  const n = g.nodes[id];
  return n ? { ...g, nodes: { ...g.nodes, [id]: { ...n, x: Math.round(x), y: Math.round(y) } } } : g;
}

// problems lists what the server would reject, in words, so the canvas can show them before saving.
export function problems(g: JourneyGraph): string[] {
  const out: string[] = [];
  if (g.trigger.type === "segment_entered" && !g.trigger.segment_id) out.push("Choose the segment that starts this journey.");
  if (g.trigger.type === "event" && !/^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/.test(g.trigger.event ?? "")) out.push("Enter the event that starts this journey.");
  if (!g.entry || !g.nodes[g.entry]) out.push("Connect the trigger to the first step.");
  if (g.trigger.reentry === "after_days" && !(g.trigger.days && g.trigger.days >= 1)) out.push("Set after how many days a contact can enter again.");
  if (g.goal && !/^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/.test(g.goal.event)) out.push("Enter the goal event, or remove the goal.");
  for (const [id, n] of Object.entries(g.nodes)) {
    const name = `"${id}"`;
    for (const [h, label] of outputs(n)) {
      if (!target(n, h)) out.push(label ? `${name}: connect the "${label.replace(/ \d+%$/, "")}" path.` : `${name}: connect it to the next step.`);
    }
    if (n.type === "condition" && !n.segment_id) out.push(`${name}: choose a segment to check.`);
    if (n.type === "wait_until" && !n.until && !n.time_of_day) out.push(`${name}: choose when to continue.`);
    if (n.type === "wait_for_event" && !/^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/.test(n.event ?? "")) out.push(`${name}: enter the event to wait for.`);
    if (n.type === "check" && !n.rule) out.push(`${name}: describe who should take the "matches" path.`);
    if (n.type === "update_attr" && Object.keys(n.attrs ?? {}).length === 0) out.push(`${name}: set at least one attribute.`);
    if (n.type === "webhook" && !/^https?:\/\/.+/.test(n.url ?? "")) out.push(`${name}: enter the webhook's https:// address.`);
    if (n.type === "update_tags" && (n.add_tags?.length ?? 0) + (n.remove_tags?.length ?? 0) === 0) out.push(`${name}: add or remove at least one tag.`);
    if (n.type === "update_tags" && [...(n.add_tags ?? []), ...(n.remove_tags ?? [])].some((t) => !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(t))) out.push(`${name}: tags use letters, digits, - and _.`);
    if (n.type === "adjust_points" && !(Number.isInteger(n.points) && n.points !== 0)) out.push(`${name}: enter a whole number of points other than 0.`);
    if (n.type === "update_list" && !n.list_id) out.push(`${name}: choose a list.`);
    if (n.type === "split" && (n.variants ?? []).some((v) => !(v.weight >= 1))) out.push(`${name}: every path needs a share of at least 1.`);
    if (n.type.startsWith("send_") && !n.template_id) out.push(`${name}: choose a template.`);
    if (n.type === "wait" && !(n.seconds && n.seconds > 0)) out.push(`${name}: set how long to wait.`);
  }
  return out;
}

export function formatWait(seconds = 0): string {
  if (seconds % 86400 === 0) return `${seconds / 86400} day${seconds === 86400 ? "" : "s"}`;
  if (seconds % 3600 === 0) return `${seconds / 3600} hour${seconds === 3600 ? "" : "s"}`;
  if (seconds % 60 === 0) return `${seconds / 60} min`;
  return `${seconds} s`;
}
