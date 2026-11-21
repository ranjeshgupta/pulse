// Custom HTTP authentication and the gateway's custom-request mode, as the provider form edits
// them. Both are stored as JSON strings in the provider's config ("auth" and "request"); secrets
// stay in the provider's secrets and are never shown again.

import { setOwn } from "./safe";

export type AuthType = "none" | "basic" | "bearer" | "api_key" | "oauth2" | "login";

export interface Header { name: string; value: string }

export interface LoginRequest {
  method?: string; url: string; headers?: Header[]; content_type?: string; body?: string;
  token_from: "json" | "header" | "regex"; token_path: string;
  expiry_from?: "" | "none" | "json" | "fixed"; expiry_path?: string; expiry_seconds?: number;
}

export interface Inject { in: "header" | "query" | "body"; name?: string; template?: string }

export interface AuthConfig {
  type: AuthType;
  key_in?: "header" | "query"; key_name?: string;
  token_url?: string; client_id?: string; scope?: string; audience?: string; grant?: string; client_auth?: "body" | "basic";
  login?: LoginRequest;
  inject?: Inject;
}

export const AUTH_TYPES: { id: AuthType; label: string; hint: string }[] = [
  { id: "none", label: "None", hint: "Pulse adds no login details to the request." },
  { id: "basic", label: "Basic", hint: "HTTP basic auth with a user and password." },
  { id: "bearer", label: "Bearer token", hint: "A fixed token in the Authorization header." },
  { id: "api_key", label: "API key", hint: "A key in a header or query parameter." },
  { id: "oauth2", label: "OAuth2 client credentials", hint: "Pulse gets a token from the token URL and refreshes it before it expires." },
  { id: "login", label: "Login request", hint: "Pulse calls your login URL, reads the token from the answer, and logs in again before it expires or after a 401." },
];

const SECRET_RE = /\{\{\s*secrets\.([A-Za-z0-9_-]+)\s*\}\}/g;

export function parseAuth(raw: string | undefined): AuthConfig {
  if (!raw || !raw.trim()) return { type: "none" };
  try {
    const v = JSON.parse(raw) as AuthConfig;
    return v && typeof v === "object" && v.type ? v : { type: "none" };
  } catch {
    return { type: "none" };
  }
}

/** serializeAuth drops what the chosen type doesn't use; "" for none. */
export function serializeAuth(a: AuthConfig): string {
  if (a.type === "none") return "";
  const out: AuthConfig = { type: a.type };
  if (a.type === "api_key") Object.assign(out, { key_in: a.key_in || "header", key_name: a.key_name || undefined });
  if (a.type === "oauth2") Object.assign(out, { token_url: a.token_url, client_id: a.client_id, scope: a.scope || undefined, audience: a.audience || undefined, client_auth: a.client_auth || undefined });
  if (a.type === "login") out.login = a.login;
  if ((a.type === "bearer" || a.type === "oauth2" || a.type === "login") && a.inject && (a.inject.in !== "header" || a.inject.name || a.inject.template)) out.inject = a.inject;
  return JSON.stringify(out);
}

/** secretsUsed lists the {{secrets.x}} a login request names (sorted, unique). */
export function secretsUsed(l?: LoginRequest): string[] {
  if (!l) return [];
  const found = new Set<string>();
  for (const s of [l.url, l.body ?? "", ...(l.headers ?? []).map((h) => h.value)]) {
    for (const m of s.matchAll(SECRET_RE)) found.add(m[1]);
  }
  return [...found].sort();
}

