// Funnel and outcome-reason maths for message reports (broadcasts and journeys). Pure: the
// components in funnel.tsx only lay these out.
import type { MessageFunnel, ReasonCount, ReasonKind, SkipGroup } from "./api";

/** The message report's funnel. */
export type FunnelData = MessageFunnel;

/** share formats n as a share of `of` like reports.tsx's pct: "24%", "3.5%", "0%", or "–" when of is 0. */
export function share(n: number, of: number): string {
  if (!of) return "–";
  const p = (n / of) * 100;
  if (p === 0) return "0%";
  return p < 10 ? `${p.toFixed(1)}%` : `${Math.round(p)}%`;
}

/** barWidth is a bar's width in percent of the widest (at least 0.5% so a non-zero count shows). */
export function barWidth(n: number | null, max: number): number {
  if (!n || !max) return 0;
  return Math.max(0.5, Math.min(100, (n / max) * 100));
}

export const DELIVERY_HINT = "Delivery is reported by providers with delivery webhooks; plain SMTP relays don't report it.";

export interface FunnelStep {
  key: string;
  label: string;
  /** null: not reported (shown as "—"). */
  count: number | null;
  /** Share of the step before it (the last step with a count). "" for the first step. */
  ofPrevious: string;
  /** What ofPrevious is a share of, when not the step just above ("" = "previous"). */
  ofLabel?: string;
  /** Share of sent; "" for steps before sending. */
  ofSent: string;
  hint?: string;
  /** Steps before anything is sent (the audience): drawn quieter. */
  before?: boolean;
}

/** funnelSteps builds the funnel's steps: the audience (or enrolled), targeted, sent, delivered,
 * opened / read and clicked. channels decides whether opens and reads apply; without it, any
 * that happened show (opens always do). */
export function funnelSteps(f: FunnelData, channels?: string[]): FunnelStep[] {
  type Raw = { key: string; label: string; count: number | null; hint?: string; before?: boolean; parent?: string };
  const raw: Raw[] = [];
  if (f.audience) {
    const a = f.audience;
    const runs = a.runs > 1 ? ` (${a.runs.toLocaleString("en-IN")} sends)` : "";
    const hint = a.ready ? undefined : "Counted when the send started; not ready for every send.";
    raw.push(
      { key: "audience", label: `Audience at send time${runs}`, count: a.audience, hint, before: true },
      { key: "after_exclude", label: "After exclusions", count: a.after_exclude, before: true },
      { key: "after_consent", label: "After consent", count: a.after_consent, before: true },
      { key: "reachable", label: "Reachable", count: a.reachable, before: true },
    );
  } else if (f.enrolled != null) {
    raw.push({ key: "enrolled", label: "Enrolled", count: f.enrolled, before: true });
  }
  // Targeted includes contacts skipped for consent or reachability, so it compares with the
  // audience after exclusions, not with Reachable (which it can exceed).
  raw.push({ key: "targeted", label: "Targeted", count: f.targeted, before: true, hint: "Contacts a message was tried for: sent, skipped or failed.",
    parent: f.audience ? "after_exclude" : undefined });
  raw.push({ key: "sent", label: "Sent", count: f.sent });
  const noDelivery = f.delivered === 0 && f.sent > 0;
  raw.push({ key: "delivered", label: "Delivered", count: noDelivery ? null : f.delivered, hint: noDelivery ? DELIVERY_HINT : undefined });
  const has = (...cs: string[]) => !!channels?.some((c) => cs.includes(c));
  const showOpened = f.opened > 0 || has("email") || !channels;
  const showRead = f.read > 0 || has("whatsapp", "rcs");
  // Opens (email) and reads (WhatsApp, RCS) are parallel: both compare with the step before them.
  const lastBefore = () => [...raw].reverse().find((s) => s.count != null)?.key;
  const engageParent = lastBefore();
  if (showOpened) raw.push({ key: "opened", label: "Opened", count: f.opened, parent: engageParent });
  if (showRead) raw.push({ key: "read", label: "Read", count: f.read, parent: engageParent });
  raw.push({ key: "clicked", label: "Clicked", count: f.clicked, parent: showOpened && f.opened > 0 ? "opened" : engageParent });

  const counts = new Map(raw.map((s) => [s.key, s.count]));
  const sentIdx = raw.findIndex((s) => s.key === "sent");
  return raw.map((s, i) => {
    let prev: number | null | undefined;
    let parentKey = s.parent;
    if (s.parent) prev = counts.get(s.parent);
    else for (let j = i - 1; j >= 0 && prev == null; j--) { prev = raw[j].count; parentKey = raw[j].key; }
    return {
      key: s.key, label: s.label, count: s.count, hint: s.hint, before: s.before,
      ofPrevious: i === 0 || s.count == null || prev == null ? "" : share(s.count, prev),
      ofLabel: s.parent && s.parent !== raw[i - 1]?.key ? `of ${(raw.find((x) => x.key === s.parent)?.label ?? "").toLowerCase()}` : undefined,
      // Not repeated when the share of the step before is already the share of sent.
      ofSent: i > sentIdx && s.count != null && parentKey !== "sent" ? share(s.count, f.sent) : "",
    };
  });
}

