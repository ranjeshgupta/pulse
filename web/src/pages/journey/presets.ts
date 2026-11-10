// Condition presets: the friendly editors for Contact tags / stages / points / owner / device build
// ordinary segment rules (the rule is the truth; node.preset only picks the editor). parse* read a
// rule back into its preset, or return null when it was shaped some other way.
import type { Rule } from "../../api";

export type TagsPreset = { match: "any" | "all"; tags: string[] };
export type PointsPreset = { op: "gt" | "gte" | "lt" | "lte" | "eq" | "ne"; value: number };
export type OwnerPreset = { mode: "in" | "exists" | "not_exists"; ids: string[] };
export type DevicePreset = { types: string[]; os: string[] };

export function tagsRule(p: TagsPreset): Rule | undefined {
  const tags = p.tags.map((t) => t.trim()).filter(Boolean);
  if (tags.length === 0) return undefined;
  const rules: Rule[] = tags.map((tag) => ({ tag, op: "has" }));
  return p.match === "all" ? { all: rules } : { any: rules };
}
export function parseTags(r?: Rule): TagsPreset | null {
  if (!r) return { match: "any", tags: [] };
  const list = "any" in r ? { match: "any" as const, rs: r.any } : "all" in r ? { match: "all" as const, rs: r.all } : null;
  if (!list || !list.rs.every((x) => "tag" in x && x.op === "has")) return null;
  return { match: list.match, tags: list.rs.map((x) => (x as { tag: string }).tag) };
}

export function stageRule(stages: string[]): Rule | undefined {
  return stages.length ? { attr: "stage", op: "in", value: stages } : undefined;
}
export function parseStage(r?: Rule): string[] | null {
  if (!r) return [];
  if ("attr" in r && r.attr === "stage" && r.op === "in" && Array.isArray(r.value)) return r.value.map(String);
  if ("attr" in r && r.attr === "stage" && r.op === "eq" && typeof r.value === "string") return [r.value];
  return null;
}

export const POINT_OPS: { op: PointsPreset["op"]; label: string }[] = [
  { op: "gte", label: "at least" }, { op: "gt", label: "more than" }, { op: "lte", label: "at most" },
  { op: "lt", label: "less than" }, { op: "eq", label: "exactly" }, { op: "ne", label: "not" },
];
export function pointsRule(p: PointsPreset): Rule | undefined {
  return Number.isFinite(p.value) ? { attr: "points", op: p.op, value: p.value } : undefined;
}
export function parsePoints(r?: Rule): PointsPreset | null {
  if (!r) return { op: "gte", value: 50 };
  if ("attr" in r && r.attr === "points" && POINT_OPS.some((o) => o.op === r.op) && typeof r.value === "number") return { op: r.op as PointsPreset["op"], value: r.value };
  return null;
}

export function ownerRule(p: OwnerPreset): Rule | undefined {
  if (p.mode !== "in") return { attr: "owner_id", op: p.mode };
  return p.ids.length ? { attr: "owner_id", op: "in", value: p.ids } : undefined;
}
export function parseOwner(r?: Rule): OwnerPreset | null {
  if (!r) return { mode: "in", ids: [] };
  if (!("attr" in r) || r.attr !== "owner_id") return null;
  if (r.op === "exists" || r.op === "not_exists") return { mode: r.op, ids: [] };
  if (r.op === "in" && Array.isArray(r.value)) return { mode: "in", ids: r.value.map(String) };
  if (r.op === "eq" && r.value !== undefined && r.value !== "") return { mode: "in", ids: [String(r.value)] };
  return null;
}

export const OS_CHOICES = ["iOS", "Android", "Windows", "macOS", "Linux"];
export function deviceRule(p: DevicePreset): Rule | undefined {
  const parts: Rule[] = [];
  if (p.types.length) parts.push({ attr: "device_type", op: "in", value: p.types });
  if (p.os.length) parts.push({ attr: "device_os", op: "in", value: p.os });
  return parts.length === 0 ? undefined : parts.length === 1 ? parts[0] : { all: parts };
}
export function parseDevice(r?: Rule): DevicePreset | null {
  if (!r) return { types: [], os: [] };
  const parts = "all" in r ? r.all : [r];
  const out: DevicePreset = { types: [], os: [] };
  for (const x of parts) {
    if (!("attr" in x) || x.op !== "in" || !Array.isArray(x.value)) return null;
    if (x.attr === "device_type") out.types = x.value.map(String);
    else if (x.attr === "device_os") out.os = x.value.map(String);
    else return null;
  }
  return out;
}
