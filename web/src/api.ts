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
  /** POSTs JSON and returns the response body as text (HTML previews). */
  postText: async (path: string, body: unknown): Promise<string> => {
    const res = await fetch(path, {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-Pulse-CSRF": "1" }, body: JSON.stringify(body),
    });
    const text = await res.text();
    if (!res.ok) {
      let msg = `Request failed (${res.status})`;
      try { msg = JSON.parse(text).error ?? msg; } catch { /* not JSON */ }
      throw new ApiError(res.status, msg);
    }
    return text;
  },
  /** Uploads multipart form data with progress (0..1); fetch can't report upload progress. */
  upload: <T>(path: string, form: FormData, onProgress?: (fraction: number) => void, signal?: AbortSignal) =>
    new Promise<T>((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", path);
      xhr.withCredentials = true;
      xhr.setRequestHeader("X-Pulse-CSRF", "1");
      xhr.upload.onprogress = (e) => { if (e.lengthComputable) onProgress?.(e.loaded / e.total); };
      xhr.onload = () => {
        let data: { error?: string } = {};
        try { data = xhr.responseText ? JSON.parse(xhr.responseText) : {}; } catch { /* not JSON */ }
        if (xhr.status >= 200 && xhr.status < 300) resolve(data as T);
        else reject(new ApiError(xhr.status, data.error ?? `Upload failed (${xhr.status})`));
      };
      xhr.onerror = () => reject(new ApiError(0, "The upload failed. Check your connection and try again."));
      xhr.onabort = () => reject(new ApiError(0, "Upload cancelled"));
      signal?.addEventListener("abort", () => xhr.abort());
      xhr.send(form);
    }),
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
/** A lifecycle stage, in sort_order. min_points (when set) moves contacts forward to it when their points reach it. */
export interface Stage { id: number; name: string; description: string; sort_order: number; min_points: number | null; contacts: number; created_at: string; updated_at: string }
/** A B2B account. Its attributes are copied to linked contacts as company_<key>. */
export interface Company { id: number; name: string; domain: string; attrs: Record<string, string | number | boolean>; members: number; created_at: string; updated_at: string }
/** Contacts sharing an email address or phone number; contacts holds the first 20 of total, oldest first. */
export interface DuplicateGroup { value: string; total: number; contacts: Contact[] }
export interface EventRow { id: string; name: string; ts: string; props: string }

export interface ChannelContent {
  text?: string; sender_id?: string;
  template_name?: string; language?: string; body_params?: string[]; header_image_url?: string; preview_text?: string;
  suggestions?: { type: "reply" | "url" | "dial"; text: string; value?: string }[];
  card?: { title: string; description?: string; image_url?: string } | null;
  title?: string; body?: string; image_url?: string; url?: string; data?: Record<string, string>;
  expires_days?: number; category?: string; // inbox
  /** email: asset ids attached to every message (10 at most, 10 MB together). */
  attachments?: number[];
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
  /** change_stage: the stage's name. */
  stage?: string;
  /** message_decision: the send step whose message it watches, and what it waits for. */
  step?: string; outcome?: "opened" | "clicked" | "delivered" | "read";
  /** split: pick a winning path automatically (an A/B test). */
  winner?: SplitWinner | null;
}
/** A split's A/B test: after after_seconds, once every path has min_per_variant contacts, the best rate of metric wins. */
export interface SplitWinner { metric: "opened" | "clicked" | "converted"; after_seconds: number; min_per_variant: number }
export interface PathStat { path: number; entrants: number; hits: number; rate: number }
/** A split's recorded winner: every contact after decided_at takes path. */
export interface SplitDecision { path: number; next: string; label?: string; metric: string; decided_at: string; stats: PathStat[] }
export interface JourneyGraph {
  trigger: { type: "segment_entered" | "event"; segment_id?: number; event?: string; include_backfill?: boolean; reentry?: "once" | "always" | "after_days"; days?: number };
  goal?: { event: string } | null;
  entry: string;
  nodes: Record<string, JourneyNode>;
}
export interface Journey {
  id: number; name: string; graph: JourneyGraph; status: "draft" | "active" | "paused"; start_at?: string; end_at?: string | null; stats?: Stats;
  /** Automatic A/B winners by split step id. */
  split_winners?: Record<string, SplitDecision>;
}
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
  /** Revenue events recipients did within `days` of their first message (only when there are any). */
  revenue?: RevenueCounts & { by_variant?: Record<string, RevenueCounts>; event: string; currency: string; days: number };
}
export interface RevenueCounts { revenue: number; orders: number; buyers: number }
/** A split step in the journey report: contacts (and, for A/B tests, hits of the metric) per path. */
export interface SplitReport { metric?: string; paths: PathStat[]; decision?: SplitDecision }
export interface JourneyReport { messages: MessageReport; enrollments: Stats; splits?: Record<string, SplitReport> }

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

