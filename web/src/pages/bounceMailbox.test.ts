import { describe, expect, it } from "vitest";
import { folderProblem, formFrom, mailboxRequest, passwordNeeded, type BounceMailbox } from "./BounceMailbox";

const saved: BounceMailbox = {
  id: 3, name: "Bounces", enabled: true, host: "imap.example.com", port: 993, tls: "tls", username: "bounces",
  folders: [{ name: "INBOX", kind: "auto" }], has_password: true, last_error: "", processed: 0, skipped: 0,
};

describe("bounce mailbox form", () => {
  it("keeps the saved password when it is sent back masked", () => {
    const f = formFrom(saved, "••••••••");
    expect(mailboxRequest(f).password).toBe("");
    expect(mailboxRequest(f).id).toBe(3);
    expect(passwordNeeded(saved, f)).toBe(false);
  });
  it("asks for the password again when the server or user changes", () => {
    const f = formFrom(saved, "••••••••");
    expect(passwordNeeded(saved, { ...f, host: "evil.test" })).toBe(true);
    expect(passwordNeeded(saved, { ...f, tls: "starttls", port: "143" })).toBe(true);
    expect(passwordNeeded(saved, { ...f, folders: [{ name: "Bounces", kind: "bounces" }] })).toBe(false);
    expect(passwordNeeded(saved, { ...f, host: "evil.test", password: "typed" })).toBe(false);
  });
  it("fills defaults and drops empty folder rows", () => {
    const f = formFrom(null, "");
    expect(mailboxRequest({ ...f, host: " imap.x.com ", username: "u", port: "", folders: [{ name: " ", kind: "auto" }] })).toEqual(
      { id: undefined, name: "", enabled: true, host: "imap.x.com", port: 993, tls: "tls", username: "u", folders: [{ name: "INBOX", kind: "auto" }], password: "" });
    expect(passwordNeeded(null, f)).toBe(true);
  });
  it("checks folders", () => {
    expect(folderProblem([{ name: "Bounces", kind: "bounces" }, { name: "[Gmail]/Spam reports", kind: "complaints" }])).toBe("");
    expect(folderProblem([{ name: "A", kind: "auto" }, { name: "A", kind: "bounces" }])).toMatch(/twice/);
    expect(folderProblem([{ name: 'a"b', kind: "auto" }])).toMatch(/quotes/);
  });
});
