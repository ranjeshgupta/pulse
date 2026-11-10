// Typed client for the Pulse API. Requests are same-origin with the session cookie; every
// state-changing request carries the CSRF header the server requires for cookie sessions.

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: {
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
      ...(method !== "GET" ? { "X-Pulse-CSRF": "1" } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) throw new ApiError(res.status, data.error ?? `Request failed (${res.status})`);
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body ?? {}),
  put: <T>(path: string, body: unknown) => request<T>("PUT", path, body),
  del: (path: string) => request<void>("DELETE", path),
};

// ---- models (mirror internal/model) ----

export type Channel = "email" | "sms" | "whatsapp" | "rcs" | "push" | "inbox";
export const CHANNELS: Channel[] = ["email", "sms", "whatsapp", "rcs", "push", "inbox"];
/** Channels sent through a provider; the app inbox is delivered by Pulse itself. */
export const PROVIDER_CHANNELS: Channel[] = ["email", "sms", "whatsapp", "rcs", "push"];
export const CHANNEL_NAMES: Record<Channel, string> = {
  email: "Email", sms: "SMS", whatsapp: "WhatsApp", rcs: "RCS", push: "Push", inbox: "App inbox",
};

export interface Workspace { id: number; name: string; slug: string; site_key: string }
/** A user's role: a built-in one or "role:<id>" for a custom role. */
export type RoleKey = "admin" | "editor" | "viewer" | `role:${number}`;
export interface User { id: number; email: string; name: string; role: RoleKey; sso: boolean; created_at: string; last_login_at?: string }
export type Area = "contacts" | "segments" | "journeys" | "broadcasts" | "content" | "reports" | "channels" | "settings";
export type Level = "none" | "view" | "edit" | "publish";
export const LEVELS: Level[] = ["none", "view", "edit", "publish"];
export type Perms = Partial<Record<Area, Level>>;
export interface Me { workspace: Workspace; role: RoleKey; user?: User; permissions: Perms }
/** can reports whether perms reach level in area (levels include the ones below them). */
export function can(perms: Perms | undefined, area: Area, level: Exclude<Level, "none">): boolean {
  return LEVELS.indexOf(perms?.[area] ?? "none") >= LEVELS.indexOf(level);
}
export interface BuiltinRole { key: "admin" | "editor" | "viewer"; name: string; description: string; permissions: Perms }
export interface CustomRole { id: number; name: string; description: string; permissions: Perms; users: number; updated_at: string }
export interface AuditEntry {
  id: number; at: string; user_id?: number; actor: string; action: string; object_type: string; object_id: string;
  status: number; ip: string; detail?: unknown;
}
export interface Webhook { id: number; name: string; url: string; events: string[]; active: boolean; secret?: string; created_at: string; updated_at: string }
export interface WebhookDelivery { id: number; event_id: string; event_name: string; at: string; attempt: number; status: number; error?: string; duration_ms: number; final: boolean }
export interface Import {
  id: number; filename: string; list_id?: number; status: "uploading" | "queued" | "running" | "done" | "failed" | "cancelled";
  bytes: number; rows_done: number; rows_failed: number; errors: { row: number; error: string }[]; message?: string;
  created_at: string; started_at?: string; finished_at?: string;
}

export interface PropFilter { prop: string; op: string; value?: unknown }

export type Rule =
  | { all: Rule[] }
  | { any: Rule[] }
  | { attr: string; op: string; value?: unknown }
  | { event: string; op: string; count: number; days: number; where?: PropFilter[] }
  | { segment: number; op: "in" | "not_in" }
  | { tag: string; op: "has" | "not_has" };

/** A rule segment, or a static list (kind "static", no rule) whose members are added by hand or import.
 * Public lists are offered on the preference center, with their description. */
export interface Segment { id: number; name: string; kind: "rule" | "static"; rule: Rule | null; has_window: boolean; status: string; built_at?: string; size: number; public: boolean; description: string }

export type AttrType = "string" | "number" | "boolean" | "date";
export interface AttributeDef { key: string; label: string; type: AttrType; pii: boolean; description: string; created_at?: string }
export interface PointRule { id: number; event: string; points: number; created_at: string }
export interface IdentitySettings { secret: string; required: boolean }

export interface Contact { id: number; external_id: string; email: string; attrs: Record<string, unknown>; created_at: string; updated_at: string }
export interface EventRow { id: string; name: string; ts: string; props: string }

export interface ChannelContent {
  text?: string; sender_id?: string;
  template_name?: string; language?: string; body_params?: string[]; header_image_url?: string; preview_text?: string;
  suggestions?: { type: "reply" | "url" | "dial"; text: string; value?: string }[];
  card?: { title: string; description?: string; image_url?: string } | null;
  title?: string; body?: string; image_url?: string; url?: string; data?: Record<string, string>;
  expires_days?: number; category?: string; // inbox
}
export interface Template { id: number; name: string; channel: Channel; subject: string; html: string; content: ChannelContent; builder?: unknown; transactional?: boolean; updated_at: string }

export interface DeliveryRules {
  caps: Partial<Record<Channel, { max: number; hours: number }>>;
  quiet: { enabled: boolean; start: string; end: string; timezone: string; channels: Channel[] };
  /** Replies (SMS, WhatsApp) that opt a contact out of, or back in to, that channel. */
  keywords: { opt_out: string[]; opt_in: string[] };
}

