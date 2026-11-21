// Copy and paste of segment rule groups (M3): a group (or the whole rule) goes to the clipboard as
// JSON in the rule format the API takes, and pastes into any group of any segment, checked against
// the builder's limits first.

import type { Rule } from "./api";
import { countNodes, depthOf, fromRule, MAX_RULE_DEPTH, MAX_RULE_NODES, type Group, type Node, type Path, toRule } from "./segmentRules";

export const SEGMENT_CLIPBOARD_KIND = "segment-rules";

export interface RuleClipboard { pulse: typeof SEGMENT_CLIPBOARD_KIND; version: 1; rule: Rule }

/** copyText is a group as clipboard text. */
export function copyText(n: Node): string {
  return JSON.stringify({ pulse: SEGMENT_CLIPBOARD_KIND, version: 1, rule: toRule(n) } satisfies RuleClipboard);
}

const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);

const has = (r: Record<string, unknown>, k: string) => Object.prototype.hasOwnProperty.call(r, k);
const optional = (r: Record<string, unknown>, k: string, type: "string" | "number") => !has(r, k) || r[k] == null || typeof r[k] === type;

/** A comparison value the builder can edit: text, a number, yes/no, or a list of those. */
function validValue(v: unknown): boolean {
  const plain = (x: unknown) => x == null || typeof x === "string" || typeof x === "boolean" || (typeof x === "number" && Number.isFinite(x));
  return plain(v) || (Array.isArray(v) && v.length <= 1000 && v.every((x) => x != null && plain(x)));
}

/** validRule checks the shape of a rule from the clipboard (the server checks it fully on save):
 * clipboard text is anything someone pasted, and the builder reads every field it lets through. */
function validRule(r: unknown, depth = 0): boolean {
  if (!isObj(r) || depth > MAX_RULE_DEPTH) return false;
  for (const k of ["all", "any", "none"] as const) {
    if (has(r, k)) return Array.isArray(r[k]) && (r[k] as unknown[]).every((c) => validRule(c, depth + 1));
  }
  if (has(r, "segment")) return typeof r.segment === "number" && Number.isFinite(r.segment) && (r.op === "in" || r.op === "not_in");
  if (has(r, "tag")) return typeof r.tag === "string" && (r.op === "has" || r.op === "not_has");
  if (has(r, "event")) {
    if (typeof r.event !== "string" || typeof r.op !== "string" || typeof r.count !== "number" || !Number.isFinite(r.count)) return false;
    if (!optional(r, "days", "number") || !optional(r, "from", "string") || !optional(r, "to", "string")) return false;
    if (has(r, "where") && r.where != null) {
      if (!Array.isArray(r.where)) return false;
      if (!r.where.every((f) => isObj(f) && typeof f.prop === "string" && typeof f.op === "string" && validValue(f.value))) return false;
    }
    return true;
  }
  return typeof r.attr === "string" && typeof r.op === "string" && validValue(r.value);
}

/** parseText reads clipboard text for pasting into the group at path of root, or says why it can't. */
export function parseText(text: string, root: Group, path: Path): { node?: Node; error?: string } {
  let v: unknown;
  try { v = JSON.parse(text); } catch { return { error: "The clipboard doesn't hold segment filters. Copy a group first." }; }
  if (!isObj(v) || v.pulse !== SEGMENT_CLIPBOARD_KIND) return { error: "The clipboard doesn't hold segment filters. Copy a group first." };
  if (v.version !== 1) return { error: "These filters were copied from a newer version of Pulse." };
  if (!validRule(v.rule)) return { error: "The copied filters are damaged." };
  const node = fromRule(v.rule as Rule);
  if (node.kind === "group" && node.children.length === 0) return { error: "The copied group is empty." };
  if (countNodes(root) + countNodes(node) > MAX_RULE_NODES) return { error: `A segment can hold at most ${MAX_RULE_NODES} filters and groups.` };
  if (path.length + 1 + depthOf(node) > MAX_RULE_DEPTH) return { error: "Pasting it here would nest groups too deep. Paste it into a group nearer the top." };
  return { node };
}