export const SKIP_LABELS: Record<SkipGroup, { label: string; hint: string }> = {
  consent: { label: "Consent", hint: "Opted out, or bounced earlier" },
  reachability: { label: "Reachability", hint: "No address, or not on the channel" },
  cap: { label: "Frequency cap", hint: "Over the channel's message limit" },
  missing_value: { label: "Missing value", hint: "A variable had no value and the template skips the contact" },
  deadline: { label: "Deadline", hint: "Dropped instead of sending late" },
};
const SKIP_ORDER: SkipGroup[] = ["consent", "reachability", "cap", "missing_value", "deadline"];

export interface SkipRow { group: SkipGroup; label: string; hint: string; count: number }

/** skippedRows lists the skip groups with contacts, largest first, and their total. */
export function skippedRows(skipped: Partial<Record<SkipGroup, number>> | undefined): { rows: SkipRow[]; total: number } {
  const rows = SKIP_ORDER.map((g) => ({ group: g, ...SKIP_LABELS[g], count: skipped?.[g] ?? 0 }))
    .filter((r) => r.count > 0)
    .sort((a, b) => b.count - a.count || SKIP_ORDER.indexOf(a.group) - SKIP_ORDER.indexOf(b.group));
  return { rows, total: rows.reduce((n, r) => n + r.count, 0) };
}

export interface SideFigure { key: string; label: string; count: number; detail?: string; rate: string; rateOf: "sent" | "targeted"; tone?: "bad" | "warn" }

/** sideFigures are the outcomes beside the funnel: downloads, bounces, complaints, failures, unsubscribes. */
export function sideFigures(f: FunnelData): SideFigure[] {
  const bounceParts = [f.hard_bounced ? `${f.hard_bounced.toLocaleString("en-IN")} hard` : "", f.soft_bounced ? `${f.soft_bounced.toLocaleString("en-IN")} soft` : ""].filter(Boolean);
  return [
    { key: "downloaded", label: "Downloads", count: f.downloaded, detail: "File link clicks", rate: share(f.downloaded, f.sent), rateOf: "sent" },
    { key: "bounced", label: "Bounced", count: f.bounced, detail: bounceParts.join(" · ") || undefined, rate: share(f.bounced, f.sent), rateOf: "sent", tone: f.bounced ? "bad" : undefined },
    { key: "complained", label: "Spam complaints", count: f.complained, rate: share(f.complained, f.sent), rateOf: "sent", tone: f.complained ? "bad" : undefined },
    { key: "failed", label: "Failed", count: f.failed, detail: "Tried and failed", rate: share(f.failed, f.targeted), rateOf: "targeted", tone: f.failed ? "warn" : undefined },
    { key: "unsubscribed", label: "Unsubscribed", count: f.unsubscribed, rate: share(f.unsubscribed, f.sent), rateOf: "sent" },
  ];
}

export const REASON_KIND_LABELS: Record<ReasonKind, string> = {
  failed: "Failed", bounced: "Bounced", complained: "Spam complaint", suppressed: "Opted out", capped: "Over limit",
};

export interface ReasonRow extends ReasonCount { share: number; shareText: string }

/** reasonRows sorts reasons by contacts (then label) and adds each one's share of all of them. */
export function reasonRows(reasons: ReasonCount[] | undefined): { rows: ReasonRow[]; total: number } {
  const list = (reasons ?? []).filter((r) => r.contacts > 0);
  const total = list.reduce((n, r) => n + r.contacts, 0);
  const rows = [...list]
    .sort((a, b) => b.contacts - a.contacts || a.label.localeCompare(b.label))
    .map((r) => ({ ...r, share: total ? r.contacts / total : 0, shareText: share(r.contacts, total) }));
  return { rows, total };
}

export interface StepReasonGroup { id: string; total: number; rows: ReasonRow[] }

/** stepReasonGroups turns reasons per journey step into groups, the step with most contacts first. */
export function stepReasonGroups(byNode: Record<string, ReasonCount[]> | undefined, order?: Record<string, number>): StepReasonGroup[] {
  return Object.entries(byNode ?? {})
    .map(([id, rs]) => ({ id, ...reasonRows(rs) }))
    .filter((g) => g.total > 0)
    .sort((a, b) => b.total - a.total || (order?.[a.id] ?? Infinity) - (order?.[b.id] ?? Infinity) || a.id.localeCompare(b.id));
}
