// Standard variables for message templates — the web side of internal/personalize:
//
//   {{contact.first_name | default:"there" | title}}   {{event.items.0.name}}   {{campaign.name}}   {{links.unsubscribe}}
//
// This module lists what can be inserted (the picker's catalog), builds tags, finds the tags in a
// field and checks them (unknown variables are warnings, unknown pipes errors: the server refuses
// those), and fills tags with sample values for the live preview. The older {{.Attrs.x}} syntax
// keeps working and is filled too.
import type { AttributeDef, EventSchema } from "./api";

export type VarGroup = "contact" | "event" | "campaign" | "links";
export type VarItem = { var: string; label: string; group: VarGroup; hint?: string };

export type PipeArg = "none" | "optional" | "required";
export type PipeDef = { name: string; arg: PipeArg; label: string; example?: string };

/** The pipes, as the server has them (internal/personalize/syntax.go). */
export const PIPES: PipeDef[] = [
  { name: "default", arg: "required", label: "Default value", example: 'default:"there"' },
  { name: "upper", arg: "none", label: "UPPER CASE" },
  { name: "lower", arg: "none", label: "lower case" },
  { name: "title", arg: "none", label: "Title Case" },
  { name: "date", arg: "optional", label: "Date", example: 'date:"2 Jan 2006"' },
  { name: "money", arg: "optional", label: "Money", example: 'money:"INR"' },
  { name: "number", arg: "optional", label: "Number with separators", example: "number:2" },
  { name: "truncate", arg: "required", label: "Shorten", example: "truncate:40" },
  { name: "urlencode", arg: "none", label: "For a link's query string" },
  { name: "json", arg: "none", label: "JSON value (webhook bodies)" },
];
const PIPE_BY_NAME = new Map(PIPES.map((p) => [p.name, p]));

export const CAMPAIGN_VARS: VarItem[] = [
  { var: "campaign.name", label: "Campaign name", group: "campaign" },
  { var: "campaign.id", label: "Campaign ID", group: "campaign" },
  { var: "campaign.type", label: "Type: journey (triggered or automation), broadcast (one-time) or test", group: "campaign" },
  { var: "campaign.step", label: "Journey step ID", group: "campaign" },
  { var: "campaign.channel", label: "Channel", group: "campaign" },
  { var: "campaign.template", label: "Template name", group: "campaign" },
];

export const LINK_VARS: VarItem[] = [
  { var: "links.unsubscribe", label: "Unsubscribe link", group: "links", hint: "email" },
  { var: "links.preferences", label: "Preference center link", group: "links", hint: "email" },
];

/** Contact fields every workspace has, before its own fields. */
export const CONTACT_BUILTINS: VarItem[] = [
  { var: "contact.first_name", label: "First name", group: "contact" },
  { var: "contact.last_name", label: "Last name", group: "contact" },
  { var: "contact.name", label: "Name", group: "contact" },
  { var: "contact.email", label: "Email address", group: "contact" },
  { var: "contact.phone", label: "Phone", group: "contact" },
  { var: "contact.phone_country", label: "Phone country (IN, US…)", group: "contact" },
  { var: "contact.city", label: "City", group: "contact" },
  { var: "contact.external_id", label: "External ID", group: "contact" },
];

/** contactVars: the built-in fields, then the workspace's contact fields not already listed. */
export function contactVars(attrs: Pick<AttributeDef, "key" | "label" | "type">[] = []): VarItem[] {
  const seen = new Set(CONTACT_BUILTINS.map((v) => v.var));
  const extra: VarItem[] = [];
  for (const a of attrs) {
    if (!/^[A-Za-z0-9_]+$/.test(a.key)) continue;
    const v = `contact.${a.key}`;
    if (seen.has(v)) continue;
    seen.add(v);
    extra.push({ var: v, label: a.label || a.key, group: "contact", hint: a.type });
  }
  return [...CONTACT_BUILTINS, ...extra];
}

/**
 * eventVars: the trigger event's properties from its schema. A property inside a list (items[].name)
 * is offered as the first item's (event.items.0.name) and as every item's, joined (event.items[].name).
 * Personal properties are left out: they are never copied into messages.
 */
export function eventVars(schema?: Pick<EventSchema, "props"> | null): VarItem[] {
  const out: VarItem[] = [];
  for (const p of schema?.props ?? []) {
    if (p.personal) continue;
    const path = p.path || p.key;
    const label = p.label || p.key;
    const i = path.indexOf("[]");
    if (i >= 0) {
      const list = path.slice(0, i), rest = path.slice(i + 2).replace(/^\./, "");
      out.push({ var: `event.${list}.0${rest ? "." + rest : ""}`, label: `${label} (first item)`, group: "event", hint: p.type });
      out.push({ var: `event.${path}`, label: `${label} (every item, joined)`, group: "event", hint: p.type });
    } else {
      out.push({ var: `event.${path}`, label, group: "event", hint: p.type });
    }
  }
  return out;
}

