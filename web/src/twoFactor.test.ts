import { describe, expect, it } from "vitest";
import { cleanCode, codeReady, isAppCode, policySummary, qrMatrix, recoveryText } from "./twoFactor";

describe("codes", () => {
  it("reads app codes and recovery codes however they're typed", () => {
    expect(cleanCode(" 123 456 ")).toBe("123456");
    expect(isAppCode("123-456")).toBe(true);
    expect(isAppCode("12345")).toBe(false);
    expect(isAppCode("k7wq2-mx9dp")).toBe(false);
    expect(codeReady("K7WQ2 MX9DP")).toBe(true);
    expect(codeReady("12345")).toBe(false);
  });

  it("writes the recovery codes file", () => {
    const text = recoveryText(["aaaaa-bbbbb", "ccccc-ddddd"], "acme-retail", "asha@example.com");
    expect(text).toContain("asha@example.com (workspace acme-retail)");
    expect(text.split("\n")).toContain("ccccc-ddddd");
  });
});

describe("policySummary", () => {
  const role = (r: string) => (r === "admin" ? "Admins" : r);
  const user = (id: number) => (id === 7 ? "Asha" : `#${id}`);
  it("says who must use it", () => {
    expect(policySummary({ mode: "optional", roles: [], users: [] }, role, user)).toMatch(/Optional/);
    expect(policySummary({ mode: "everyone", roles: [], users: [] }, role, user)).toMatch(/everyone/);
    expect(policySummary({ mode: "some", roles: ["admin"], users: [7, 9] }, role, user)).toBe("Required for Admins, Asha and #9.");
    expect(policySummary({ mode: "some", roles: [], users: [] }, role, user)).toMatch(/nobody/);
  });
});

describe("qrMatrix", () => {
  it("makes a square QR code with finder patterns", () => {
    const m = qrMatrix("otpauth://totp/Pulse:asha%40example.com?secret=GEZDGNBVGY3TQOJQ&issuer=Pulse");
    expect(m.length).toBeGreaterThanOrEqual(21);
    expect(m.every((row) => row.length === m.length)).toBe(true);
    // Top-left finder pattern: a dark 7x7 ring.
    expect(m[0].slice(0, 7).every(Boolean)).toBe(true);
    expect(m[1][1]).toBe(false);
    expect(m[3][3]).toBe(true);
  });
});
