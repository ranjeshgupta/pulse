// Checks for Settings → Bounces & unsubscribes → Unsubscribes, mirroring the server's
// (internal/automation/unsubscribe.go).
import type { UnsubscribeSettings } from "./api";

const CHANNELS = new Set(["email", "sms", "whatsapp", "rcs", "push", "inbox", "all"]);

/** topicKeyFrom turns a topic name into its key: lowercase letters, digits and _. */
export function topicKeyFrom(name: string): string {
  return name.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").slice(0, 40);
}

/** unsubscribeProblem: the first thing to fix, or "". */
export function unsubscribeProblem(u: UnsubscribeSettings): string {
  if (u.topics.length > 30) return "Use at most 30 topics.";
  const seen = new Set<string>();
  for (const t of u.topics) {
    const label = t.name.trim() || "A topic";
    if (!t.name.trim()) return "Give every topic a name.";
    if (t.name.trim().length > 80) return `${label}: use a shorter name (80 characters at most).`;
    if (!/^[a-z0-9_]{1,40}$/.test(t.key)) return `${label}: the name needs at least one letter or digit.`;
    if (CHANNELS.has(t.key)) return `${label}: pick a name that isn't a channel's.`;
    if (seen.has(t.key)) return `${label}: two topics have the same key (${t.key}); rename one.`;
    if ((t.description ?? "").length > 300) return `${label}: the description is too long.`;
    seen.add(t.key);
  }
  return "";
}