/** A tag with its pipes, e.g. {{contact.first_name | default:"there" | title}}. */
export type TagPipe = { name: string; arg?: string };
export function buildTag(v: string, pipes: TagPipe[] = []): string {
  const parts = pipes.filter((p) => PIPE_BY_NAME.has(p.name)).map((p) => {
    const def = PIPE_BY_NAME.get(p.name)!;
    if (def.arg === "none" || p.arg === undefined || p.arg === "") return p.name;
    return /^-?\d+$/.test(p.arg) ? `${p.name}:${p.arg}` : `${p.name}:${JSON.stringify(p.arg)}`;
  });
  return `{{${[v, ...parts].join(" | ")}}}`;
}

/** Ref is one standard-variable tag found in a field. */
export type Ref = { var: string; pipes: TagPipe[]; start: number; end: number; text: string; error?: string };

const NS = /^(contact|event|campaign|links)\./;
const SEG = /^[A-Za-z0-9_]+(\[\])?$/;

/** isStandard: the action text (between the braces) uses the standard syntax. */
export const isStandard = (action: string) => NS.test(action.trim());

function splitPipes(s: string): string[] | null {
  const out: string[] = [];
  let quote = "", last = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      if (c === "\\") i++;
      else if (c === quote) quote = "";
    } else if (c === '"' || c === "'") quote = c;
    else if (c === "|") { out.push(s.slice(last, i)); last = i + 1; }
  }
  if (quote) return null;
  out.push(s.slice(last));
  return out;
}

function parseArg(a: string): string | null {
  if (!a) return null;
  if (a[0] === '"') { try { return JSON.parse(a) as string; } catch { return null; } }
  if (a[0] === "'") return a.length >= 2 && a.endsWith("'") ? a.slice(1, -1) : null;
  return /[\s"']/.test(a) ? null : a;
}

/** parseAction parses "ns.path | pipe:arg"; error says what is wrong (as the server would). */
export function parseAction(action: string): { var: string; pipes: TagPipe[]; error?: string } {
  const parts = splitPipes(action.trim());
  if (!parts) return { var: action.trim(), pipes: [], error: "A quote is not closed." };
  const v = parts[0].trim();
  const pipes: TagPipe[] = [];
  const [ns, ...segs] = v.split(".");
  if (!segs.length || segs.some((s) => !SEG.test(s))) return { var: v, pipes, error: `“${v}” isn't a variable name (letters, digits and _ after the dot).` };
  if (segs.filter((s) => s.endsWith("[]")).length > 1) return { var: v, pipes, error: "A path goes through one list ([]) at most." };
  if (ns === "campaign" && !CAMPAIGN_VARS.some((c) => c.var === v)) return { var: v, pipes, error: `campaign has ${CAMPAIGN_VARS.map((c) => c.var.slice(9)).join(", ")}.` };
  if (ns === "links" && !LINK_VARS.some((c) => c.var === v)) return { var: v, pipes, error: `links has ${LINK_VARS.map((c) => c.var.slice(6)).join(", ")}.` };
  for (const raw of parts.slice(1)) {
    const p = raw.trim();
    const i = p.indexOf(":");
    const name = (i < 0 ? p : p.slice(0, i)).trim();
    const def = PIPE_BY_NAME.get(name);
    if (!def) return { var: v, pipes, error: `Unknown format “${name}”. Use ${PIPES.map((x) => x.name).join(", ")}.` };
    if (i >= 0) {
      if (def.arg === "none") return { var: v, pipes, error: `${name} takes no value.` };
      const arg = parseArg(p.slice(i + 1).trim());
      if (arg === null) return { var: v, pipes, error: `${name}: put the value in quotes, e.g. ${def.example}.` };
      if (name === "truncate" && !(Number.isInteger(Number(arg)) && Number(arg) >= 1)) return { var: v, pipes, error: "truncate needs a number of characters, e.g. truncate:40." };
      if (name === "number" && !(Number.isInteger(Number(arg)) && Number(arg) >= 0 && Number(arg) <= 6)) return { var: v, pipes, error: "number takes 0 to 6 decimals." };
      pipes.push({ name, arg });
    } else {
      if (def.arg === "required") return { var: v, pipes, error: `${name} needs a value, e.g. ${def.example}.` };
      pipes.push({ name });
    }
  }
  return { var: v, pipes };
}

/** findRefs lists the standard-variable tags of a field, in order. */
export function findRefs(src: string): Ref[] {
  const out: Ref[] = [];
  const re = /\{\{-?\s*([\s\S]*?)\s*-?\}\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(src))) {
    const inner = decodeEntities(m[1]);
    if (!isStandard(inner)) continue;
    const p = parseAction(inner);
    out.push({ ...p, start: m.index, end: m.index + m[0].length, text: m[0] });
  }
  return out;
}

