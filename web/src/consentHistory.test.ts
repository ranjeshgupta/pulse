import { describe, expect, it } from "vitest";
import { consentLine } from "./consentHistory";

const ev = (name: string, props: object) => ({ id: "1", name, ts: "2026-11-21T10:00:00Z", props: JSON.stringify(props) });
describe("consent history", () => {
  it("describes consent events", () => {
    expect(consentLine(ev("unsubscribed", { channel: "email", source: "one_click" }))!.text).toBe("Unsubscribed from email (one click)");
    expect(consentLine(ev("resubscribed", { channel: "whatsapp", source: "form" }))!.text).toBe("Opted in to WhatsApp (form)");
    expect(consentLine(ev("form_submitted", { form_name: "Webinar", consent: true, consent_text: "Email me" }))!.detail).toBe("“Email me”");
    expect(consentLine(ev("form_submitted", { form_name: "Webinar" }))).toBeNull();
    expect(consentLine(ev("email_bounce_action", { action: "leave_segments" }))!.text).toBe("Bounce rule on email: leave segments");
  });
});
