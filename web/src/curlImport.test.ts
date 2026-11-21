import { describe, expect, it } from "vitest";
import { findTokenPath, importExamples, parseCommand, shellWords } from "./curlImport";

// An RCS gateway API, as its docs and Postman show it.
const LOGIN = `postman request POST 'https://api.sms-gateway.example/auth/v1/login/' \\
  --header 'Content-Type: application/json' \\
  --body '{
    "username": "xx",
    "password": "xxx@"
}'`;
const ANSWER = `{"JWTAUTH": "eyJhbGciOi.payload.sig", "user_data": {"email": "a@b.c", "first_name": "x", "is_active": true}}`;
const SEND = `curl --location 'https://api.sms-gateway.example/rcs/v1/message' \\
--header 'Authorization: Bearer xxxxxtokenxxxx' \\
--header 'Content-Type: application/json' \\
--data '{
    "type": "template",
    "phone_no": "xxxxx",
    "bot_name": "acme_promo",
    "template": {
        "name": "order_update",
        "variables": {
            "name": "Dewy"
        }
    }
}'`;

describe("pasted examples", () => {
  it("splits shell words with quotes and continuations", () => {
    expect(shellWords(`curl -H "A: \\"x\\"" 'b c' \\\n d`)).toEqual(["curl", "-H", 'A: "x"', "b c", "d"]);
  });
  it("reads curl and postman requests", () => {
    expect(parseCommand(LOGIN)).toMatchObject({ method: "POST", url: "https://api.sms-gateway.example/auth/v1/login/", headers: [{ name: "Content-Type", value: "application/json" }] });
    const s = parseCommand(SEND)!;
    expect(s.method).toBe("POST"); // a body without -X means POST
    expect(s.headers.map((h) => h.name)).toEqual(["Authorization", "Content-Type"]);
    expect(JSON.parse(s.body).bot_name).toBe("acme_promo");
    expect(parseCommand("wget https://x")).toBeNull();
    expect(parseCommand("curl -X GET https://api.example.com/status")).toMatchObject({ method: "GET", body: "" });
  });
  it("finds the token in a login answer", () => {
    expect(findTokenPath(ANSWER)).toBe("JWTAUTH");
    expect(findTokenPath(`{"data": {"access_token": "abc", "token_type": "Bearer"}}`)).toBe("data.access_token");
    expect(findTokenPath("not json")).toBe("");
  });
  it("turns the gateway login-then-send examples into a login sign-in and a templated request", () => {
    const r = importExamples(SEND, LOGIN, ANSWER);
    if ("error" in r) throw new Error(r.error);
    expect(r.auth.type).toBe("login");
    expect(r.auth.login).toMatchObject({ method: "POST", url: "https://api.sms-gateway.example/auth/v1/login/", content_type: "application/json", token_from: "json", token_path: "JWTAUTH" });
    expect(JSON.parse(r.auth.login!.body!)).toEqual({ username: "{{secrets.username}}", password: "{{secrets.password}}" });
    expect(r.auth.inject).toEqual({ in: "header", name: "Authorization", template: "Bearer {{token}}" });
    expect(r.secrets).toEqual({ username: "xx", password: "xxx@" });
    expect(r.request).toMatchObject({ method: "POST", url: "https://api.sms-gateway.example/rcs/v1/message", content_type: "application/json", headers: [] });
    const body = JSON.parse(r.request.body!);
    expect(body.phone_no).toBe("{{message.to}}");
    expect(body.template).toEqual({ name: "order_update", variables: { name: "Dewy" } });
    expect(r.notes.some((n) => n.includes("templates"))).toBe(true);
  });
  it("keeps a fixed token or API key as a secret when there is no login", () => {
    const bearer = importExamples(`curl https://api.x.com/send -H 'Authorization: Bearer t0k' -d '{"to":"1","text":"hi"}'`);
    if ("error" in bearer) throw new Error(bearer.error);
    expect(bearer.auth.type).toBe("bearer");
    expect(bearer.secrets).toEqual({ auth_token: "t0k" });
    expect(JSON.parse(bearer.request.body!)).toEqual({ to: "{{message.to}}", text: "{{message.text}}" });
    const key = importExamples(`curl https://api.x.com/send -H 'X-API-Key: k1' -H 'X-Trace: on' -d '{"mobile":"1","message":"hi"}'`);
    if ("error" in key) throw new Error(key.error);
    expect(key.auth).toEqual({ type: "api_key", key_in: "header", key_name: "X-API-Key" });
    expect(key.request.headers).toEqual([{ name: "X-Trace", value: "on" }]);
    expect(importExamples("hello")).toEqual({ error: expect.stringContaining("curl") });
  });
});
