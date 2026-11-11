import { describe, expect, it } from "vitest";
import { authSecretFields, endpointHosts, formatStatuses, hostsChanged, newLogin, parseAuth, parseRequest, parseStatuses, secretsUsed, serializeAuth, serializeRequest } from "./providerAuth";

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
