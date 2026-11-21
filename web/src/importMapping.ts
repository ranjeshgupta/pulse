// Column mapping for contact imports. The file itself is never rewritten: the import uploads a new
// header row followed by the untouched rest of the file (a Blob slice, so even a 10 GB file isn't
// read into memory). A column mapped to "Don't import" gets an empty heading, which the importer
// skips.

import { dict } from "./safe";

/** Special targets; anything else is an attribute key. "" skips the column. */
export const CORE_TARGETS: { value: string; label: string }[] = [
  { value: "external_id", label: "External ID" },
  { value: "email", label: "Email" },
  { value: "phone", label: "Phone (international format)" },
  { value: "tags", label: "Tags to add (separated by ;)" },
];

const KEY_RE = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/;
export const validKey = (k: string) => KEY_RE.test(k);

/** normalise turns a heading into the attribute key the importer would use. */
export function normalise(h: string): string {
  return h.replace(/^﻿/, "").trim().toLowerCase().replace(/ /g, "_");
}

// No prototype: a column headed "constructor" is a field of that name, not Object's constructor.
const ALIASES: Record<string, string> = dict({
  id: "external_id", user_id: "external_id", customer_id: "external_id", contact_id: "external_id",
  e_mail: "email", email_address: "email", mail: "email",
  mobile: "phone", phone_number: "phone", mobile_number: "phone", mobile_phone: "phone",
});

/** guessTarget suggests a target for a heading: common names of the ID, email and phone columns,
 * else the heading as a field name (letters, digits and _ only), else "Don't import". */
export function guessTarget(heading: string): string {
  const k = normalise(String(heading ?? "")).replace(/-/g, "_").replace(/[^a-z0-9_]/g, "");
  if (ALIASES[k]) return ALIASES[k];
  return validKey(k) ? k : "";
}

/** headerEnd finds the byte after the header row's line break (outside quotes), or -1. */
export function headerEnd(bytes: Uint8Array): number {
  let quoted = false;
  for (let i = 0; i < bytes.length; i++) {
    if (bytes[i] === 0x22) quoted = !quoted;
    else if (bytes[i] === 0x0a && !quoted) return i + 1;
  }
  return -1;
}

/** parseRows reads up to max CSV records from text (a file's first bytes; a cut-off last record is dropped). */
export function parseRows(text: string, max: number): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < text.length && rows.length < max; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') { cell += '"'; i++; }
      else if (c === '"') quoted = false;
      else cell += c;
    } else if (c === '"') quoted = true;
    else if (c === ",") { row.push(cell); cell = ""; }
    else if (c === "\n") { row.push(cell.replace(/\r$/, "")); rows.push(row); row = []; cell = ""; }
    else cell += c;
  }
  return rows;
}

const csvCell = (v: string) => (/[",\r\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);

/** buildHeader is the replacement header row for the chosen targets. */
export function buildHeader(targets: string[]): string {
  return targets.map(csvCell).join(",") + "\n";
}

/** mappingProblems lists what stops an import: no ID column, duplicates, invalid names. */
export function mappingProblems(targets: string[]): string[] {
  const out: string[] = [];
  if (!targets.includes("external_id") && !targets.includes("email")) out.push("Map a column to External ID or Email: every contact needs one.");
  const seen = new Set<string>();
  for (const t of targets) {
    if (!t) continue;
    if (seen.has(t)) out.push(`Two columns are imported as ${t}. Choose one, or don't import the other.`);
    seen.add(t);
    if (!validKey(t)) out.push(`"${t}" isn't a valid field name: use letters, digits and _, starting with a letter.`);
  }
  return [...new Set(out)];
}

/** remapped is the file to upload: the new header, then everything after the old one. */
export function remapped(file: Blob, end: number, targets: string[]): Blob {
  return new Blob([buildHeader(targets), file.slice(end)], { type: "text/csv" });
}
