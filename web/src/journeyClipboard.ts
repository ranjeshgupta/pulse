// Copy and paste of journey steps (M3). A copy is JSON on the system clipboard, so steps can move
// between journeys and browser tabs. The server checks a clipboard before it is pasted
// (POST /v1/journey-steps/check: links to steps that weren't copied and references to another
// workspace's templates or segments are dropped, with a note); pasteSteps then gives the steps new
// ids and inserts them where the next step would go.

import type { JourneyGraph, JourneyNode } from "./api";
import { isUnsafeKey } from "./safe";
import { connect, firstOutput, newId, numberedOrder, outputs, positions, ROW, targetAt, targetOf, TRIGGER_ID, typeInfo, type InsertAt } from "./journeyGraph";

export const CLIPBOARD_KIND = "journey-steps";
export const MAX_CLIPBOARD_STEPS = 100;

export interface StepClipboard {
  pulse: typeof CLIPBOARD_KIND;
  version: 1;
  /** The first step: the one the paste point leads to. */
  entry: string;
  nodes: Record<string, JourneyNode>;
  /** The journey copied from (informational). */
  from?: number;
}

/** edges maps a step's outgoing edges (next, yes/no or split paths). */
function mapEdges(n: JourneyNode, f: (t: string | undefined) => string | undefined): JourneyNode {
  const out: JourneyNode = { ...n };
  if (n.variants) out.variants = n.variants.map((v) => ({ ...v, next: f(v.next) }));
  for (const k of ["next", "yes", "no"] as const) {
    if (k in n) {
      const t = f(n[k]);
      if (t) out[k] = t;
      else delete out[k];
    }
  }
  return out;
}

/** copySteps copies a selection of steps (the trigger is never copied). Edges to steps outside the
 * selection are dropped; the entry is the selected step that comes first in the journey's order. */
export function copySteps(g: JourneyGraph, ids: Iterable<string>, from?: number): StepClipboard | null {
  const pick = new Set([...ids].filter((id) => id !== TRIGGER_ID && g.nodes[id]));
  if (pick.size === 0) return null;
  const nodes: Record<string, JourneyNode> = {};
  for (const id of pick) nodes[id] = mapEdges(g.nodes[id], (t) => (t && pick.has(t) ? t : undefined));
  const order = numberedOrder(g).filter((id) => pick.has(id));
  // The entry is a step none of the other copied steps leads to, first in the journey's order.
  const led = new Set(Object.values(nodes).flatMap((n) => outputs(n).map(([h]) => targetOf(n, h)).filter(Boolean) as string[]));
  const entry = order.find((id) => !led.has(id)) ?? order[0];
  return { pulse: CLIPBOARD_KIND, version: 1, entry, nodes, ...(from ? { from } : {}) };
}

/** copyBranch copies a step and every step after it (not following jumps). */
export function branchIds(g: JourneyGraph, id: string): string[] {
  const seen: string[] = [];
  const stack = [id];
  while (stack.length) {
    const x = stack.pop()!;
    if (seen.includes(x) || !g.nodes[x]) continue;
    seen.push(x);
    const n = g.nodes[x];
    for (const [h] of outputs(n)) { const t = targetOf(n, h); if (t) stack.push(t); }
  }
  return seen;
}

/** parseClipboard reads clipboard text, or explains why it isn't journey steps. */
export function parseClipboard(text: string): { clip?: StepClipboard; error?: string } {
  let v: unknown;
  try { v = JSON.parse(text); } catch { return { error: "The clipboard doesn't hold journey steps. Copy steps first (select them, then Copy)." }; }
  const c = v as Partial<StepClipboard>;
  if (!c || typeof c !== "object" || c.pulse !== CLIPBOARD_KIND) return { error: "The clipboard doesn't hold journey steps. Copy steps first (select them, then Copy)." };
  if (c.version !== 1) return { error: "These steps were copied from a newer version of Pulse." };
  if (!c.nodes || typeof c.nodes !== "object" || Array.isArray(c.nodes)) return { error: "The copied steps are damaged." };
  const ids = Object.keys(c.nodes);
  if (ids.length === 0 || ids.length > MAX_CLIPBOARD_STEPS) return { error: `Paste 1 to ${MAX_CLIPBOARD_STEPS} steps at a time.` };
  // Only the copy's own step ids count: an entry of "constructor" must not find Object's.
  if (typeof c.entry !== "string" || !Object.prototype.hasOwnProperty.call(c.nodes, c.entry)) return { error: "The copied steps have no first step." };
  for (const [id, n] of Object.entries(c.nodes)) {
    if (isUnsafeKey(id) || id === TRIGGER_ID) return { error: "The copied steps are damaged." };
    if (!n || typeof n !== "object" || typeof n.type !== "string" || !typeInfo(n.type)) return { error: `Step "${id}" is of a kind this journey editor doesn't know.` };
    const links = ["next", "yes", "no", "target", "step"] as const;
    if (links.some((k) => n[k] != null && typeof n[k] !== "string")) return { error: "The copied steps are damaged." };
    if (n.variants != null && (!Array.isArray(n.variants) || n.variants.some((v) => !v || typeof v !== "object" || (v.next != null && typeof v.next !== "string")))) return { error: "The copied steps are damaged." };
    if ((n.x != null && !Number.isFinite(n.x)) || (n.y != null && !Number.isFinite(n.y))) return { error: "The copied steps are damaged." };
  }
  return { clip: c as StepClipboard };
}

