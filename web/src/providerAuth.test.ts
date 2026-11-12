import { describe, expect, it } from "vitest";
import { authSecretFields, endpointHosts, formatStatuses, hostsChanged, newLogin, parseAuth, parseRequest, parseStatuses, secretsUsed, serializeAuth, serializeRequest, cleanLimits, limitsNote, sameLimits } from "./providerAuth";

describe("auth config", () => {
  it("round-trips and drops fields the type doesn't use", () => {
    const raw = serializeAuth({ type: "api_key", key_in: "query", key_name: "key", token_url: "https://x" });
    expect(JSON.parse(raw)).toEqual({ type: "api_key", key_in: "query", key_name: "key" });
    expect(parseAuth(raw).key_name).toBe("key");
    expect(serializeAuth({ type: "none", key_name: "x" })).toBe("");
    expect(parseAuth("not json").type).toBe("none");
    expect(parseAuth(undefined).type).toBe("none");
  });
  it("keeps a non-default token placement only", () => {
    expect(JSON.parse(serializeAuth({ type: "bearer", inject: { in: "header" } }))).toEqual({ type: "bearer" });
    expect(JSON.parse(serializeAuth({ type: "login", login: newLogin(), inject: { in: "query", name: "access_token" } })).inject).toEqual({ in: "query", name: "access_token" });
  });
  it("lists the secrets a login request names", () => {
    const l = { ...newLogin(), url: "https://a.example.com/{{secrets.tenant}}/login", headers: [{ name: "X-K", value: "{{ secrets.api_key }}" }] };
    expect(secretsUsed(l)).toEqual(["api_key", "password", "tenant", "username"]);
    expect(authSecretFields({ type: "login", login: l }).map((f) => f.key)).toEqual(["api_key", "password", "tenant", "username"]);
    expect(authSecretFields({ type: "basic" }).map((f) => f.key)).toEqual(["auth_username", "auth_password"]);
  });
});

describe("custom request", () => {
  it("parses status lines and reports bad ones", () => {
    const { statuses, errors } = parseStatuses("DLVRD = delivered\nREAD=Read\nqueued = sent\n\nfoo");
    expect(statuses).toEqual({ DLVRD: "delivered", READ: "read" });
    expect(errors).toEqual(["queued = sent", "foo"]);
    expect(formatStatuses(statuses)).toBe("DLVRD = delivered\nREAD = read");
  });
  it("drops empty headers and keeps the template", () => {
    const r = parseRequest(serializeRequest({ method: "POST", url: "https://a.example.com/x", headers: [{ name: "", value: "x" }], success: {}, body: "{}" }), "sms");
    expect(r.headers).toBeUndefined();
    expect(r.url).toBe("https://a.example.com/x");
    expect(parseRequest("", "email").body).toContain("message.subject");
  });
});

describe("endpoint hosts", () => {
  it("sees URL settings, the login URL and the request URL", () => {
    const cfg = { url: "https://gw.example.com/a", auth: serializeAuth({ type: "login", login: { ...newLogin(), url: "https://login.example.com/x" } }),
      request: serializeRequest({ method: "POST", url: "https://api.example.com/{{message.to}}", success: {} }), status_callback_url: "https://pulse/x" };
    expect(endpointHosts(cfg)).toEqual(["https://api.example.com", "https://gw.example.com", "https://login.example.com"]);
    expect(hostsChanged(cfg, { ...cfg, url: "https://gw.example.com/other" })).toBe(false);
    expect(hostsChanged(cfg, { ...cfg, url: "https://elsewhere.example.com/a" })).toBe(true);
    expect(hostsChanged({ host: "smtp.a.com" }, { host: "smtp.b.com" })).toBe(true);
  });
});

describe("sending limits", () => {
  it("sends empty limits as 0 and compares them with the saved ones", () => {
    expect(cleanLimits({})).toEqual({ rate_per_second: 0, daily_quota: 0, timezone: "" });
    expect(sameLimits({ rate_per_second: 300, timezone: "Asia/Kolkata" }, { rate_per_second: 300 })).toBe(true);
    expect(sameLimits({ rate_per_second: 300 }, { rate_per_second: 200 })).toBe(false);
    expect(sameLimits({ daily_quota: 10, timezone: "Asia/Kolkata" }, { daily_quota: 10, timezone: "UTC" })).toBe(false);
    expect(sameLimits({}, undefined)).toBe(true);
  });
  it("explains the server's rate and warns when the provider's is higher", () => {
    expect(limitsNote(300, undefined, "email", "Email")).toBeNull();
    const over = limitsNote(300, 200, "email", "Email")!;
    expect(over.warn).toBe(true);
    expect(over.text).toContain("gets 200, not 300");
    expect(over.text).toContain("PULSE_EMAIL_RATE");
    const under = limitsNote(50, 100, "sms", "SMS")!;
    expect(under.warn).toBe(false);
    expect(under.text).toContain("PULSE_SMS_RATE");
  });
});
