// Event schemas: pure helpers for Audience → Events (no React). The checks mirror the server's
// (internal/eventschema Normalize) so the editor can explain problems before saving.
import type { DiscoveredProp, EventSchema, PropType, SchemaMode, SchemaProp, Violation } from "./api";

export const PROP_TYPES: PropType[] = ["string", "number", "bool", "date", "enum", "list", "object"];
export const TYPE_NAMES: Record<PropType, string> = {
  string: "Text", number: "Number", bool: "Yes / no", date: "Date", enum: "One of a list", list: "List", object: "Object",
};
export const MAX_PROPS = 100;
export const MAX_ENUM_VALUES = 200;
const MAX_LABEL = 120;
const MAX_DESC = 500;

const KEY_RE = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/;

/** validKey: letters, digits and _, starting with a letter or _, at most 64 characters. */
export function validKey(k: string): boolean {
  return KEY_RE.test(k);
}

/** validPath: a name, a dotted path of up to 4 parts (product.code), or a path through one list (items[].code). */
export function validPath(p: string): boolean {
  if (p === "" || p.length > 128) return false;
  const parts = p.split(".");
  if (parts.length > 4) return false;
  let list = false;
  for (const part of parts) {
    const isList = part.endsWith("[]");
    const key = isList ? part.slice(0, -2) : part;
    if (!validKey(key) || (isList && list)) return false;
    list = list || isList;
  }
  return true;
}

/** hasList: the path goes through a list (items[].code). */
export const hasList = (p: string) => p.includes("[]");

/** pathOf: where the property is in the event's props (its key unless a path is given). */
export const pathOf = (p: Pick<SchemaProp, "key" | "path">) => (p.path && p.path.trim()) || p.key;

// Contact fields Pulse maintains itself; an event can't add values to them.
const RESERVED_LIST_FIELDS = new Set([
  "tags", "merged_into", "stage", "stage_changed_at", "date_added", "date_identified", "date_modified", "email_domain",
  "phone_country", "owner_id", "owner_name", "device_type", "device_os", "device_os_version", "device_brand", "device_model",
  "browser", "app_version",
]);

/** validListField: a contact field an event may add values to (not tags or a field Pulse maintains). */
export function validListField(k: string): boolean {
  return validKey(k) && !RESERVED_LIST_FIELDS.has(k) && !k.startsWith("company_");
}

/** validateSchema lists a schema's problems in plain English (empty when it can be saved). */
export function validateSchema(s: Pick<EventSchema, "label" | "description" | "mode" | "props">): string[] {
  const out: string[] = [];
  if (!["off", "warn", "enforce"].includes(s.mode)) out.push("Choose a mode: off, warn or enforce.");
  if ((s.label ?? "").trim().length > MAX_LABEL) out.push(`The label is too long (at most ${MAX_LABEL} characters).`);
  if ((s.description ?? "").length > MAX_DESC) out.push(`The description is too long (at most ${MAX_DESC} characters).`);
  if (s.props.length > MAX_PROPS) out.push(`An event can have at most ${MAX_PROPS} properties.`);
  const keys = new Map<string, number>();
  const paths = new Set<string>();
  s.props.forEach((p, i) => {
    const name = p.key ? `“${p.key}”` : `Property ${i + 1}`;
    if (!validKey(p.key)) {
      out.push(`Property ${i + 1}: the key needs letters, digits and _, starting with a letter (at most 64 characters).`);
    } else if (keys.has(p.key)) {
      out.push(`${name} is listed twice.`);
    } else {
      keys.set(p.key, i);
    }
    const path = pathOf(p);
    if (!validPath(path)) {
      out.push(`${name}: the path must be a name, a dotted path like product.code, or items[].code.`);
    } else if (paths.has(path)) {
      out.push(`${name}: another property already reads ${path}.`);
    } else {
      paths.add(path);
    }
    if (!PROP_TYPES.includes(p.type)) out.push(`${name}: choose a type.`);
    if ((p.label ?? "").trim().length > MAX_LABEL || (p.description ?? "").length > MAX_DESC) out.push(`${name}: the label or description is too long.`);
    const values = p.values ?? [];
    if (p.type === "enum") {
      if (values.length === 0 || values.length > MAX_ENUM_VALUES) {
        out.push(`${name}: add between 1 and ${MAX_ENUM_VALUES} allowed values.`);
      } else {
        const seen = new Set<string>();
        const bad = values.some((v) => {
          const val = v.value.trim();
          const dup = seen.has(val);
          seen.add(val);
          return val === "" || val.length > 200 || (v.label ?? "").trim().length > MAX_LABEL || dup;
        });
        if (bad) out.push(`${name}: allowed values must be filled in, different from each other and short.`);
      }
    } else if (values.length > 0) {
      out.push(`${name}: only “One of a list” properties have allowed values.`);
    }
    if (p.add_to_list) {
      if (!validListField(p.add_to_list)) out.push(`${name}: “${p.add_to_list}” can't be a list field.`);
      else if (p.type === "list" || p.type === "object") out.push(`${name}: only single values (text, numbers, enums) can be added to a list field.`);
    }
  });
  s.props.forEach((p) => {
    const d = p.depends_on;
    if (!d) return;
    const name = `“${p.key}”`;
    const j = keys.get(d.prop);
    if (j === undefined || d.prop === p.key) {
      out.push(`${name} applies only when another property has certain values: choose which property.`);
      return;
    }
    const other = s.props[j];
    if (other.depends_on) out.push(`${name} depends on “${d.prop}”, which depends on another property itself. Use one level.`);
    if (hasList(pathOf(other)) || hasList(pathOf(p))) out.push(`${name}: “Only when…” can't use items[] paths.`);
    if (d.values.length === 0 || d.values.length > MAX_ENUM_VALUES) out.push(`${name}: tick the values of “${d.prop}” it applies to.`);
  });
  return out;
}