/** An uploaded file: downloaded at /a/{public_key} (behind form_id when set) and attachable to emails. */
export interface Asset {
  id: number; public_key: string; title: string; description: string; filename: string; content_type: string;
  size: number; sha256: string; form_id?: number | null; download_count: number; created_at: string; updated_at: string;
}
export interface AssetList { assets: Asset[]; base_url: string; max_size: number; max_attachment_bytes: number; extensions: string[] }
/** downloads counts every download; known_downloads and contacts count form-gated downloads by known contacts. */
export interface AssetDetail { asset: Asset; url: string; stats: { downloads: number; known_downloads?: number; contacts?: number }; templates: string[] }

/** A landing page, served at {base_url}{slug} while published and inside its schedule. */
export interface LandingPage {
  id: number; slug: string; title: string; meta_description: string; html: string; builder?: unknown;
  status: "draft" | "published"; start_at: string; end_at?: string | null; created_at?: string; updated_at?: string;
}
export interface LandingPageList { pages: { page: LandingPage; live: boolean }[]; base_url: string }
export interface LandingPageDetail { page: LandingPage; url: string; base_url: string; live: boolean }
export interface PageStats {
  views: number; visitors: number; submissions: number;
  per_day: { day: string; views: number; visitors: number; submissions: number }[];
}

export interface FunnelStep { event: string; where?: PropFilter[]; label?: string }
export interface Funnel { id: number; name: string; steps: FunnelStep[]; window_days: number; updated_at?: string }
export interface FunnelResult {
  from: string; to: string;
  steps: { event: string; label?: string; contacts: number; of_first: number; of_previous: number; dropped: number }[];
  /** With a breakdown: contacts reaching each step for the top 10 values of the attribute; other is everyone else. */
  breakdown?: { attr: string; rows: { value: string; reached: number[] }[]; other?: number[] };
}

// ---- analytics (batch 7) ----

/** A saved analytics report (cohort or custom). */
export interface SavedReport<S> { id: number; kind: "cohort" | "custom"; name: string; spec: S; updated_at: string }

export type Period = "day" | "week" | "month";
/** Most periods a cohort report follows, per period. */
export const MAX_PERIODS: Record<Period, number> = { day: 60, week: 26, month: 12 };
export const MAX_COHORTS = 60;
export interface CohortSpec {
  /** first_seen: the contact's first event; event: the first time in the range they did `event`. */
  by: "first_seen" | "event"; event?: string; where?: PropFilter[];
  period: Period; periods: number;
  /** What counts as coming back: an event (with filters), or any activity when event is empty. */
  return: { event?: string; where?: PropFilter[] };
  segment_id?: number;
}
/** returned[i]: contacts of the cohort who came back in period i (0 = the start period); null = not reached yet. */
export interface CohortRow { start: string; size: number; returned: (number | null)[] }
export interface CohortResult { period: Period; periods: number; from: string; to: string; cohorts: CohortRow[]; average: (number | null)[] }

export interface RevenueSettings { event: string; amount_prop: string; currency: string }
export type AttributionModel = "last_touch" | "first_touch" | "linear";
export interface Credit {
  kind: "broadcast" | "journey" | "utm"; id?: number; source?: string; medium?: string; campaign?: string; name?: string;
  revenue: number; conversions: number;
}
export interface Attribution { credits: Credit[]; revenue: number; conversions: number }
export interface RevenueReport {
  settings: RevenueSettings; from: string; to: string; model: AttributionModel; window_days: number;
  totals: { orders: number; revenue: number; buyers: number };
  messages: Attribution; campaigns: Attribution;
}

export type ReportSource = "events" | "messages" | "contacts";
export type ReportMetric = "count" | "unique_contacts" | "sum" | "avg";
export type ReportGroup = "none" | "event" | "prop" | "attr" | "kind" | "channel" | "campaign" | "day" | "week" | "month";
export interface ReportSpec {
  source: ReportSource;
  metric: { type: ReportMetric; key?: string };
  group_by: { type: ReportGroup; key?: string };
  /** events: only these event names (10 at most). */
  events?: string[];
  /** events: property filters; contacts: attribute filters (5 at most). */
  where?: PropFilter[];
  /** messages: broadcast or journey (empty: both), channels and outcomes. */
  message_source?: "" | "broadcast" | "journey"; channels?: Channel[]; kinds?: string[];
  segment_id?: number;
  chart: "table" | "bars" | "line";
  /** Default range: the last N days. */
  range_days?: number;
}
export interface ReportResult { rows: { group: string; value: number }[]; labels: Record<string, string>; from: string; to: string; max_groups: number }

export interface SSOConfig { enabled: boolean; issuer: string; discovery_url: string; client_id: string; client_secret?: string; has_client_secret: boolean; auto_create_users: boolean; default_role: RoleKey }

export interface Overview {
  counts: { contacts: number; segments: number; active_journeys: number; templates: number };
  days: number;
  activity?: { events_per_minute: { day: string; count: number }[]; events_per_day: { day: string; count: number }[]; top_events: { name: string; count: number }[]; messages: { name: string; count: number }[] };
}
