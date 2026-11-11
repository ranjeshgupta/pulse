import { describe, expect, it } from "vitest";
import { formFrom, mailboxRequest, passwordNeeded, type BounceMailbox } from "./BounceMailbox";

const saved: BounceMailbox = { enabled: true, host: "imap.example.com", port: 993, tls: "tls", username: "bounces", folder: "INBOX", has_password: true, last_error: "", processed: 0, skipped: 0 };

describe("bounce mailbox form", () => {
  it("keeps the saved password when it is sent back masked", () => {
    const f = formFrom(saved, "••••••••");
    expect(mailboxRequest(f).password).toBe("");
    expect(passwordNeeded(saved, f)).toBe(false);
  });
  it("asks for the password again when the server or user changes", () => {
    const f = formFrom(saved, "••••••••");
    expect(passwordNeeded(saved, { ...f, host: "evil.test" })).toBe(true);
    expect(passwordNeeded(saved, { ...f, tls: "starttls", port: "143" })).toBe(true);
    expect(passwordNeeded(saved, { ...f, folder: "Bounces" })).toBe(false);
    expect(passwordNeeded(saved, { ...f, host: "evil.test", password: "typed" })).toBe(false);
  });
  it("fills defaults", () => {
    const f = formFrom(null, "");
    expect(mailboxRequest({ ...f, host: " imap.x.com ", username: "u", folder: "", port: "" })).toEqual(
      { enabled: true, host: "imap.x.com", port: 993, tls: "tls", username: "u", folder: "INBOX", password: "" });
    expect(passwordNeeded(null, f)).toBe(true);
  });
});
