import { describe, expect, it } from "vitest";
import { loginError, loginStep, passwordChangeError, passwordProblem, sameURL, ssoSecretNeeded } from "./accountSecurity";

const q = (s: string) => new URLSearchParams(s);

describe("login page from the address", () => {
  it("opens the two-step part after single sign-on", () => {
    expect(loginStep(q("two_factor=code"))).toBe("code");
    expect(loginStep(q("two_factor=setup"))).toBe("setup");
    expect(loginStep(q(""))).toBe("password");
    expect(loginStep(q("two_factor=password"))).toBe("password");
    expect(loginStep(q("two_factor=constructor"))).toBe("password");
  });

  it("explains sign-in errors, never through inherited keys", () => {
    expect(loginError(q(""))).toBeNull();
    expect(loginError(q("error=sso_link_needed"))).toBe(
      "This email already has a password account. Sign in with your password first, then use Single sign-on once to link it.");
    expect(loginError(q("error=sso_denied"))).toMatch(/cancelled/);
    expect(loginError(q("error=constructor"))).toBe("Sign-in failed.");
    expect(loginError(q("error=__proto__"))).toBe("Sign-in failed.");
    expect(loginError(q("error=xxx"))).toBe("Sign-in failed.");
  });
});

describe("change password form", () => {
  const ok = { current: "old password 1", next: "new password 1", confirm: "new password 1" };
  it("is ready when everything is filled in and matches", () => {
    expect(passwordProblem(ok)).toBeNull();
  });
  it("says what's missing or wrong", () => {
    expect(passwordProblem({ ...ok, current: "" })).toMatch(/current password/);
    expect(passwordProblem({ ...ok, next: "", confirm: "" })).toMatch(/new password/i);
    expect(passwordProblem({ ...ok, next: "short", confirm: "short" })).toMatch(/at least 10/);
    expect(passwordProblem({ ...ok, next: "old password 1", confirm: "old password 1" })).toMatch(/same/);
    expect(passwordProblem({ ...ok, confirm: "new password 2" })).toMatch(/don't match/);
  });
  it("words the server's answers", () => {
    expect(passwordChangeError(403, "the current password is wrong")).toBe("Your current password is wrong.");
    expect(passwordChangeError(429, "too many attempts; try again in an hour")).toMatch(/Too many attempts/);
    expect(passwordChangeError(400, "your account signs in with single sign-on and has no password here"))
      .toBe("Your account signs in with single sign-on and has no password here.");
    expect(passwordChangeError(400, "password too short.")).toBe("Password too short.");
    expect(passwordChangeError(500, "")).toMatch(/couldn't be changed/);
  });
});

describe("SSO client secret", () => {
  const saved = { issuer: "https://idp.example.com/realms/acme", discovery_url: "", client_id: "pulse", has_client_secret: true };
  it("keeps the stored secret while the provider stays the same", () => {
    expect(ssoSecretNeeded(saved, { ...saved })).toBe(false);
    expect(ssoSecretNeeded(saved, { ...saved, issuer: "https://idp.example.com/realms/acme/ " })).toBe(false);
  });
  it("needs it again when the issuer, discovery URL or client ID changed", () => {
    expect(ssoSecretNeeded(saved, { ...saved, issuer: "https://evil.example.com" })).toBe(true);
    expect(ssoSecretNeeded(saved, { ...saved, discovery_url: "http://keycloak:8080/realms/acme" })).toBe(true);
    expect(ssoSecretNeeded(saved, { ...saved, client_id: "pulse2" })).toBe(true);
  });
  it("needs one when none is stored", () => {
    expect(ssoSecretNeeded({ ...saved, has_client_secret: false }, saved)).toBe(true);
    expect(ssoSecretNeeded(undefined, saved)).toBe(true);
  });
  it("compares URLs like the server", () => {
    expect(sameURL(" https://a.example/ ", "https://a.example")).toBe(true);
    expect(sameURL("https://a.example//", "https://a.example")).toBe(true);
    expect(sameURL("https://a.example/x", "https://a.example")).toBe(false);
  });
});