function decodeEntities(s: string): string {
  return s.includes("&") ? s.replace(/&quot;/g, '"').replace(/&#39;|&apos;/g, "'").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&") : s;
}

export type Problem = { level: "error" | "warning"; var: string; message: string };

/**
 * lint checks a field's tags against what the workspace knows: unknown contact fields and (when the
 * trigger event's schema is known) unknown event properties are warnings — the value may still
 * arrive — and syntax mistakes are errors, which the server refuses on save.
 */
export function lint(src: string, known: { contact: VarItem[]; event?: VarItem[] | null; channel?: string }): Problem[] {
  const out: Problem[] = [];
  const contactKeys = new Set(known.contact.map((v) => v.var.split(".")[1]));
  const eventRoots = known.event ? new Set(known.event.map((v) => v.var.split(".")[1].replace("[]", ""))) : null;
  for (const r of findRefs(src)) {
    if (r.error) { out.push({ level: "error", var: r.var, message: r.error }); continue; }
    const [ns, first] = r.var.split(".");
    if (ns === "contact" && !contactKeys.has(first.replace("[]", ""))) {
      out.push({ level: "warning", var: r.var, message: `No contact field “${first}”. Contacts without it get ${defaultOf(r)}.` });
    }
    if (ns === "event" && eventRoots && !eventRoots.has(first.replace("[]", ""))) {
      out.push({ level: "warning", var: r.var, message: `The trigger event's schema has no property “${first}”.` });
    }
    if (ns === "links" && known.channel && known.channel !== "email") {
      out.push({ level: "warning", var: r.var, message: `${r.var} is only filled in emails.` });
    }
  }
  return out;
}

function defaultOf(r: Ref): string {
  const d = r.pipes.find((p) => p.name === "default");
  return d ? `“${d.arg}”` : "nothing (or skip, if the template says so)";
}

/** lintAll runs lint over several fields and keeps one problem per variable and message. */
export function lintAll(fields: (string | undefined)[], known: Parameters<typeof lint>[1]): Problem[] {
  const seen = new Set<string>();
  const out: Problem[] = [];
  for (const f of fields) for (const p of lint(f ?? "", known)) {
    const k = p.level + p.var + p.message;
    if (!seen.has(k)) { seen.add(k); out.push(p); }
  }
  return out;
}

// ---------- sample values for the live preview ----------

export type Sample = { contact: Record<string, unknown>; event?: Record<string, unknown> | null; campaign?: Record<string, unknown>; links?: Record<string, string> };

export const SAMPLE_CONTACT: Record<string, unknown> = {
  first_name: "Asha", last_name: "Rao", name: "Asha Rao", email: "asha@example.com", city: "Pune", phone: "+919800000001", phone_country: "IN", external_id: "sample",
};

/** lookup follows a dotted path: items.0.name reads one item, items[].name every item (joined). */
export function lookup(obj: unknown, path: string): unknown {
  const segs = path.split(".");
  let cur: unknown = obj;
  for (let i = 0; i < segs.length; i++) {
    const s = segs[i];
    const list = s.endsWith("[]");
    const key = list ? s.slice(0, -2) : s;
    if (Array.isArray(cur)) { const n = Number(key); cur = Number.isInteger(n) ? cur[n] : undefined; }
    else if (cur && typeof cur === "object") cur = (cur as Record<string, unknown>)[key];
    else return undefined;
    if (list) {
      const rest = segs.slice(i + 1).join(".");
      const items = Array.isArray(cur) ? cur : [];
      return items.map((it) => (rest ? lookup(it, rest) : it)).filter((x) => !isEmpty(x));
    }
  }
  return cur;
}

export const isEmpty = (v: unknown) => v === undefined || v === null || (typeof v === "string" && !v.trim()) || (Array.isArray(v) && !v.length);

function show(v: unknown): string {
  if (isEmpty(v)) return "";
  if (Array.isArray(v)) return v.map((x) => (x && typeof x === "object" ? JSON.stringify(x) : String(x))).join(", ");
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

const SYMBOLS: Record<string, string> = { INR: "₹", USD: "$", EUR: "€", GBP: "£", JPY: "¥" };

function applyPipe(v: unknown, p: TagPipe): unknown {
  const s = show(v);
  switch (p.name) {
    case "default": return isEmpty(v) ? p.arg ?? "" : v;
    case "upper": return s.toUpperCase();
    case "lower": return s.toLowerCase();
    case "title": return s.toLowerCase().replace(/(^|[\s-])(\S)/g, (_, a: string, b: string) => a + b.toUpperCase());
    case "truncate": { const n = Number(p.arg); return [...s].length > n ? [...s].slice(0, n - 1).join("").trimEnd() + "…" : s; }
    case "urlencode": return encodeURIComponent(s).replace(/%20/g, "+");
    case "json": return JSON.stringify(v ?? null);
    case "number": case "money": {
      const n = Number(String(s).replace(/,/g, ""));
      if (!s || Number.isNaN(n)) return s;
      if (p.name === "number") {
        const dec = p.arg ? Number(p.arg) : Number.isInteger(n) ? 0 : 2;
        return n.toLocaleString("en-US", { minimumFractionDigits: dec, maximumFractionDigits: dec });
      }
      const cur = (p.arg || "INR").toUpperCase();
      const dec = cur === "JPY" ? 0 : 2;
      const txt = Math.abs(n).toLocaleString(cur === "INR" ? "en-IN" : "en-US", { minimumFractionDigits: dec, maximumFractionDigits: dec });
      return (n < 0 ? "-" : "") + (SYMBOLS[cur] ?? cur + " ") + txt;
    }
    case "date": {
      if (!s) return "";
      const d = new Date(/^\d+(\.\d+)?$/.test(s) ? Number(s) * (Number(s) > 1e11 ? 1 : 1000) : s);
      if (Number.isNaN(d.getTime())) return s;
      return d.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" }); // close to the default layout
    }
  }
  return v;
}

/** valueOf reads a standard variable from a sample. */
export function valueOf(v: string, sample: Sample): unknown {
  const [ns, ...rest] = v.split(".");
  const path = rest.join(".");
  if (ns === "contact") return lookup(sample.contact, path);
  if (ns === "event") return sample.event ? lookup(sample.event, path) : undefined;
  if (ns === "campaign") return sample.campaign?.[path];
  if (ns === "links") return sample.links?.[path];
  return undefined;
}

/**
 * fillSample fills the tags of a field with sample values, for the preview while typing: standard
 * variables with their pipes, and the older {{.Attrs.x}} and {{.Contact.Email}}. Other Go actions
 * are left out. (The server renders the real thing: Preview as contact.)
 */
export function fillSample(src = "", sample: Sample = { contact: SAMPLE_CONTACT }): string {
  return src.replace(/\{\{-?\s*([\s\S]*?)\s*-?\}\}/g, (whole, raw: string) => {
    const inner = decodeEntities(raw);
    if (isStandard(inner)) {
      const p = parseAction(inner);
      if (p.error) return whole;
      return show(p.pipes.reduce<unknown>((v, pipe) => applyPipe(v, pipe), valueOf(p.var, sample)));
    }
    const attr = /^\.Attrs\.(\w+)$/.exec(inner.trim());
    if (attr) return show(sample.contact[attr[1]]) || `[${attr[1]}]`;
    if (inner.trim() === ".Contact.Email") return show(sample.contact.email);
    if (inner.trim() === ".Contact.ExternalID") return show(sample.contact.external_id);
    if (inner.trim() === ".UnsubscribeURL") return sample.links?.unsubscribe ?? "#unsubscribe";
    if (inner.trim() === ".PreferencesURL") return sample.links?.preferences ?? "#preferences";
    return "";
  });
}

/** exampleEvent builds sample properties from an event schema (as the server's schema example). */
export function exampleEvent(schema?: Pick<EventSchema, "props"> | null): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const p of schema?.props ?? []) {
    if (p.personal) continue;
    const v = p.type === "number" ? 1299 : p.type === "bool" ? true : p.type === "date" ? "2026-11-11T10:30:00Z"
      : p.type === "enum" ? p.values?.[0]?.value ?? "value" : p.type === "list" ? ["item 1", "item 2"] : p.type === "object" ? {} : `Sample ${p.label || p.key}`;
    setPath(out, p.path || p.key, v);
  }
  return out;
}

function setPath(obj: Record<string, unknown>, path: string, v: unknown) {
  const [before, after] = path.split("[]");
  const keys = before.split(".");
  let cur = obj;
  keys.forEach((k, i) => {
    const last = i === keys.length - 1;
    if (last && after === undefined) { if (!(k in cur)) cur[k] = v; return; }
    if (last) {
      const items = (Array.isArray(cur[k]) ? cur[k] : [{}]) as Record<string, unknown>[];
      setPath(items[0], after.replace(/^\./, ""), v);
      cur[k] = items;
      return;
    }
    if (!cur[k] || typeof cur[k] !== "object") cur[k] = {};
    cur = cur[k] as Record<string, unknown>;
  });
}

/** insertAt puts text into a field value at the cursor (or the end), returning the new value and cursor. */
export function insertAt(value: string, text: string, start?: number | null, end?: number | null): { value: string; cursor: number } {
  const s = start ?? value.length, e = end ?? s;
  return { value: value.slice(0, s) + text + value.slice(e), cursor: s + text.length };
}