/** keyFromPath suggests a property key for a path: product.code → product_code, items[].code → item_code. */
export function keyFromPath(path: string): string {
  const parts = path.split(".").map((part) => {
    if (!part.endsWith("[]")) return part;
    const name = part.slice(0, -2);
    return name.length > 1 && name.endsWith("s") && !name.endsWith("ss") ? name.slice(0, -1) : name;
  });
  let key = parts.join("_").replace(/[^a-zA-Z0-9_]/g, "_");
  if (!/^[a-zA-Z_]/.test(key)) key = "_" + key;
  return key.slice(0, 64);
}

/** guessType maps a discovered type to a schema type (mixed values are treated as text). */
export function guessType(t: string): PropType {
  return (PROP_TYPES as string[]).includes(t) ? (t as PropType) : "string";
}

/** newProp: a schema property for a path seen in events, prefilled from what was discovered. */
export function propFromDiscovered(d: DiscoveredProp): SchemaProp {
  const key = keyFromPath(d.path);
  return { key, path: d.path === key ? undefined : d.path, type: guessType(d.type) };
}

export type PropertyRow = {
  path: string;
  /** In the schema, seen in the sample, or both. */
  described: boolean;
  seen: boolean;
  prop?: SchemaProp;
  discovered?: DiscoveredProp;
};

/** mergeProperties lines up the schema's properties (in schema order) with the ones seen in sampled
 * events; properties seen but not described follow, most common first. */
export function mergeProperties(discovered: DiscoveredProp[], props: SchemaProp[]): PropertyRow[] {
  const byPath = new Map(discovered.map((d) => [d.path, d]));
  const rows: PropertyRow[] = props.map((p) => {
    const d = byPath.get(pathOf(p));
    return { path: pathOf(p), described: true, seen: !!d, prop: p, discovered: d };
  });
  const described = new Set(rows.map((r) => r.path));
  const rest = discovered.filter((d) => !described.has(d.path)).sort((a, b) => b.share - a.share || a.path.localeCompare(b.path));
  return [...rows, ...rest.map((d) => ({ path: d.path, described: false, seen: true, discovered: d }))];
}

/** modeLabel names a schema mode (no mode: the event isn't described). */
export function modeLabel(mode?: SchemaMode | null): string {
  switch (mode) {
    case "off": return "Off";
    case "warn": return "Warn";
    case "enforce": return "Enforce";
    default: return "Not described";
  }
}

/** MODE_HELP explains each mode in a sentence. */
export const MODE_HELP: Record<SchemaMode, string> = {
  off: "The schema documents the event only. Nothing is checked.",
  warn: "Events that don't fit are kept; problems are counted and the latest are shown here.",
  enforce: "The server API refuses events that don't fit, with a message saying why. Browser and app SDK events are never refused.",
};

/** problemWords says what's wrong for a violation code. */
export function problemWords(code: Violation["code"] | string): string {
  switch (code) {
    case "missing": return "missing";
    case "type": return "wrong type";
    case "enum": return "not an allowed value";
    case "not_applicable": return "sent when it doesn't apply";
    default: return code;
  }
}

/** isoDay formats a date as its UTC YYYY-MM-DD. */
export const isoDay = (d: Date) => d.toISOString().slice(0, 10);

/** fillDays returns one entry per day for the `days` days ending today (UTC), oldest first, with 0
 * for days without events. */
export function fillDays(volume: { day: string; count: number }[], days: number, today: Date): { day: string; count: number }[] {
  const counts = new Map(volume.map((v) => [v.day.slice(0, 10), v.count]));
  const base = Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate());
  const out: { day: string; count: number }[] = [];
  for (let i = days - 1; i >= 0; i--) {
    const day = isoDay(new Date(base - i * 86_400_000));
    out.push({ day, count: counts.get(day) ?? 0 });
  }
  return out;
}

/** exampleValue: a plausible value for a property in code examples. */
export function exampleValue(p: SchemaProp, today: Date): unknown {
  switch (p.type) {
    case "string": return "SKU-1";
    case "enum": return p.values?.[0]?.value ?? "";
    case "number": return 499;
    case "bool": return true;
    case "date": return isoDay(today);
    case "list": return ["A1"];
    case "object": return {};
  }
}