export interface JourneyNode {
  type: string; next?: string; seconds?: number; segment_id?: number; yes?: string; no?: string;
  template_id?: number; fallback_template_id?: number; x?: number; y?: number;
  until?: string; time_of_day?: string; timezone?: string; event?: string; rule?: Rule;
  variants?: { weight: number; next?: string; label?: string }[]; attrs?: Record<string, unknown>; url?: string; body?: string;
  add_tags?: string[]; remove_tags?: string[]; points?: number; list_id?: number; list_action?: "add" | "remove";
}
export interface JourneyGraph {
  trigger: { type: "segment_entered" | "event"; segment_id?: number; event?: string; include_backfill?: boolean; reentry?: "once" | "always" | "after_days"; days?: number };
  goal?: { event: string } | null;
  entry: string;
  nodes: Record<string, JourneyNode>;
}
export interface Journey { id: number; name: string; graph: JourneyGraph; status: "draft" | "active" | "paused"; start_at?: string; end_at?: string | null; stats?: Stats }
export type Stats = { status: Record<string, number>; node: Record<string, number> };

export interface ProviderField { key: string; label: string; placeholder?: string; required: boolean }
export interface ProviderKind { id: string; channel: Channel; name: string; description: string; config: ProviderField[]; secrets: ProviderField[] }
export interface Provider { id: number; channel: Channel; provider: string; name: string; config: Record<string, string>; has_secrets: boolean; is_default: boolean; webhook_url?: string }
export interface SandboxMessage { id: number; channel: Channel; recipient: string; message_id: string; payload: { subject?: string; html?: string; content?: ChannelContent; platform?: string }; created_at: string }

export interface FocusItem {
  id: number; name: string; kind: "modal" | "bar" | "slide"; status: "draft" | "active"; updated_at?: string; start_at?: string; end_at?: string | null;
  content: { title: string; body_html: string; image_url?: string; cta_text?: string; cta_url?: string; background?: string; text_color?: string; button_color?: string; position?: string };
  targeting: { segment_ids?: number[]; exclude_segment_ids?: number[]; url_contains?: string[]; trigger: string; trigger_value?: number; frequency: string };
}
export interface DWCSlot { id: number; slot: string; name: string; default_html: string; variants: { segment_id: number; html: string }[]; status: "draft" | "active"; updated_at?: string; start_at?: string; end_at?: string | null }

export type Repeat = "none" | "daily" | "weekly" | "monthly";
export type ABMetric = "opens" | "clicks" | "manual";
/** A/B test: variants go to test_percent of the audience; with less than 100%, the winner goes to the rest. */
export interface ABTest { variants: { label: string; template_id: number }[]; test_percent: number; metric: ABMetric; wait_hours: number }
export interface Broadcast {
  id: number; name: string; channel: Channel; template_id: number; fallback_template_id?: number; segment_id: number;
  start_at: string; end_at?: string | null; repeat: Repeat; next_run_at?: string | null;
  /** testing: an A/B test is waiting for its winner. */
  status: "draft" | "scheduled" | "running" | "testing" | "completed" | "cancelled"; updated_at?: string;
  ab?: ABTest | null; conversion_event?: string; conversion_days?: number;
}
export interface BroadcastRun {
  id: number; scheduled_at: string; started_at: string; finished_at?: string; recipients: number;
  test_recipients: number; winner_due_at?: string; winner?: string; winner_reason?: string; winner_sent_at?: string; winner_recipients: number;
}

/** Unique contacts per outcome. opened/clicked are email only; read is WhatsApp and RCS. */
export interface MessageCounts {
  sent: number; delivered: number; read: number; failed: number; fallback: number; suppressed: number; capped: number;
  deferred: number; opened: number; clicked: number; unsubscribed: number; converted: number;
}
export interface MessageReport {
  totals: MessageCounts;
  by_variant?: Record<string, MessageCounts>; // broadcasts; "winner" is the winner's send to everyone else
  by_run?: Record<string, MessageCounts>; // broadcasts, keyed by run (unix seconds of its scheduled time)
  by_node?: Record<string, MessageCounts>; // journeys, keyed by step id
  by_channel: Record<string, MessageCounts>;
  links: { url: string; contacts: number; clicks: number }[];
  daily: { day: string; kind: "sent" | "opened" | "clicked"; count: number }[];
}
export interface JourneyReport { messages: MessageReport; enrollments: Stats }

export type FormFieldType = "text" | "email" | "tel" | "number" | "textarea" | "select" | "checkbox" | "hidden";
export interface FormField { key: string; label: string; type: FormFieldType; required?: boolean; options?: string[]; default?: string; progressive?: boolean }
export interface FormSettings {
  submit_text?: string; success_message?: string; redirect_url?: string; list_ids?: number[]; tags?: string[];
  consent_text?: string; resubscribe?: Channel[]; description?: string; theme?: string;
}
export interface Form { id: number; name: string; public_key: string; fields: FormField[]; settings: FormSettings; status: "draft" | "active"; updated_at?: string }
export interface FormStats {
  total: number; last_30_days: number; contacts: number;
  recent: { external_id: string; ts: string; props: string }[];
  per_day: { day: string; count: number }[];
}

export interface FunnelStep { event: string; where?: PropFilter[]; label?: string }
export interface Funnel { id: number; name: string; steps: FunnelStep[]; window_days: number; updated_at?: string }
export interface FunnelResult {
  from: string; to: string;
  steps: { event: string; label?: string; contacts: number; of_first: number; of_previous: number; dropped: number }[];
}

export interface SSOConfig { enabled: boolean; issuer: string; discovery_url: string; client_id: string; client_secret?: string; has_client_secret: boolean; auto_create_users: boolean; default_role: RoleKey }

export interface Overview {
  counts: { contacts: number; segments: number; active_journeys: number; templates: number };
  days: number;
  activity?: { events_per_minute: { day: string; count: number }[]; events_per_day: { day: string; count: number }[]; top_events: { name: string; count: number }[]; messages: { name: string; count: number }[] };
}
