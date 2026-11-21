// Path analysis (O6): lays out the server's per-step links as a flow chart — a column per step,
// a box per event (the 7 biggest, the rest as "Other"), bands as wide as the people who moved.
// Pure for vitest.

export interface PathLink { step: number; from: string; to: string; people: number }
export interface PathRow { steps: string[]; people: number }
export interface PathsAnswer { start: string; end: string; steps: number; days: number; people: number; paths: PathRow[]; links: PathLink[] }

export const STOPPED = "(stopped)";
export const OTHER = "Other events";
const MAX_NODES = 7;

export interface FlowNode { key: string; col: number; name: string; value: number; x: number; y: number; h: number }
export interface FlowBand { from: string; to: string; value: number; d: string; width: number }

/** layout positions nodes and bands in a w × h box. reverse puts step 0 on the right (paths that end at an event). */
export function layout(links: PathLink[], steps: number, anchor: string, w = 1000, h = 420, reverse = false): { nodes: FlowNode[]; bands: FlowBand[] } {
  // Column values: col 0 holds the anchor; col k+1 holds what step k led to.
  steps = Number.isInteger(steps) && steps > 0 ? Math.min(steps, 100) : 0;
  // Links outside the columns (a step below 0 or past steps, or no count) are left out rather than
  // crashing on a missing column or drawing NaN-wide bands.
  links = (links ?? []).filter((l) => Number.isInteger(l.step) && l.step >= 0 && l.step <= steps && Number.isFinite(l.people));
  const cols: Map<string, number>[] = Array.from({ length: steps + 1 }, () => new Map());
  for (const l of links) {
    const name = l.to || STOPPED;
    if (l.step === 0) cols[0].set(anchor, (cols[0].get(anchor) ?? 0) + l.people);
    if (l.step + 1 <= steps) cols[l.step + 1].set(name, (cols[l.step + 1].get(name) ?? 0) + l.people);
  }
  // Keep the biggest per column; the rest become Other (stops always shown).
  const keep: Set<string>[] = cols.map((m) => {
    const sorted = [...m.entries()].filter(([n]) => n !== STOPPED).sort((a, b) => b[1] - a[1]);
    return new Set(sorted.slice(0, MAX_NODES).map(([n]) => n).concat(STOPPED));
  });
  const nameAt = (col: number, n: string) => (keep[col].has(n) ? n : OTHER);
  const values: Map<string, number>[] = cols.map((m, c) => {
    const out = new Map<string, number>();
    for (const [n, v] of m) out.set(nameAt(c, n), (out.get(nameAt(c, n)) ?? 0) + v);
    return out;
  });
  const pad = 10, nodeW = 12;
  const maxTotal = Math.max(1, ...values.map((m) => [...m.values()].reduce((a, b) => a + b, 0)));
  const maxNodes = Math.max(1, ...values.map((m) => m.size));
  const scale = (h - pad * (maxNodes - 1)) / maxTotal;
  const colX = (c: number) => {
    const x = steps === 0 ? 0 : (c * (w - nodeW)) / steps;
    return reverse ? w - nodeW - x : x;
  };
  const nodes: FlowNode[] = [];
  const byKey = new Map<string, FlowNode>();
  values.forEach((m, c) => {
    let y = 0;
    const ordered = [...m.entries()].sort((a, b) => (a[0] === STOPPED ? 1 : b[0] === STOPPED ? -1 : a[0] === OTHER ? 1 : b[0] === OTHER ? -1 : b[1] - a[1]));
    for (const [name, value] of ordered) {
      const n: FlowNode = { key: `${c}:${name}`, col: c, name, value, x: colX(c), y, h: Math.max(1, value * scale) };
      y += n.h + pad;
      nodes.push(n);
      byKey.set(n.key, n);
    }
  });
  // Bands, stacked within each node's out side and in side.
  const outY = new Map<string, number>(), inY = new Map<string, number>();
  const merged = new Map<string, { from: FlowNode; to: FlowNode; value: number }>();
  for (const l of links) {
    if (l.step + 1 > steps) continue;
    const from = byKey.get(`${l.step}:${l.step === 0 ? anchor : nameAt(l.step, l.from)}`);
    const to = byKey.get(`${l.step + 1}:${nameAt(l.step + 1, l.to || STOPPED)}`);
    if (!from || !to) continue;
    const k = `${from.key}>${to.key}`;
    const m = merged.get(k) ?? { from, to, value: 0 };
    m.value += l.people;
    merged.set(k, m);
  }
  const bands: FlowBand[] = [];
  for (const { from, to, value } of [...merged.values()].sort((a, b) => b.value - a.value)) {
    const width = Math.max(1, value * scale);
    const y0 = from.y + (outY.get(from.key) ?? 0) + width / 2, y1 = to.y + (inY.get(to.key) ?? 0) + width / 2;
    outY.set(from.key, (outY.get(from.key) ?? 0) + width);
    inY.set(to.key, (inY.get(to.key) ?? 0) + width);
    const x0 = reverse ? from.x : from.x + nodeW, x1 = reverse ? to.x + nodeW : to.x, mid = (x0 + x1) / 2;
    bands.push({ from: from.name, to: to.name, value, width, d: `M${x0},${y0} C${mid},${y0} ${mid},${y1} ${x1},${y1}` });
  }
  return { nodes, bands };
}
