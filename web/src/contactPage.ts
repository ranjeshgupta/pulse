// Pure helpers for the contact page (pages/ContactPage.tsx).
import type { Contact, ContactMessage } from "./api";

/** contactName is the contact's name: the name attribute, or first and last name. */
export function contactName(c: Pick<Contact, "attrs">): string {
  const n = c.attrs.name;
  if (typeof n === "string" && n.trim()) return n.trim();
  return [c.attrs.first_name, c.attrs.last_name].filter((v) => v != null && String(v).trim() !== "").map((v) => String(v).trim()).join(" ");
}

/** attrText reads an attribute as text ("" when missing). */
export function attrText(c: Pick<Contact, "attrs">, key: string): string {
  const v = c.attrs[key];
  if (v == null) return "";
  if (Array.isArray(v)) return v.map(String).join(", ");
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

/** The activity windows the profile counts, in days. */
export const WINDOWS = ["1", "7", "30", "365"] as const;
export type Window = (typeof WINDOWS)[number];
export const WINDOW_LABELS: Record<Window, string> = { "1": "Today", "7": "7 days", "30": "30 days", "365": "365 days" };

/** productList reads the products list field: a list, a comma-separated string, or a JSON list. */
export function productList(v: unknown): string[] {
  let items: unknown[] = [];
  if (Array.isArray(v)) items = v;
  else if (typeof v === "string") {
    const t = v.trim();
    if (t.startsWith("[")) {
      try { const parsed = JSON.parse(t); if (Array.isArray(parsed)) items = parsed; } catch { items = t.split(","); }
    } else items = t.split(",");
  } else if (v != null && typeof v !== "object") items = [v];
  const out: string[] = [];
  for (const x of items) {
    const s = (x != null && typeof x === "object" ? JSON.stringify(x) : String(x ?? "")).trim();
    if (s && !out.includes(s)) out.push(s);
  }
  return out;
}

/** campaignLink is where a message's campaign lives: a broadcast or a journey. */
export function campaignLink(m: Pick<ContactMessage, "source" | "source_id">): string | null {
  if (!m.source_id) return null;
  if (m.source === "broadcast") return `/broadcasts/${m.source_id}`;
  if (m.source === "journey") return `/journeys/${m.source_id}`;
  return null;
}

/** campaignName names a message's campaign when the server didn't (deleted ones). */
export function campaignName(m: Pick<ContactMessage, "source" | "source_id" | "campaign">): string {
  if (m.campaign) return m.campaign;
  if (m.source === "broadcast") return `One-time campaign ${m.source_id} (deleted)`;
  if (m.source === "journey") return `Campaign ${m.source_id} (deleted)`;
  return "Other";
}

const MESSAGE_STATUS: Record<string, { label: string; tone: string }> = {
  complained: { label: "Spam complaint", tone: "bad" },
  bounced: { label: "Bounced", tone: "bad" },
  failed: { label: "Failed", tone: "bad" },
  clicked: { label: "Clicked", tone: "ok" },
  opened: { label: "Opened", tone: "ok" },
  read: { label: "Read", tone: "ok" },
  delivered: { label: "Delivered", tone: "ok" },
  sent: { label: "Sent", tone: "info" },
  fallback: { label: "Sent as SMS", tone: "info" },
  suppressed: { label: "Opted out", tone: "warn" },
  capped: { label: "Over limit", tone: "warn" },
  deferred: { label: "Held for quiet hours", tone: "warn" },
};

/** messageStatus is a message status's label and badge tone. */
export function messageStatus(status: string): { label: string; tone: string } {
  return MESSAGE_STATUS[status] ?? { label: status ? status[0].toUpperCase() + status.slice(1).replace(/_/g, " ") : "Unknown", tone: "" };
}

/** formatProps pretty-prints an event's props JSON ("" for none). */
export function formatProps(props: string): string {
  if (!props || props === "{}" || props === "null") return "";
  try { return JSON.stringify(JSON.parse(props), null, 2); } catch { return props; }
}

/** propsPreview is a one-line summary of an event's props: key=value pairs, cut at max characters. */
export function propsPreview(props: string, max = 120): string {
  if (!props || props === "{}" || props === "null") return "";
  let text: string;
  try {
    const v = JSON.parse(props);
    text = v && typeof v === "object" && !Array.isArray(v)
      ? Object.entries(v).map(([k, x]) => `${k}=${typeof x === "object" && x !== null ? JSON.stringify(x) : String(x)}`).join(" · ")
      : JSON.stringify(v);
  } catch { text = props; }
  return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

/** parseNames reads a comma-separated list of event names: trimmed, unique, at most 20. */
export function parseNames(text: string): string[] {
  const out: string[] = [];
  for (const n of text.split(",").map((s) => s.trim()).filter(Boolean)) if (!out.includes(n)) out.push(n);
  return out.slice(0, 20);
}

/** toggleName adds a name to a comma list, or removes it when it's there. */
export function toggleName(text: string, name: string): string {
  const names = parseNames(text);
  return (names.includes(name) ? names.filter((n) => n !== name) : [...names, name]).join(", ");
}

export type EventKind = "" | "activity" | "pulse";

/** eventsPath is the timeline request for one page. */
export function eventsPath(externalId: string, f: { kind: EventKind; names: string[]; before?: string; limit: number }): string {
  const q = new URLSearchParams();
  if (f.names.length) q.set("name", f.names.join(","));
  if (f.kind) q.set("kind", f.kind);
  if (f.before) q.set("before", f.before);
  q.set("limit", String(f.limit));
  return `/v1/contacts/${encodeURIComponent(externalId)}/events?${q}`;
}

/** messagesPath is the message history request for one page. */
export function messagesPath(externalId: string, before: string | undefined, limit: number): string {
  const q = new URLSearchParams();
  if (before) q.set("before", before);
  q.set("limit", String(limit));
  return `/v1/contacts/${encodeURIComponent(externalId)}/messages?${q}`;
}

/** nextBefore is the cursor for the next page: the last row's time, when the page was full. */
export function nextBefore<T>(rows: T[], limit: number, time: (r: T) => string): string | undefined {
  return rows.length >= limit && rows.length > 0 ? time(rows[rows.length - 1]) : undefined;
}

/** uniqueBy drops rows whose key was seen before (pages can overlap on equal times). */
export function uniqueBy<T>(rows: T[], key: (r: T) => string): T[] {
  const seen = new Set<string>();
  return rows.filter((r) => { const k = key(r); if (seen.has(k)) return false; seen.add(k); return true; });
}

/** distinctNames lists the event names in rows, most frequent first. */
export function distinctNames(rows: { name: string }[], max = 15): string[] {
  const counts = new Map<string, number>();
  for (const r of rows) counts.set(r.name, (counts.get(r.name) ?? 0) + 1);
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, max).map(([n]) => n);
}

/** formatTime is a short absolute date and time. */
export function formatTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString("en-IN", { day: "numeric", month: "short", year: "numeric", hour: "numeric", minute: "2-digit" });
}

/** stepTypeLabel turns a journey step type (send_email) into words (Send email). */
export function stepTypeLabel(t: string): string {
  if (!t) return "";
  const s = t.replace(/_/g, " ");
  return s[0].toUpperCase() + s.slice(1);
}
