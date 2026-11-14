// "Paste from your API's docs": turns the curl (or Postman CLI) examples a gateway documents into
// the custom gateway's settings, so nobody has to map method, address, headers, body and sign-in by
// hand. Pure functions; the form applies the result.
import type { AuthConfig, CustomRequest, Header, LoginRequest } from "./providerAuth";

export type ParsedRequest = { method: string; url: string; headers: Header[]; body: string };

/** shellWords splits a command line the way a POSIX shell would for these examples: single and
 * double quotes, backslash escapes outside single quotes, and backslash-newline continuations. */
export function shellWords(cmd: string): string[] {
  const out: string[] = [];
  let cur = "", inWord = false, q: "" | "'" | '"' = "";
  const s = cmd.replace(/\\\r?\n/g, " ");
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (q === "'") { if (c === "'") q = ""; else cur += c; continue; }
    if (q === '"') {
      if (c === '"') q = "";
      else if (c === "\\" && i + 1 < s.length && '"\\$`'.includes(s[i + 1])) cur += s[++i];
      else cur += c;
      continue;
    }
    if (c === "'" || c === '"') { q = c; inWord = true; continue; }
    if (c === "\\" && i + 1 < s.length) { cur += s[++i]; inWord = true; continue; }
    if (/\s/.test(c)) { if (inWord) { out.push(cur); cur = ""; inWord = false; } continue; }
    cur += c; inWord = true;
  }
  if (inWord) out.push(cur);
  return out;
}

const DATA_FLAGS = new Set(["-d", "--data", "--data-raw", "--data-binary", "--data-ascii", "--body", "--json"]);
const SKIP_WITH_VALUE = new Set(["-u", "--user", "-o", "--output", "-A", "--user-agent", "-e", "--referer", "--connect-timeout", "-m", "--max-time"]);

/** parseCommand reads one `curl …` or `postman request …` example; null when it isn't one. */
export function parseCommand(cmd: string): ParsedRequest | null {
  const w = shellWords(cmd.trim());
  let i = 0;
  let method = "";
  if (w[0] === "curl") i = 1;
  else if (w[0] === "postman" && w[1] === "request") {
    i = 2;
    if (w[i] && /^[A-Z]+$/.test(w[i])) method = w[i++];
  } else return null;
  let url = "", body = "";
  const headers: Header[] = [];
  let json = false;
  for (; i < w.length; i++) {
    const a = w[i];
    if (a === "-X" || a === "--request") { method = (w[++i] ?? "").toUpperCase(); continue; }
    if (a === "-H" || a === "--header") {
      const [name, ...rest] = (w[++i] ?? "").split(":");
      if (name?.trim()) headers.push({ name: name.trim(), value: rest.join(":").trim() });
      continue;
    }
    if (DATA_FLAGS.has(a)) { body = w[++i] ?? ""; if (a === "--json") json = true; continue; }
    if (a === "--url") { url = w[++i] ?? ""; continue; }
    if (SKIP_WITH_VALUE.has(a)) { i++; continue; }
    if (a.startsWith("-")) continue; // --location, -s, --compressed…
    if (!url) url = a;
  }
  if (!url) return null;
  if (json && !headers.some((h) => h.name.toLowerCase() === "content-type")) headers.push({ name: "Content-Type", value: "application/json" });
  return { method: method || (body ? "POST" : "GET"), url, headers, body };
}

const isHeader = (h: Header, name: string) => h.name.toLowerCase() === name;
const contentTypeOf = (r: ParsedRequest) => (r.headers.find((h) => isHeader(h, "content-type"))?.value.split(";")[0].trim() || "application/json");

// Body keys whose sample values become placeholders.
const TO_KEYS = /^(to|phone|phone_?no|phone_?number|mobile|msisdn|destination|recipient|number|receiver|dest)$/i;
const TEXT_KEYS = /^(text|message|msg|body|content|sms)$/i;
const USER_KEYS = /^(user(name)?|login|email|client_?id|account)$/i;
const PASS_KEYS = /^(pass(word)?|pwd|secret|client_?secret)$/i;

/** templateJSON replaces sample values by placeholders for the keys that clearly mean them, at any
 * depth; it reports which secrets it named and their pasted values. */
function templateJSON(body: string, map: (key: string, v: unknown) => string | null): string {
  let v: unknown;
  try { v = JSON.parse(body); } catch { return body; }
  const walk = (x: unknown): unknown => {
    if (Array.isArray(x)) return x.map(walk);
    if (x && typeof x === "object") {
      return Object.fromEntries(Object.entries(x).map(([k, val]) => {
        if (typeof val === "string" || typeof val === "number") { const t = map(k, val); if (t != null) return [k, t]; }
        return [k, walk(val)];
      }));
    }
    return x;
  };
  return JSON.stringify(walk(v), null, 2);
}

/** findTokenPath: the JSON path of the token in an example login answer (JWTAUTH, access_token,
 * data.token…): a string under a key that names a token, preferring the usual names, then length. */
