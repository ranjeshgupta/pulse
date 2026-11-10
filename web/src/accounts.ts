// Pure helpers for stages, companies and duplicate merges, shared by their pages and the contact drawer.
import type { Company, Contact } from "./api";

/** Company attribute keys become company_<key> on contacts, which must stay a 64-character attribute key. */
export const MAX_COMPANY_KEY = 64 - "company_".length;
const COMPANY_KEY_RE = /^[a-zA-Z_][a-zA-Z0-9_]*$/;

/** One row of the company attributes editor: values are typed as text and converted on save. */
export type AttrRow = { key: string; value: string };

export function rowsFromAttrs(attrs: Company["attrs"]): AttrRow[] {
  return Object.entries(attrs).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => ({ key, value: String(value) }));
}

/** attrsFromRows converts editor rows to the API's attributes: numbers and true/false keep their type, empty rows are dropped. */
export function attrsFromRows(rows: AttrRow[]): Company["attrs"] {
  const out: Company["attrs"] = {};
  for (const r of rows) {
    const key = r.key.trim();
    if (!key) continue;
    const v = r.value.trim();
    if (v === "true" || v === "false") out[key] = v === "true";
    else if (v !== "" && /^-?\d+(\.\d+)?$/.test(v)) out[key] = Number(v);
    else out[key] = r.value;
  }
  return out;
}

/** companyProblems lists what the API would refuse in a company form, in words. */
export function companyProblems(name: string, domain: string, rows: AttrRow[]): string[] {
  const out: string[] = [];
  if (!name.trim()) out.push("Enter the company's name.");
  if (name.trim().length > 200) out.push("Keep the name under 200 characters.");
  if (/[\s/@:]/.test(domain.trim())) out.push("Enter a domain like example.com.");
  const seen = new Set<string>();
  for (const r of rows) {
    const k = r.key.trim();
    if (!k && !r.value.trim()) continue;
    if (!COMPANY_KEY_RE.test(k) || k.length > MAX_COMPANY_KEY) out.push(`"${k || "(empty)"}": attribute names use letters, digits and _ (up to ${MAX_COMPANY_KEY}).`);
    else if (k === "id" || k === "name") out.push(`"${k}" is taken by the company itself; choose another attribute name.`);
    else if (seen.has(k)) out.push(`"${k}" is used twice.`);
    if (r.value.length > 500) out.push(`"${k}": keep values under 500 characters.`);
    seen.add(k);
  }
  if (seen.size > 50) out.push("A company can have up to 50 attributes.");
  return out;
}

/** The company a contact is linked to, from its profile (company_id, company_name). */
export function contactCompany(c: Pick<Contact, "attrs">): { id: number; name: string } | null {
  const id = Number(c.attrs.company_id);
  return Number.isInteger(id) && id > 0 ? { id, name: String(c.attrs.company_name ?? `Company ${id}`) } : null;
}

/** Profile attributes the drawer shows in their own sections instead of the plain attribute list. */
export function isAccountAttr(key: string): boolean {
  return key === "stage" || key === "stage_changed_at" || key === "owner_id" || key === "owner_name" || key.startsWith("company_");
}

/** mergePlan lists the merges that fold a duplicate group into winner: every other contact, oldest first. */
export function mergePlan(group: Pick<Contact, "external_id">[], winner: string): { winner: string; loser: string }[] {
  return group.filter((c) => c.external_id !== winner).map((c) => ({ winner, loser: c.external_id }));
}

/** moveItem returns ids with the one at index moved by delta (-1 up, +1 down), or ids unchanged at the ends. */
export function moveItem<T>(ids: T[], index: number, delta: -1 | 1): T[] {
  const to = index + delta;
  if (index < 0 || index >= ids.length || to < 0 || to >= ids.length) return ids;
  const out = [...ids];
  [out[index], out[to]] = [out[to], out[index]];
  return out;
}

/** describeThreshold words a stage's points threshold for lists. */
export function describeThreshold(minPoints: number | null): string {
  return minPoints == null ? "set by hand, journeys or the API" : `at ${minPoints.toLocaleString("en-IN")} points or more`;
}