/** authSecretFields are the secret inputs an auth configuration needs. */
export function authSecretFields(a: AuthConfig): { key: string; label: string }[] {
  switch (a.type) {
    case "basic": return [{ key: "auth_username", label: "User" }, { key: "auth_password", label: "Password" }];
    case "bearer": return [{ key: "auth_token", label: "Token" }];
    case "api_key": return [{ key: "auth_api_key", label: "API key" }];
    case "oauth2": return [{ key: "auth_client_secret", label: "Client secret" }];
    case "login": {
      // Usernames before passwords; names read as words ("api_key" → "Api key").
      const rank = (k: string) => (/user|login|email|client_?id/i.test(k) ? 0 : /pass|secret/i.test(k) ? 2 : 1);
      return secretsUsed(a.login).sort((x, y) => rank(x) - rank(y) || x.localeCompare(y))
        .map((k) => ({ key: k, label: k.replace(/[_-]+/g, " ").replace(/^./, (c) => c.toUpperCase()) }));
    }
  }
  return [];
}

export function newLogin(): LoginRequest {
  return { method: "POST", url: "", content_type: "application/json", body: '{"username": "{{secrets.username}}", "password": "{{secrets.password}}"}',
    token_from: "json", token_path: "access_token", expiry_from: "json", expiry_path: "expires_in" };
}

// ---- custom request ----

export interface SuccessRule { status_min?: number; status_max?: number; json_path?: string; json_value?: string }
export type ReceiptStatus = "delivered" | "read" | "failed" | "undeliverable" | "bounced" | "pending";
export interface ReceiptMapping {
  items_path?: string; id_path: string; status_path: string; statuses: Record<string, ReceiptStatus>;
  /** "" when reports only come from status requests (the webhook then accepts nothing). */
  reason_path?: string; code_path?: string; verify: "header" | "query" | "hmac" | ""; verify_name: string;
}
/** FailureRule: an answer whose field holds one of the values is a refusal (unreachable: send the SMS fallback). */
export interface FailureRule { json_path: string; values: string[]; unreachable?: boolean }
/** PollRequest asks the provider for a message's status ("We ask them"). */
export interface PollRequest {
  method: string; url: string; headers?: Header[]; content_type?: string; body?: string; every_minutes?: number; for_hours?: number;
}
export interface CustomRequest {
  method: string; url: string; headers?: Header[]; content_type?: string; body?: string;
  success: SuccessRule; id_path?: string; receipt?: ReceiptMapping;
  failure?: FailureRule; match_by?: "provider" | "pulse"; poll?: PollRequest;
}

export const RECEIPT_STATUSES: ReceiptStatus[] = ["delivered", "read", "failed", "undeliverable", "bounced", "pending"];

/** What each Pulse status means, for the status-mapping table. */
export const RECEIPT_STATUS_LABELS: Record<ReceiptStatus, string> = {
  delivered: "Delivered", read: "Read", failed: "Failed", undeliverable: "Failed: can't get it here (send the SMS fallback)",
  bounced: "Bounced", pending: "Not final yet (ask again)",
};

// Words gateways commonly use for each status (operator DLR codes included), in lower case.
const COMMON_STATUS_WORDS: [ReceiptStatus, string[]][] = [
  ["delivered", ["delivered", "delivrd", "success", "successful", "dlvrd", "deliver"]],
  ["read", ["read", "seen", "opened"]],
  ["undeliverable", ["undeliv", "undelivered", "invalid_number", "not_on_whatsapp", "not_rcs_capable", "unreachable"]],
  ["failed", ["failed", "failure", "rejectd", "rejected", "expired", "error", "dnd", "blocked"]],
  ["bounced", ["bounced", "bounce"]],
  ["pending", ["submitted", "sent", "queued", "accepted", "pending", "enroute", "processing", "acknowledged"]],
];

/** suggestStatuses maps the provider's status words to Pulse's, from common names; unknown words are left out. */
export function suggestStatuses(words: string[]): Record<string, ReceiptStatus> {
  const out: Record<string, ReceiptStatus> = {};
  for (const raw of words) {
    const w = raw.trim();
    if (!w) continue;
    const lc = w.toLowerCase();
    const hit = COMMON_STATUS_WORDS.find(([, list]) => list.includes(lc)) ?? COMMON_STATUS_WORDS.find(([, list]) => list.some((x) => lc.includes(x)));
    if (hit) setOwn(out, w, hit[0]);
  }
  return out;
}