export function findTokenPath(answer: string): string {
  let v: unknown;
  try { v = JSON.parse(answer); } catch { return ""; }
  let best = { path: "", len: 0 };
  const walk = (x: unknown, path: string) => {
    if (!x || typeof x !== "object" || Array.isArray(x)) return;
    for (const [k, val] of Object.entries(x)) {
      const p = path ? `${path}.${k}` : k;
      // Token-ish keys, not token_type / expires_in / refresh_token; the clearest names win, then length.
      if (typeof val === "string" && /token|jwt|auth|access|session/i.test(k) && !/type|expir|refresh|scope/i.test(k)) {
        const score = val.length + (/^(access_?token|token|jwt\w*|auth_?token|id_?token|session_?token)$/i.test(k) ? 100_000 : 0);
        if (score > best.len) best = { path: p, len: score };
      } else walk(val, p);
    }
  };
  walk(v, "");
  return best.path;
}

export type Imported = {
  request: Partial<CustomRequest>; auth: AuthConfig;
  secrets: Record<string, string>; notes: string[];
};

/** importExamples builds the custom request (and its sign-in) from the send example, an optional
 * login example and an optional example of the login's answer. Secrets pasted in the examples
 * (passwords, keys, tokens) move to secret fields; the request keeps placeholders. */
export function importExamples(send: string, login = "", loginAnswer = ""): Imported | { error: string } {
  const s = parseCommand(send);
  if (!s) return { error: "The send example needs to be a curl or postman request command with an address." };
  const notes: string[] = [];
  const secrets: Record<string, string> = {};
  let auth: AuthConfig = { type: "none" };
  const authz = s.headers.find((h) => isHeader(h, "authorization"));
  const keyHeader = s.headers.find((h) => /^(x-)?api[-_]?key$|^apikey$|^x-auth-token$|^key$/i.test(h.name));
  const scheme = authz?.value.split(/\s+/)[0] ?? "";

  if (login.trim()) {
    const l = parseCommand(login);
    if (!l) return { error: "The login example needs to be a curl or postman request command with an address." };
    const body = templateJSON(l.body, (k, v) => {
      if (USER_KEYS.test(k)) { secrets.username = String(v); return "{{secrets.username}}"; }
      if (PASS_KEYS.test(k)) { secrets.password = String(v); return "{{secrets.password}}"; }
      return null;
    });
    const tokenPath = findTokenPath(loginAnswer) || "access_token";
    if (!findTokenPath(loginAnswer)) notes.push("Pulse guessed the token is at access_token in the login answer; paste an example answer, or use Test connection, to check.");
    const lr: LoginRequest = {
      method: l.method, url: l.url, content_type: contentTypeOf(l), body,
      headers: l.headers.filter((h) => !isHeader(h, "content-type")),
      token_from: "json", token_path: tokenPath, expiry_from: "none",
    };
    auth = { type: "login", login: lr, inject: { in: "header", name: authz?.name || "Authorization", template: scheme && scheme !== authz?.value ? `${scheme} {{token}}` : "Bearer {{token}}" } };
    if (Object.keys(secrets).length === 0) notes.push("No username or password field was found in the login example; put {{secrets.name}} where each secret goes.");
  } else if (authz && /^bearer$/i.test(scheme)) {
    auth = { type: "bearer" };
    secrets.auth_token = authz.value.slice(scheme.length).trim();
  } else if (authz && /^basic$/i.test(scheme)) {
    try {
      const [u, ...p] = atob(authz.value.slice(scheme.length).trim()).split(":");
      auth = { type: "basic" }; secrets.auth_username = u; secrets.auth_password = p.join(":");
    } catch { notes.push("The Authorization header looks like basic auth but couldn't be read; choose the sign-in by hand."); }
  } else if (keyHeader) {
    auth = { type: "api_key", key_in: "header", key_name: keyHeader.name };
    secrets.auth_api_key = keyHeader.value;
  } else if (authz) {
    notes.push(`The Authorization header (${scheme}) isn't one Pulse recognises; choose how Pulse signs in by hand.`);
  }

  const signIn = new Set(["authorization", "content-type", "content-length", (keyHeader?.name ?? "").toLowerCase()]);
  let to = false, text = false;
  const body = templateJSON(s.body, (k) => {
    if (TO_KEYS.test(k)) { to = true; return "{{message.to}}"; }
    if (TEXT_KEYS.test(k)) { text = true; return "{{message.text}}"; }
    return null;
  });
  if (!to) notes.push("Put {{message.to}} where the phone number or address goes (Insert: Phone or address).");
  if (!text && !/template/i.test(s.body)) notes.push("Put {{message.text}} where the message goes, if the gateway takes free text.");
  if (/template/i.test(s.body)) notes.push("This gateway sends approved templates: replace the template's sample variables with placeholders such as {{contact.first_name}}.");
  return {
    request: { method: s.method, url: s.url, content_type: contentTypeOf(s), body, headers: s.headers.filter((h) => !signIn.has(h.name.toLowerCase())) },
    auth, secrets, notes,
  };
}
