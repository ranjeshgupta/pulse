// Bounce rules (Settings → Bounces & unsubscribes): labels and plain sentences for the rules the
// server applies (internal/automation/bounces.go, internal/profile/bounce.go).
import { CHANNEL_NAMES, type BounceAction, type BounceRule, type Channel, type ChannelBounces } from "./api";

export const BOUNCE_CHANNELS: Channel[] = ["email", "sms", "whatsapp", "rcs"];

export const BOUNCE_ACTIONS: { key: BounceAction; label: string }[] = [
  { key: "record", label: "Only count it" },
  { key: "stop", label: "Stop sending on this channel" },
  { key: "unsubscribe", label: "Unsubscribe from this channel" },
  { key: "leave_segments", label: "Stop sending and leave all segments" },
  { key: "do_not_contact", label: "Do Not Contact (every channel)" },
  { key: "delete", label: "Delete the contact" },
];

export const BOUNCE_TYPES: { key: keyof ChannelBounces; label: string; hint: Record<string, string> }[] = [
  { key: "hard", label: "Hard bounce", hint: { email: "The address doesn't exist or refuses mail for good", other: "Invalid number, or the number can't take messages" } },
  { key: "soft", label: "Soft bounce", hint: { email: "Mailbox full, server busy, message too big", other: "Phone off or unreachable, not on the channel right now" } },
  { key: "complaint", label: "Spam complaint", hint: { email: "Marked as spam (feedback loop)", whatsapp: "Blocked or reported the business", other: "Reported or blocked the sender" } },
];

const typeWords: Record<keyof ChannelBounces, [string, string]> = {
  hard: ["hard bounce", "hard bounces"], soft: ["soft bounce", "soft bounces"], complaint: ["spam complaint", "spam complaints"],
};

const actionWords: Record<BounceAction, (ch: string) => string> = {
  record: () => "Pulse only counts it",
  stop: (ch) => `${ch} to that ${ch === "Email" ? "address" : "number"} stops`,
  unsubscribe: (ch) => `the contact is unsubscribed from ${ch}`,
  leave_segments: (ch) => `${ch} stops and the contact leaves every segment`,
  do_not_contact: () => "the contact is unsubscribed from every channel",
  delete: () => "the contact and their history are deleted",
};

/** ruleSentence: a rule in words, e.g. "After 3 soft bounces, Email to that address stops." */
export function ruleSentence(ch: Channel, type: keyof ChannelBounces, r: BounceRule): string {
  const [one, many] = typeWords[type];
  const n = r.count === 1 ? `the first ${one}` : `${r.count} ${many}`;
  return `After ${n}, ${actionWords[r.action]?.(CHANNEL_NAMES[ch]) ?? "nothing happens"}.`;
}

/** ruleProblem: why a channel's rules can't be saved, or "". */
export function ruleProblem(rules: ChannelBounces): string {
  for (const t of BOUNCE_TYPES) {
    const r = rules[t.key];
    if (!Number.isInteger(r.count) || r.count < 1 || r.count > 100) return `${t.label}: the count must be a whole number from 1 to 100.`;
    if (!BOUNCE_ACTIONS.some((a) => a.key === r.action)) return `${t.label}: choose what to do.`;
  }
  return "";
}
