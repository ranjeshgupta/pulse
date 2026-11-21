// Consent history (O6, data-protection rights): what a contact agreed to and withdrew, when and
// how, from their events.
import type { EventRow } from "./api";
import { dict, own } from "./safe";

export const CONSENT_EVENTS = ["unsubscribed", "resubscribed", "optin_requested", "optin_confirmed", "form_submitted", "email_complained",
  "email_bounce_action", "sms_bounce_action", "whatsapp_bounce_action", "rcs_bounce_action"];

const CH: Record<string, string> = dict({ email: "email", sms: "SMS", whatsapp: "WhatsApp", rcs: "RCS", push: "push", inbox: "app inbox", all: "everything" });

/** consentLine describes one event in words, or null when it isn't about consent (a form without consent). */
export function consentLine(e: EventRow): { icon: string; text: string; detail?: string } | null {
  let p: Record<string, unknown> = {};
  // Props are what the event carried: "null", a list or a number parse fine but aren't an object.
  try { const v: unknown = JSON.parse(e.props || "{}"); if (v && typeof v === "object" && !Array.isArray(v)) p = v as Record<string, unknown>; } catch { /* keep empty */ }
  p = { channel: own(p, "channel"), source: own(p, "source"), topic: own(p, "topic"), form_name: own(p, "form_name"), form_id: own(p, "form_id"),
    email: own(p, "email"), consent: own(p, "consent"), consent_text: own(p, "consent_text"), action: own(p, "action") };
  // Values as text: a nested object as JSON rather than "[object Object]".
  const text = (v: unknown) => (v !== null && typeof v === "object" ? JSON.stringify(v) : String(v ?? ""));
  const ch = CH[text(p.channel ?? "")] ?? text(p.channel ?? "");
  const via = p.source ? ` (${text(p.source).replace(/_/g, " ")})` : "";
  const topic = p.topic ? ` — topic “${text(p.topic)}”` : "";
  switch (e.name) {
    case "unsubscribed": return { icon: "🚫", text: `Unsubscribed from ${ch || "a channel"}${topic}${via}` };
    case "resubscribed": return { icon: "✅", text: `Opted in to ${ch || "a channel"}${topic}${via}` };
    case "optin_requested": return { icon: "✉️", text: `Asked to confirm their email${p.form_name ? ` (form “${text(p.form_name)}”)` : ""}`, detail: p.email ? text(p.email) : undefined };
    case "optin_confirmed": return { icon: "☑️", text: `Confirmed their subscription${p.form_name ? ` (form “${text(p.form_name)}”)` : ""}` };
    case "form_submitted":
      if (!p.consent) return null;
      return { icon: "📝", text: `Agreed on form “${text(p.form_name ?? p.form_id ?? "")}”`, detail: p.consent_text ? `“${text(p.consent_text)}”` : undefined };
    case "email_complained": return { icon: "⚠️", text: "Marked an email as spam" };
    default:
      if (e.name.endsWith("_bounce_action")) return { icon: "↩️", text: `Bounce rule on ${CH[e.name.split("_")[0]] ?? e.name.split("_")[0]}: ${text(p.action ?? "").replace(/_/g, " ")}` };
      return null;
  }
}