/** statusWordsIn finds the values at a path in a pasted report or answer (JSON, or a list of them). */
export function statusWordsIn(sample: string, path: string): string[] {
  let doc: unknown;
  try { doc = JSON.parse(sample); } catch { return []; }
  const items = Array.isArray(doc) ? doc : [doc];
  const out = new Set<string>();
  const walk = (v: unknown, parts: string[]): void => {
    if (parts.length === 0) {
      if (typeof v === "string" || typeof v === "number") out.add(String(v));
      return;
    }
    if (Array.isArray(v)) { v.forEach((x) => walk(x, parts)); return; }
    if (v && typeof v === "object") walk((v as Record<string, unknown>)[parts[0]], parts.slice(1));
  };
  for (const it of items) walk(it, path.split(".").filter(Boolean));
  return [...out];
}

export function newCustomRequest(channel: string): CustomRequest {
  const body = channel === "email"
    ? '{"to": "{{message.to}}", "subject": "{{message.subject}}", "html": "{{message.html}}"}'
    : `{"to": "{{message.to}}", "text": "{{message.text}}", "name": "{{contact.first_name | default:'there'}}"}`;
  return { method: "POST", url: "https://", content_type: "application/json", body, success: {}, id_path: "id" };
}

export function parseRequest(raw: string | undefined, channel: string): CustomRequest {
  if (!raw || !raw.trim()) return newCustomRequest(channel);
  try {
    const v = JSON.parse(raw) as CustomRequest | null;
    if (!v || typeof v !== "object" || Array.isArray(v)) return newCustomRequest(channel);
    return { ...v, success: v.success ?? {} };
  } catch {
    return newCustomRequest(channel);
  }
}

export function serializeRequest(r: CustomRequest): string {
  const out: CustomRequest = { ...r, headers: (r.headers ?? []).filter((h) => h.name.trim()) };
  if (!out.headers?.length) delete out.headers;
  if (!out.id_path) delete out.id_path;
  // A rule being filled in is kept (the server says what's missing on save); only an empty one goes.
  if (!out.failure?.json_path?.trim() && !out.failure?.values.some((v) => v.trim())) delete out.failure;
  else if (out.failure) out.failure = { ...out.failure, values: out.failure.values.map((v) => v.trim()).filter(Boolean) };
  if (!out.match_by || out.match_by === "provider") delete out.match_by;
  if (out.poll) out.poll = { ...out.poll, headers: (out.poll.headers ?? []).filter((h) => h.name.trim()) };
  return JSON.stringify(out);
}

/** formatStatuses shows a status map as "provider value = pulse status" lines. */
export function formatStatuses(m: Record<string, string> | undefined): string {
  return Object.entries(m ?? {}).map(([k, v]) => `${k} = ${v}`).join("\n");
}

/** parseStatuses reads those lines back; bad lines are reported. */
export function parseStatuses(text: string): { statuses: Record<string, ReceiptStatus>; errors: string[] } {
  const statuses: Record<string, ReceiptStatus> = {};
  const errors: string[] = [];
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const i = line.lastIndexOf("=");
    const k = i > 0 ? line.slice(0, i).trim() : "";
    const v = i > 0 ? line.slice(i + 1).trim().toLowerCase() : "";
    if (!k || !RECEIPT_STATUSES.includes(v as ReceiptStatus)) errors.push(line);
    else setOwn(statuses, k, v as ReceiptStatus);
  }
  return { statuses, errors };
}

/** MESSAGE_VARS are the {{message.x}} a request template can use. */
export const MESSAGE_VARS = ["to", "text", "subject", "html", "title", "body", "id", "channel", "from", "sender_id",
  "dlt_template_id", "dlt_entity_id", "template_name", "language", "params", "image_url", "url", "suggestions", "card", "data", "ref", "provider_id"];