type Obj = Record<string, unknown>;

/** exampleProps builds example event props from a schema: nested paths become nested objects
 * (product.code → {product: {code}}), items[].code → {items: [{code}]}. A property that applies
 * only when another has certain values is included only when the other's example value is one of them. */
export function exampleProps(schema: Pick<EventSchema, "props">, today = new Date()): Obj {
  const props = schema.props.filter((p) => validPath(pathOf(p)));
  const byKey = new Map(props.map((p) => [p.key, p]));
  const out: Obj = {};
  const objAt = (root: Obj, keys: string[]): Obj => {
    let cur = root;
    for (const k of keys) {
      const next = cur[k];
      if (typeof next !== "object" || next === null || Array.isArray(next)) cur[k] = {};
      cur = cur[k] as Obj;
    }
    return cur;
  };
  // Lists of objects read by items[] paths: their element holds the per-item properties.
  const listElem = (listPath: string[]): Obj => {
    const parent = objAt(out, listPath.slice(0, -1));
    const last = listPath[listPath.length - 1];
    const cur = parent[last];
    if (!Array.isArray(cur) || typeof cur[0] !== "object" || cur[0] === null) parent[last] = [{}];
    return (parent[last] as Obj[])[0];
  };
  const applies = (p: SchemaProp) => {
    const d = p.depends_on;
    if (!d) return true;
    const other = byKey.get(d.prop);
    return !!other && d.values.includes(String(exampleValue(other, today)));
  };
  const itemLists = new Set(props.map(pathOf).filter(hasList).map((p) => p.slice(0, p.indexOf("[]"))));
  for (const p of props) {
    if (!applies(p)) continue;
    const path = pathOf(p);
    const value = exampleValue(p, today);
    if (hasList(path)) {
      const [listPart, inner] = path.split("[]");
      const elem = listElem(listPart.split("."));
      const keys = inner.replace(/^\./, "").split(".").filter(Boolean);
      if (keys.length === 0) continue;
      const target = objAt(elem, keys.slice(0, -1));
      const k = keys[keys.length - 1];
      if (!(k in target) || p.type !== "object") target[k] = value;
      continue;
    }
    // A list read through items[] paths is built from those; an object may already have children.
    if (p.type === "list" && itemLists.has(path)) {
      listElem(path.split("."));
      continue;
    }
    const keys = path.split(".");
    const target = objAt(out, keys.slice(0, -1));
    const k = keys[keys.length - 1];
    if (p.type === "object" && typeof target[k] === "object" && target[k] !== null) continue;
    target[k] = value;
  }
  return out;
}

/** dependsSentence reads a dependency aloud: “Only when method is Social or OTP”. */
export function dependsSentence(p: SchemaProp, props: SchemaProp[]): string {
  const d = p.depends_on;
  if (!d) return "Always applies";
  const other = props.find((x) => x.key === d.prop);
  const label = (v: string) => other?.values?.find((x) => x.value === v)?.label || v;
  const vals = d.values.map(label);
  const list = vals.length <= 1 ? (vals[0] ?? "…") : `${vals.slice(0, -1).join(", ")} or ${vals[vals.length - 1]}`;
  return `Only when ${other?.label || d.prop || "…"} is ${list}`;
}

/** moveItem returns a copy of list with item i moved by d (−1 up, +1 down); unchanged at the ends. */
export function moveProp<T>(list: T[], i: number, d: -1 | 1): T[] {
  const j = i + d;
  if (j < 0 || j >= list.length) return list;
  const out = list.slice();
  [out[i], out[j]] = [out[j], out[i]];
  return out;
}

/** cleanProps tidies the editor's properties for saving: trims text, drops empty optional fields. */
export function cleanProps(props: SchemaProp[]): SchemaProp[] {
  return props.map((p) => {
    const key = p.key.trim();
    const path = (p.path ?? "").trim();
    const out: SchemaProp = { key, type: p.type };
    if (path && path !== key) out.path = path;
    if (p.label?.trim()) out.label = p.label.trim();
    if (p.description?.trim()) out.description = p.description.trim();
    if (p.required) out.required = true;
    if (p.personal) out.personal = true;
    if (p.add_to_list?.trim()) out.add_to_list = p.add_to_list.trim();
    if (p.type === "enum") out.values = (p.values ?? []).map((v) => ({ value: v.value.trim(), ...(v.label?.trim() ? { label: v.label.trim() } : {}) }));
    if (p.depends_on && p.depends_on.prop) out.depends_on = { prop: p.depends_on.prop, values: p.depends_on.values };
    return out;
  });
}

/** formatShare shows a 0..1 share as a percentage (“<1%” for small non-zero ones). */
export function formatShare(share: number): string {
  if (share > 0 && share < 0.01) return "<1%";
  return `${Math.round(share * 100)}%`;
}
