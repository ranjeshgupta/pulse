// Campaigns hub and one-time campaign helpers (M3): categories, tags, send deadlines, the audience
// estimate's steps and the wizard's checks. Pure, so vitest covers them.

import type { CampaignCategory, Channel, Deadline, Estimate } from "./api";

export const CATEGORIES: { key: CampaignCategory; label: string; hint: string }[] = [
  { key: "one_time", label: "One-time", hint: "Broadcasts: one message to a segment, once or on a repeat." },
  { key: "trigger", label: "Trigger based", hint: "Journeys started by an event or a form." },
  { key: "automation", label: "Automation", hint: "Journeys contacts enter when they join a segment." },
];

export const MAX_TAGS = 20;
export const MAX_TAG_LENGTH = 40;

/** normalizeTags trims, collapses spaces and drops repeats ignoring case (the first spelling wins), like the server. */
export function normalizeTags(tags: string[]): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const raw of tags) {
    const t = raw.trim().split(/\s+/).join(" ");
    if (!t || seen.has(t.toLowerCase())) continue;
    seen.add(t.toLowerCase());
    out.push(t);
  }
  return out;
}

/** tagProblem explains why a tag can't be added, or null. */
export function tagProblem(tag: string, tags: string[]): string | null {
  const t = tag.trim();
  if (!t) return null;
  if (t.length > MAX_TAG_LENGTH) return `Tags are at most ${MAX_TAG_LENGTH} characters.`;
  if (/[,\n\t]/.test(t)) return "Tags can't contain commas.";
  if (normalizeTags([...tags, t]).length > MAX_TAGS) return `At most ${MAX_TAGS} tags.`;
  return null;
}

/** suggestTags lists known tags matching what is typed, not already used. */
export function suggestTags(known: string[], typed: string, used: string[], limit = 8): string[] {
  const q = typed.trim().toLowerCase();
  const usedL = new Set(used.map((u) => u.toLowerCase()));
  return known.filter((k) => !usedL.has(k.toLowerCase()) && (!q || k.toLowerCase().includes(q))).slice(0, limit);
}

// ---- send deadlines ----

const HHMM = /^([01]\d|2[0-3]):[0-5]\d$/;

/** deadlineProblem checks a deadline as the server does (null/undefined: none). */
export function deadlineProblem(d?: Deadline | null): string | null {
  if (!d) return null;
  if (!d.at && !d.hours) return "Set a time of day or a number of hours for the send deadline.";
  if (d.at && !HHMM.test(d.at)) return "Write the deadline's time as HH:MM, e.g. 21:00.";
  if (d.hours !== undefined && (d.hours < 0 || d.hours > 168 || !Number.isInteger(d.hours))) return "A message may be 1 to 168 hours late.";
  return null;
}

/** describeDeadline says what a deadline does, for summaries. */
export function describeDeadline(d?: Deadline | null, localTime = false): string {
  if (!d || (!d.at && !d.hours)) return "No deadline: late messages still go out.";
  const parts: string[] = [];
  if (d.at) parts.push(`after ${d.at}${localTime ? " in each contact's time zone" : d.timezone ? ` (${d.timezone})` : " UTC"}`);
  if (d.hours) parts.push(`more than ${d.hours} hour${d.hours === 1 ? "" : "s"} after the send starts`);
  return `Not sent ${parts.join(", or ")}: those messages are dropped and reported as late.`;
}

// ---- the audience estimate ----

export type EstimateStep = { key: string; label: string; count: number; dropped: number; note?: string };

/** estimateSteps lays an estimate out as the steps the wizard shows, with how many each step drops. */
export function estimateSteps(e: Estimate, channelName: string, excluding: boolean): EstimateStep[] {
  const steps: EstimateStep[] = [{ key: "audience", label: "In the chosen segments", count: e.audience, dropped: 0 }];
  if (excluding) steps.push({ key: "exclude", label: "After exclusions", count: e.after_exclude, dropped: e.audience - e.after_exclude });
  steps.push({ key: "consent", label: `Not unsubscribed from ${channelName}`, count: e.after_consent, dropped: e.after_exclude - e.after_consent });
  steps.push({ key: "reach", label: `Reachable on ${channelName}`, count: e.reachable, dropped: e.after_consent - e.reachable,
    note: "has an address, not bounced, not Do Not Contact" });
  if (e.capped && e.cap) {
    steps.push({ key: "cap", label: `Under the frequency cap (${e.cap.max} per ${e.cap.hours} h)`, count: e.will_send, dropped: e.capped.capped,
      note: e.capped.approximate ? `estimated from ${e.capped.checked.toLocaleString("en-IN")} contacts` : undefined });
  }
  return steps;
}

// ---- the one-time wizard ----

export type WizardStep = "audience" | "content" | "schedule" | "review";
export const WIZARD_STEPS: { key: WizardStep; label: string }[] = [
  { key: "audience", label: "Audience" }, { key: "content", label: "Content" }, { key: "schedule", label: "Schedule" }, { key: "review", label: "Review" },
];

export interface WizardState {
  name: string; channel: Channel; include: number[]; exclude: number[];
  /** content: a template written here, or a reusable one chosen. */
  templateMode: "write" | "pick"; pickedTemplate?: number;
  start_at: string; end_at?: string | null; repeat: string; local_time?: boolean; deadline?: Deadline | null;
}

/** stepProblem says what blocks leaving a step, or null. */
export function stepProblem(s: WizardState, step: WizardStep, content: { subject?: string; body?: boolean }): string | null {
  switch (step) {
    case "audience":
      if (!s.name.trim()) return "Name the campaign.";
      if (s.include.length === 0) return "Choose at least one segment to send to.";
      if (s.include.some((i) => s.exclude.includes(i))) return "A segment can't be both sent to and excluded.";
      return null;
    case "content":
      if (s.templateMode === "pick") return s.pickedTemplate ? null : "Choose a template.";
      if (s.channel === "email" && !content.subject?.trim()) return "Write a subject.";
      if (!content.body) return "Write the message.";
      return null;
    case "schedule":
      if (!s.start_at) return "Choose when to send.";
      return deadlineProblem(s.deadline);
    default:
      return null;
  }
}