/** urlHost is the scheme://host of a URL ("" when it has none), to warn that changing it clears secrets. */
export function urlHost(u: string | undefined): string {
  const m = /^([a-z][a-z0-9+.-]*):\/\/([^/?#]+)/i.exec((u ?? "").trim());
  return m ? `${m[1]}://${m[2]}`.toLowerCase() : "";
}

/** endpointHosts lists every host a provider's config sends credentials to (mirrors the server). */
export function endpointHosts(config: Record<string, string>): string[] {
  const set = new Set<string>();
  for (const [k, v] of Object.entries(config)) {
    if (k === "status_callback_url") continue;
    if (k === "url" || k.endsWith("_url")) { const h = urlHost(v); if (h) set.add(h); }
    if (k === "host" && v.trim()) set.add("smtp://" + v.trim().toLowerCase());
  }
  const a = parseAuth(config.auth);
  if (a.type === "oauth2" && a.token_url) set.add(urlHost(a.token_url));
  if (a.type === "login" && a.login?.url) set.add(urlHost(a.login.url));
  if (config.request) {
    const r = parseRequest(config.request, "sms");
    for (const u of [r.url, r.poll?.url]) { const h = urlHost(u); if (h) set.add(h); }
  }
  set.delete("");
  return [...set].sort();
}

export function hostsChanged(before: Record<string, string>, after: Record<string, string>): boolean {
  return endpointHosts(before).join(" ") !== endpointHosts(after).join(" ");
}

// ---- sending limits ----

type Limits = { rate_per_second?: number; daily_quota?: number; timezone?: string };

/** cleanLimits: the limits as the API takes them (0 is no limit). */
export function cleanLimits(l: Limits): Required<Limits> {
  return { rate_per_second: l.rate_per_second || 0, daily_quota: l.daily_quota || 0, timezone: l.timezone ?? "" };
}

/** sameLimits: whether the form's limits equal the saved ones (empty and 0 are the same). */
export function sameLimits(a: Limits, b?: Limits): boolean {
  const x = cleanLimits(a), y = cleanLimits(b ?? {});
  return x.rate_per_second === y.rate_per_second && x.daily_quota === y.daily_quota && (!x.daily_quota || (x.timezone || "UTC") === (y.timezone || "UTC"));
}

/** limitsNote explains the server's own per-workspace rate for the channel, and warns when the
 * provider's rate is above it (the server's rate wins). */
export function limitsNote(rate: number | undefined, serverRate: number | undefined, channel: string, channelName: string): { text: string; warn: boolean } | null {
  if (!serverRate) return null;
  // "200 email messages", but "100 SMS messages" and "80 WhatsApp messages".
  if (/^[A-Z][a-z ]+$/.test(channelName)) channelName = channelName.toLowerCase();
  const cap = serverRate.toLocaleString("en-IN");
  if (rate && rate > serverRate) {
    return { warn: true, text: `This Pulse server sends at most ${cap} ${channelName} messages a second per workspace, so this provider gets ${cap}, not ${rate.toLocaleString("en-IN")}. Ask whoever runs Pulse to raise it (${rateSetting(channel)}).` };
  }
  return { warn: false, text: `This Pulse server also sends at most ${cap} ${channelName} messages a second per workspace (${rateSetting(channel)}), whatever the provider allows.` };
}

const rateSetting = (channel: string) => `PULSE_${channel.toUpperCase()}_RATE`;

// ---- provider form fields ----

type ShowField = { show_if?: { key: string; values: string[] } };

/** fieldShown: whether a catalog field applies with these settings (its show_if, if any). */
export function fieldShown(f: ShowField, config: Record<string, string>): boolean {
  if (!f.show_if) return true;
  return f.show_if.values.includes((config[f.show_if.key] ?? "").trim());
}

/** applyPreset: the settings after picking a preset (its values over the current ones). */
export function applyPreset(config: Record<string, string>, values: Record<string, string>): Record<string, string> {
  return { ...config, ...values };
}

/** presetOf: the preset the settings match (every value it sets), if any, to show it as chosen. */
export function presetOf<T extends { values: Record<string, string> }>(presets: T[] | undefined, config: Record<string, string>): T | undefined {
  return presets?.find((p) => Object.entries(p.values).every(([k, v]) => (config[k] ?? "") === v));
}
