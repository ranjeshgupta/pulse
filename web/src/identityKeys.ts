// Settings → Identity: which fields identify a person and in what order (the "identity" section of
// the automation settings, package identity on the server).
import type { IdentityBackfill, IdentityKey } from "./api";
import { own } from "./safe";

export const EXTERNAL_ID = "external_id";
export const MAX_KEYS = 8;
export const DEFAULT_KEYS: IdentityKey[] = [{ field: EXTERNAL_ID, mode: "merge" }];

/** The fields every workspace has, offered first. */
export const BUILT_IN: { field: string; label: string; hint: string }[] = [
  { field: EXTERNAL_ID, label: "External id", hint: "The id your website, app or CRM sends with every event" },
  { field: "email", label: "Email address", hint: "Compared in lower case; an external id that is an email counts too" },
  { field: "phone", label: "Phone number", hint: "Compared as digits, +country code kept; an external id that is a phone counts too" },
];

export function keyLabel(field: string, labels: Record<string, string> = {}): string {
  // own(): a field named "constructor" is labelled by its name, not by Object's constructor.
  return BUILT_IN.find((b) => b.field === field)?.label ?? own(labels, field) ?? String(field ?? "");
}

/** move returns keys with the key at i moved by delta (-1 up, +1 down). */
export function move(keys: IdentityKey[], i: number, delta: number): IdentityKey[] {
  const j = i + delta;
  if (j < 0 || j >= keys.length) return keys;
  const out = keys.slice();
  [out[i], out[j]] = [out[j], out[i]];
  return out;
}

export function addKey(keys: IdentityKey[], field: string): IdentityKey[] {
  if (!field || keys.some((k) => k.field === field) || keys.length >= MAX_KEYS) return keys;
  return [...keys, { field, mode: "merge" }];
}

export function removeKey(keys: IdentityKey[], field: string): IdentityKey[] {
  return field === EXTERNAL_ID ? keys : keys.filter((k) => k.field !== field);
}

/** problem is why keys can't be saved, or "". */
export function problem(keys: IdentityKey[]): string {
  if (!keys.some((k) => k.field === EXTERNAL_ID)) return "The external id must stay one of the keys.";
  if (keys.length > MAX_KEYS) return `At most ${MAX_KEYS} keys.`;
  if (new Set(keys.map((k) => k.field)).size !== keys.length) return "A field is listed twice.";
  return "";
}

/** explain says in words what the order does, for the example under the list. */
export function explain(keys: IdentityKey[], labels: Record<string, string> = {}): string[] {
  const merging = keys.filter((k) => k.field !== EXTERNAL_ID && k.mode === "merge");
  if (merging.length === 0) return ["Contacts are told apart by their external id only. Contacts sharing an email or phone are listed under Duplicates for you to merge."];
  const order = keys.filter((k) => k.field === EXTERNAL_ID || k.mode === "merge").map((k) => keyLabel(k.field, labels).toLowerCase());
  const out = [`An event is matched by ${order.join(", then ")}.`];
  const ext = keys.findIndex((k) => k.field === EXTERNAL_ID);
  const first = merging[0];
  out.push(`A new external id whose ${keyLabel(first.field, labels).toLowerCase()} is already known joins the contact that has it.`);
  const above = merging.filter((k) => keys.indexOf(k) < ext);
  const below = merging.filter((k) => keys.indexOf(k) > ext);
  if (below.length) out.push(`A known contact that gets another contact's ${below.map((k) => keyLabel(k.field, labels).toLowerCase()).join(" or ")} takes that contact in (it keeps its own values; the other's fill the gaps).`);
  if (above.length) out.push(`Ranked above the external id: a known contact that gets another contact's ${above.map((k) => keyLabel(k.field, labels).toLowerCase()).join(" or ")} is merged into that contact.`);
  const suggest = keys.filter((k) => k.mode === "suggest");
  if (suggest.length) out.push(`Shared ${suggest.map((k) => keyLabel(k.field, labels).toLowerCase()).join(", ")} values are only listed under Duplicates.`);
  return out;
}

/** progress is a backfill's state in words, or "" when there is none. */
export function progress(b: IdentityBackfill | undefined): string {
  if (!b) return "";
  if (b.done_at) return "Ready";
  if (b.contacts <= 0) return "Starting…";
  const pct = Math.max(0, Math.min(99, Math.floor((b.next_id / b.contacts) * 100) || 0));
  return `Reading existing contacts… ${pct}%`;
}
