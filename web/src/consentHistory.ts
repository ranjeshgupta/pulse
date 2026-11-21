// Consent history (O6, data-protection rights): what a contact agreed to and withdrew, when and
// how, from their events.
import type { EventRow } from "./api";

export const CONSENT_EVENTS = ["unsubscribed", "resubscribed", "optin_requested", "optin_confirmed", "form_submitted", "email_complained",
  "email_bounce_action", "sms_bounce_action", "whatsapp_bounce_action", "rcs_bounce_action"];

const CH: Record<string, string> = { email: "email", sms: "SMS", whatsapp: "WhatsApp", rcs: "RCS", push: "push", inbox: "app inbox", all: "everything" };

/** consentLine describes one event in words, or null when it isn't about consent (a form without consent). */
export function consentLine(e: EventRow): { icon: string; text: string; detail?: string } | null {
  let p: Record<string, unknown> = {};
  try { p = JSON.parse(e.props || "{}"); } catch { /* keep empty */ }
  const ch = CH[String(p.channel ?? "")] ?? String(p.channel ?? "");
  const via = p.source ? ` (${String(p.source).replace(/_/g, " ")})` : "";
  const topic = p.topic ? ` — topic “${String(p.topic)}”` : "";
  switch (e.name) {
    case "unsubscribed": return { icon: "🚫", text: `Unsubscribed from ${ch || "a channel"}${topic}${via}` };
    case "resubscribed": return { icon: "✅", text: `Opted in to ${ch || "a channel"}${topic}${via}` };
    case "optin_requested": return { icon: "✉️", text: `Asked to confirm their email${p.form_name ? ` (form “${String(p.form_name)}”)` : ""}`, detail: p.email ? String(p.email) : undefined };
    case "optin_confirmed": return { icon: "☑️", text: `Confirmed their subscription${p.form_name ? ` (form “${String(p.form_name)}”)` : ""}` };
    case "form_submitted":
      if (!p.consent) return null;
      return { icon: "📝", text: `Agreed on form “${String(p.form_name ?? p.form_id ?? "")}”`, detail: p.consent_text ? `“${String(p.consent_text)}”` : undefined };
    case "email_complained": return { icon: "⚠️", text: "Marked an email as spam" };
    default:
      if (e.name.endsWith("_bounce_action")) return { icon: "↩️", text: `Bounce rule on ${CH[e.name.split("_")[0]] ?? e.name.split("_")[0]}: ${String(p.action ?? "").replace(/_/g, " ")}` };
      return null;
  }
}