/** pasteSteps inserts copied steps at an insertion point. Steps get new ids; links between them,
 * jumps between them and message decisions watching one of them follow. The first copied step goes
 * where the insertion point leads; what it led to before follows the copied steps' main path (first
 * outputs from the entry). The pasted block is laid out below the insertion point. */
export function pasteSteps(g: JourneyGraph, at: InsertAt, clip: StepClipboard): { graph: JourneyGraph; ids: string[]; entry: string } {
  const rename: Record<string, string> = Object.create(null); // keyed by copied ids
  let out: JourneyGraph = { ...g, nodes: { ...g.nodes } };
  for (const id of Object.keys(clip.nodes)) {
    const nid = newId(out, clip.nodes[id].type);
    rename[id] = nid;
    out = { ...out, nodes: { ...out.nodes, [nid]: { type: clip.nodes[id].type } } }; // reserve the id
  }
  const was = targetAt(g, at);
  const pos = positions(g);
  const anchor = was ? pos[was] : { x: (pos[at.source] ?? pos[TRIGGER_ID]).x, y: (pos[at.source] ?? pos[TRIGGER_ID]).y + ROW };
  const src = Object.values(clip.nodes);
  const minX = Math.min(...src.map((n) => n.x ?? 0)), minY = Math.min(...src.map((n) => n.y ?? 0));
  const e = clip.nodes[clip.entry];
  const dx = anchor.x - (e.x ?? minX), dy = anchor.y - minY;
  for (const [id, n] of Object.entries(clip.nodes)) {
    let copy = mapEdges(n, (t) => (t && rename[t] ? rename[t] : undefined));
    if (copy.target) copy = rename[copy.target] ? { ...copy, target: rename[copy.target] } : { ...copy, target: undefined };
    if (copy.step) copy = rename[copy.step] ? { ...copy, step: rename[copy.step] } : copy;
    copy.x = Math.round((n.x ?? anchor.x) + dx);
    copy.y = Math.round((n.y ?? anchor.y) + dy);
    out.nodes[rename[id]] = copy;
  }
  const entry = rename[clip.entry];
  // Make room: what came after the insertion point moves below the pasted block.
  const ids = Object.values(rename);
  if (was) {
    const height = Math.max(...ids.map((id) => out.nodes[id].y ?? 0)) - anchor.y + ROW;
    const below = new Set<string>();
    const stack = [was];
    while (stack.length) {
      const x = stack.pop()!;
      if (below.has(x) || !g.nodes[x]) continue;
      below.add(x);
      const n = g.nodes[x];
      for (const [h] of outputs(n)) { const t = targetOf(n, h); if (t) stack.push(t); }
    }
    for (const id of below) out.nodes[id] = { ...out.nodes[id], x: pos[id].x, y: pos[id].y + height };
    // Continue the old path from the end of the pasted block's main path.
    let tail = entry;
    const seen = new Set<string>();
    while (!seen.has(tail)) {
      seen.add(tail);
      const n = out.nodes[tail];
      const h = firstOutput(n);
      if (!h) break;
      const t = targetOf(n, h);
      if (!t || !ids.includes(t)) {
        if (!t) out = connect(out, tail, h, was);
        break;
      }
      tail = t;
    }
  }
  out = connect(out, at.source, at.handle, entry);
  return { graph: out, ids, entry };
}
