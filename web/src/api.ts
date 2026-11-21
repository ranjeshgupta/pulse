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
export interface User { id: number; email: string; name: string; role: RoleKey; sso: boolean; password: boolean; two_factor: boolean; created_at: string; last_login_at?: string }
/** Two-step sign-in (authenticator app) of the signed-in person: GET /v1/account/two-factor. */
export interface TwoFactorStatus { enabled: boolean; enabled_at?: string | null; recovery_codes_left: number; required: boolean; password: boolean; available: boolean }
/** A new authenticator-app secret: the QR code holds uri; secret is for typing in. */
export interface TwoFactorSetup { secret: string; uri: string; issuer: string; account: string }
/** Who must use two-step sign-in (password sign-ins only). */
export interface TwoFactorPolicy { mode: "optional" | "everyone" | "some"; roles: RoleKey[]; users: number[] }
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
export interface Webhook {
  id: number; name: string; url: string; events: string[]; active: boolean; secret?: string; has_secret?: boolean; created_at: string; updated_at: string;
  /** REST hook subscriptions (Zapier, Make) belong to an integration and may narrow to a segment or form. */
  integration_id?: number; segment_id?: number; form_id?: number;
}
export interface WebhookDelivery { id: number; event_id: string; event_name: string; at: string; attempt: number; status: number; error?: string; duration_ms: number; final: boolean; test?: boolean }
export interface Import {
  id: number; filename: string; list_id?: number; status: "uploading" | "queued" | "running" | "done" | "failed" | "cancelled";
  bytes: number; rows_done: number; rows_failed: number; errors: { row: number; error: string }[]; message?: string;
  created_at: string; started_at?: string; finished_at?: string;
}

/** prop is a property name, a dotted path (product.code) or a path through a list (items[].code: any item). */
export interface PropFilter { prop: string; op: string; value?: unknown }

/** Event rules count over `days` (today and the days before), or an absolute range from–to
 * (UTC days, both included; no `to` = up to today). */
export type EventRule = { event: string; op: string; count: number; days?: number; from?: string; to?: string; where?: PropFilter[] };
export type Rule =
  | { all: Rule[] }
  | { any: Rule[] }
  | { none: Rule[] }
  | { attr: string; op: string; value?: unknown }
  | EventRule
  | { segment: number; op: "in" | "not_in" }
  | { tag: string; op: "has" | "not_has" };

/** live: membership follows every event; daily: rebuilt once a day at `at` (HH:MM) in time zone `tz`. */
export interface SegmentRefresh { mode: "live" | "daily"; at?: string; tz?: string }

/** A rule segment, or a static list (kind "static", no rule) whose members are added by hand or import.
 * Public lists are offered on the preference center, with their description. */
export interface Segment {
  id: number; name: string; kind: "rule" | "static"; rule: Rule | null; has_window: boolean; status: string; built_at?: string; size: number; public: boolean; description: string;
  refresh?: SegmentRefresh | null; next_refresh_at?: string | null;
}

/** "Why is this contact in this segment": the rule evaluated for one contact, node by node. */
export interface Explanation {
  kind: "all" | "any" | "none" | "attr" | "event" | "segment" | "tag";
  rule?: Rule; match: boolean;
  /** attr: the contact's value ("" when missing). */
  value?: string;
  /** event: events counted in the window. */
  count?: number;
  /** event: older than 365 days; the periodic sweep decides. */
  sweep?: boolean;
  children?: Explanation[];
}
export interface SegmentExplain { segment: number; name: string; kind: "rule" | "static"; member: boolean; refresh?: SegmentRefresh | null; merged?: boolean; explanation?: Explanation }

/** list: a set of values (has / doesn't have, like tags); enum: one of options. */
export type AttrType = "string" | "number" | "boolean" | "date" | "list" | "enum";
export interface AttrOption { value: string; label?: string }
export interface AttributeDef { key: string; label: string; type: AttrType; pii: boolean; description: string; options?: AttrOption[]; created_at?: string }

// ---- event schemas (Audience → Events) ----

export type SchemaMode = "off" | "warn" | "enforce";
export type PropType = "string" | "number" | "bool" | "date" | "enum" | "list" | "object";
export interface SchemaProp {
  key: string;
  /** Where it is in the event's props when not at the top level: product.code, items[].code. */
  path?: string;
  label?: string; description?: string; type: PropType; required?: boolean;
  /** enum: the allowed values. */
  values?: { value: string; label?: string }[];
  /** Applies only when another property (a key of this schema) has one of these values. */
  depends_on?: { prop: string; values: string[] } | null;
  /** Personal data: hidden in samples and examples, never copied into messages. */
  personal?: boolean;
  /** A contact list field the value is added to (e.g. products). */
  add_to_list?: string;
}
export interface EventSchema { name: string; label: string; description: string; mode: SchemaMode; props: SchemaProp[]; updated_at?: string }
export interface CatalogEntry {
  name: string; label: string; count: number; contacts: number; last_seen?: string;
  schema: boolean; mode?: SchemaMode; props: number;
  /** Events with problems in the last 7 days (kept or refused), and events checked. */
  problems: number; checked: number;
}
export interface Violation { prop: string; code: "missing" | "type" | "enum" | "not_applicable"; message: string }
export interface SchemaChecks {
  days: { day: string; checked: number; bad: number; refused: number }[];
  checked: number; bad: number; refused: number;
  problems: { prop: string; code: string; count: number }[];
  samples: { at: string; event_id: string; external_id: string; violations: Violation[]; props: string }[];
}
export interface DiscoveredProp { path: string; type: string; share: number; examples: string[] }
export interface EventSummary {
  name: string; days: number; volume: { day: string; count: number }[]; properties: DiscoveredProp[]; sampled?: number;
  schema?: EventSchema; checks?: SchemaChecks;
}
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
  text?: string; sender_id?: string; dlt_template_id?: string; dlt_entity_id?: string;
  /** sms, rcs: replace links with short tracked links (…/t/s/…), recorded as sms_clicked / rcs_clicked. */
  track_links?: boolean;
  template_name?: string; language?: string; body_params?: string[]; header_image_url?: string; preview_text?: string;
  suggestions?: { type: "reply" | "url" | "dial"; text: string; value?: string }[];
  card?: { title: string; description?: string; image_url?: string } | null;
  title?: string; body?: string; image_url?: string; url?: string; data?: Record<string, string>;
  expires_days?: number; category?: string; // inbox
  /** email: asset ids attached to every message (10 at most, 10 MB together). */
  attachments?: number[];
  /** email: the optional AMP part (built by the server from amp_blocks when those are set). */
  amp?: string;
  /** email: the visual AMP builder's document (see ampBuilder.ts). */
  amp_blocks?: AmpDoc | null;
}

// ---- M6 AMP email (internal/amp) ----
export type AmpBlockType = "heading" | "text" | "image" | "button" | "carousel" | "accordion" | "gallery" | "poll" | "form";
export interface AmpImage { src: string; alt?: string; link?: string; caption?: string; width?: number; height?: number }
export interface AmpFormField { key: string; label: string; type: "text" | "email" | "number" | "textarea" | "select" | "checkbox"; required?: boolean; options?: string[]; save_to?: string }
export interface AmpBlock {
  id: string; type: AmpBlockType; text?: string; url?: string; image?: AmpImage | null; items?: AmpImage[]; height?: number; autoplay?: number;
  sections?: { title: string; body: string }[]; options?: string[]; fields?: AmpFormField[]; show_results?: boolean; submit_text?: string; success_text?: string;
}
export interface AmpDoc { blocks: AmpBlock[]; style: { background?: string; text?: string; accent?: string; font?: "sans" | "serif" } }
export interface AmpProblem { line: number; message: string; warning?: boolean }
/** POST /v1/templates/amp/check {amp} or {amp_blocks}. */
export interface AmpCheck { amp: string; problems: AmpProblem[]; size: number; max_size: number; preview: string; blocks: string[];
  providers: { name: string; amp: boolean; note?: string }[] }
/** GET /v1/templates/{id}/amp/results */
export interface AmpPollResults { polls: Record<string, { question: string; total: number; items: { label: string; count: number; percent: number }[] }> }
/** missing_values: when a variable has no value and no default, render it empty (default) or skip the contact. */
export interface Template { id: number; name: string; channel: Channel; subject: string; html: string; content: ChannelContent; builder?: unknown; transactional?: boolean; missing_values?: "empty" | "skip";
  /** Written inside a one-time campaign: kept out of template lists until saved as reusable. */
  hidden?: boolean; updated_at: string;
  /** The template in other languages: a contact whose locale matches gets it (exact, then by language). */
  translations?: Translation[] }

/** Translation: a template's text in one locale; empty fields keep the default's. */
export interface Translation { locale: string; subject?: string; html?: string; content?: Partial<ChannelContent> }

export interface DeliveryRules {
  caps: Partial<Record<Channel, { max: number; hours: number }>>;
  quiet: { enabled: boolean; start: string; end: string; timezone: string; channels: Channel[] };
  /** Replies (SMS, WhatsApp) that opt a contact out of, or back in to, that channel. */
  keywords: { opt_out: string[]; opt_in: string[] };
  /** Reply-To address on journey emails, so a "Replies to email" decision can see replies (its domain's inbound parse posts to the Reply webhook). */
  reply_to?: string;
}

/** A send_message rule: conditions on contact.* and event.* (segment property filters), a channel and its template. */
export interface ChannelRule { channel: Channel; template_id?: number; fallback_template_id?: number; provider_id?: number; when?: PropFilter[] }

export interface JourneyNode {
  type: string; next?: string; seconds?: number; segment_id?: number; yes?: string; no?: string;
  template_id?: number; fallback_template_id?: number; provider_id?: number; x?: number; y?: number;
  /** send_* and send_message: the subscription topic (its unsubscribers are skipped). */
  topic?: string;
  until?: string; time_of_day?: string; timezone?: string; event?: string; rule?: Rule;
  variants?: { weight: number; next?: string; label?: string }[]; attrs?: Record<string, unknown>; url?: string; body?: string;
  add_tags?: string[]; remove_tags?: string[]; points?: number; list_id?: number; list_action?: "add" | "remove";
  /** change_stage: the stage's name. */
  stage?: string;
  /** message_decision: the send step whose message it watches, and what it waits for ("replied": email only;
   * with "clicked", url is what the link must contain, empty = any link). */
  step?: string; outcome?: "opened" | "clicked" | "delivered" | "read" | "replied";
  /** split: pick a winning path automatically (an A/B test). */
  winner?: SplitWinner | null;
  /** on_form, if_form_field: the form (absent = any form). */
  form_id?: number;
  /** on_asset: the asset (absent = any asset). */
  asset_id?: number;
  /** on_page: a landing page (instead of url). */
  page_id?: number;
  /** on_device: "desktop" | "mobile" | "tablet", and/or an OS such as "iOS". */
  device_type?: string; os?: string;
  /** on_dwc, push_dwc: the Dynamic Web Content slot; push_dwc: its HTML for this contact, for days (default 30). */
  slot?: string; html?: string;
  /** if_visited: 0 = ever, else within the last N days; push_dwc: how long the content stays. */
  days?: number;
  /** check: which preset built the rule (a UI hint; the rule is the truth). */
  preset?: "field" | "tags" | "stage" | "points" | "owner" | "device";
  /** if_journey: the journeys and the state the contact must be in. */
  journey_ids?: number[]; journey_state?: "active" | "completed" | "entered";
  /** if_form_field: the field, comparison and value. */
  field?: string; op?: string; value?: string;
  /** if_notification. */
  notify?: "push" | "inbox" | "any";
  /** if_valid_email. */
  reject_disposable?: boolean;
  /** if_dnc ("any" = everything), dnc_add / dnc_remove ("all" = every channel). */
  channel?: string;
  /** dnc_add: why (shown on the contact). */
  reason?: string;
  /** notify_user: who gets the internal email (template_id is an email template). */
  user_ids?: number[]; to_owner?: boolean; emails?: string[];
  /** send_message: rules in order; the first whose conditions (when) match and whose channel the contact can
   * receive sends. A rule without conditions always matches ("otherwise"). */
  channels?: ChannelRule[];
  /** show_focus. */
  focus_id?: number;
  /** update_owner: 0 = unassign. */
  owner_id?: number;
  /** update_company: attributes set on the contact's company. */
  company_attrs?: Record<string, string | number | boolean>;
  /** add_company. */
  company_id?: number;
  /** update_list (new form): static lists to add to and remove from. */
  add_lists?: number[]; remove_lists?: number[];
  /** change_journey: journeys to add the contact to / remove them from. */
  add_journeys?: number[]; remove_journeys?: number[];
  /** jump: the step to continue from. */
  target?: string;
  /** delete_contact: must be "DELETE". */
  confirm?: string;
  /** push_integration: the CRM integration the contact is pushed to. */
  integration_id?: number;
  /** send steps and send_message: drop the message instead of sending it late. */
  deadline?: Deadline | null;
}
/** A split's A/B test: after after_seconds, once every path has min_per_variant contacts, the best rate of metric wins. */
export interface SplitWinner { metric: "opened" | "clicked" | "converted"; after_seconds: number; min_per_variant: number }
export interface PathStat { path: number; entrants: number; hits: number; rate: number }
/** A split's recorded winner: every contact after decided_at takes path. */
export interface SplitDecision { path: number; next: string; label?: string; metric: string; decided_at: string; stats: PathStat[] }
export interface JourneyGraph {
  trigger: {
    type: "segment_entered" | "event" | "form_submitted"; segment_id?: number; event?: string; form_id?: number; include_backfill?: boolean; reentry?: "once" | "always" | "after_days"; days?: number;
    /** event triggers: only events whose properties pass every filter enroll. */
    where?: PropFilter[];
  };
  goal?: { event: string } | null;
  entry: string;
  nodes: Record<string, JourneyNode>;
}
export interface Journey {
  id: number; name: string; graph: JourneyGraph; status: "draft" | "active" | "paused"; start_at?: string; end_at?: string | null; stats?: Stats;
  /** Set when it's live but its saved steps no longer pass the checks, so nobody moves through it. */
  problem?: string;
  /** Automatic A/B winners by split step id. */
  split_winners?: Record<string, SplitDecision>;
  /** Archived journeys are hidden from the list; their enrollments and reports are kept. */
  archived_at?: string | null;
  /** Someone has entered it (list only): it can be archived but not deleted. */
  has_run?: boolean;
  tags?: string[];
}
export type Stats = { status: Record<string, number>; node: Record<string, number> };

export interface ProviderField {
  key: string; label: string; placeholder?: string; required: boolean; default?: string; hint?: string; options?: string[]; option_labels?: Record<string, string>;
  /** "" (text), "bool" (a switch), "number" or "textarea". */
  type?: "" | "bool" | "number" | "textarea";
  /** Folded under "More settings". */
  advanced?: boolean;
  /** Shown only while config[key] is one of values ("" = left empty). */
  show_if?: { key: string; values: string[] };
}
export interface ProviderPreset { name: string; values: Record<string, string>; note?: string }
export interface ProviderKind {
  id: string; channel: Channel; name: string; description: string; config: ProviderField[]; secrets: ProviderField[];
  notes?: string; default_limits?: ProviderLimits; auth?: boolean; custom_request?: boolean;
  /** A short numbered how-to shown at the top of the form. */
  guide?: string[];
  /** One-click settings for well-known services. */
  presets?: ProviderPreset[];
}
/** Per-provider sending limits: messages per second and a daily quota whose day runs in timezone (0 = none). */
export interface ProviderLimits { rate_per_second?: number; daily_quota?: number; timezone?: string }
export interface Provider {
  id: number; channel: Channel; provider: string; name: string; config: Record<string, string>; has_secrets: boolean; is_default: boolean; webhook_url?: string;
  limits?: ProviderLimits; usage_today?: number;
}
/** A saved test contact: an existing contact, or bare addresses per channel. */
export interface TestContact { id: number; name: string; external_id?: string; email?: string; phone?: string; push_token?: string; created_at?: string }
export interface SandboxMessage { id: number; channel: Channel; recipient: string; message_id: string; payload: { subject?: string; html?: string; content?: ChannelContent; platform?: string }; created_at: string }

export interface FocusItem {
  id: number; name: string; kind: "modal" | "bar" | "slide"; status: "draft" | "active"; updated_at?: string; start_at?: string; end_at?: string | null;
  content: { title: string; body_html: string; image_url?: string; cta_text?: string; cta_url?: string; background?: string; text_color?: string; button_color?: string; position?: string };
  targeting: { segment_ids?: number[]; exclude_segment_ids?: number[]; url_contains?: string[]; trigger: string; trigger_value?: number; frequency: string;
    /** M6: more audience rules, and the order among items of one kind (lower first). */
    url_not_contains?: string[]; devices?: Device[]; conditions?: OnsiteCondition[]; priority?: number };
}
// ---- M6 on-site audiences (internal/onsite) ----
export type Device = "desktop" | "mobile" | "tablet";
/** A contact-field rule (segment property filter: prop path, op, value). Matches verified visitors only. */
export interface OnsiteCondition { prop: string; op: string; value?: unknown }
export interface OnsiteAudience { segment_ids?: number[]; exclude_segment_ids?: number[]; url_contains?: string[]; url_not_contains?: string[]; devices?: Device[]; conditions?: OnsiteCondition[] }
/** Variants are tried in order; the first whose audience matches is shown, else default_html. segment_id is pre-M6 (folded into audience on save). */
/** App slot content drawn natively by the app SDKs. */
export interface AppCard { title?: string; body?: string; image_url?: string; button_text?: string; deep_link?: string; data?: Record<string, string> }
export type SlotPlatform = "web" | "app";
export interface DWCVariant { id?: string; name?: string; segment_id?: number; audience: OnsiteAudience; html: string; card?: AppCard | null }
export interface DWCShadow { index: number; by: number; note: string }
export interface DWCSlot { id: number; slot: string; name: string; platform?: SlotPlatform; default_card?: AppCard | null; default_html: string; variants: DWCVariant[]; status: "draft" | "active"; updated_at?: string; start_at?: string; end_at?: string | null;
  /** GET only: variants that can never be shown, and whether an "everyone" variant hides the default. */
  shadowed?: DWCShadow[]; default_never_shown?: boolean }
/** POST /v1/dwc/overlap {slot_id} | {variants}; POST /v1/focus-items/overlap {kind} | {ids}. */
export interface Overlap {
  variants: { index: number; matching: number; sees: number; approximate?: boolean }[];
  pairs: { a: number; b: number; both: number }[]; multi: number; default: number; contacts: number; approximate: boolean; at: string;
  names: string[]; shadowed: DWCShadow[]; default_never_shown: boolean; cached: boolean;
}

export type Repeat = "none" | "daily" | "weekly" | "monthly";
export type ABMetric = "opens" | "clicks" | "manual";
/** A/B test: variants go to test_percent of the audience; with less than 100%, the winner goes to the rest. */
export interface ABTest { variants: { label: string; template_id: number }[]; test_percent: number; metric: ABMetric; wait_hours: number }
export interface Broadcast {
  id: number; name: string; channel: Channel; template_id: number; fallback_template_id?: number; provider_id?: number; segment_id: number;
  /** The subscription topic: its unsubscribers are skipped, and its unsubscribe link leaves the topic only. */
  topic?: string;
  /** At most this many messages an hour (0: as fast as possible). */
  send_per_hour?: number;
  /** The campaign's control group: this share of the audience is kept back to measure the uplift. */
  holdout_percent?: number;
  /** Send each message at its contact's best hour (from their opens, clicks and reads), within a day. */
  best_time?: boolean;
  /** List its sends in the workspace's public archive (/p/{workspace}/archive). */
  archive_public?: boolean;
  /** RSS-to-email: each send lists what's new in this feed as {{block.feed}}; nothing new skips the send. */
  feed_url?: string;
  start_at: string; end_at?: string | null; repeat: Repeat; next_run_at?: string | null;
  /** testing: an A/B test is waiting for its winner. */
  status: "draft" | "scheduled" | "running" | "testing" | "completed" | "cancelled"; updated_at?: string;
  ab?: ABTest | null; conversion_event?: string; conversion_days?: number;
  /** Members of any of these segments get nothing. */
  exclude_segment_ids?: number[];
  /** Send at start_at's clock time in each contact's time zone (one-time only); timezone: the zone it was chosen in. */
  local_time?: boolean; timezone?: string;
  /** Members of these segments get it too (the audience is the union with segment_id). */
  include_segment_ids?: number[];
  /** Don't send late: after a time of day and/or more than some hours after the send started. */
  deadline?: Deadline | null;
  tags?: string[];
}
export interface Deadline { at?: string; hours?: number; timezone?: string }
export interface BroadcastRun {
  id: number; scheduled_at: string; started_at: string; finished_at?: string; recipients: number;
  test_recipients: number; winner_due_at?: string; winner?: string; winner_reason?: string; winner_sent_at?: string; winner_recipients: number;
}

/** Unique contacts per outcome. opened/clicked are email only; read is WhatsApp and RCS. */
export interface MessageCounts {
  sent: number; delivered: number; read: number; failed: number; fallback: number; suppressed: number; capped: number;
  deferred: number; opened: number; clicked: number; unsubscribed: number; converted: number;
  /** M4: email bounces and spam complaints linked to the message; clicks on file links (also in clicked). */
  bounced: number; complained: number; downloaded: number;
}
export interface MessageReport {
  totals: MessageCounts;
  /** The campaign's control group and how many of them converted anyway (with a conversion event). */
  holdout?: { contacts: number; converted: number };
  by_variant?: Record<string, MessageCounts>; // broadcasts; "winner" is the winner's send to everyone else
  by_run?: Record<string, MessageCounts>; // broadcasts, keyed by run (unix seconds of its scheduled time)
  by_node?: Record<string, MessageCounts>; // journeys, keyed by step id
  by_channel: Record<string, MessageCounts>;
  links: { url: string; contacts: number; clicks: number }[];
  daily: { day: string; kind: "sent" | "opened" | "clicked"; count: number }[];
  /** Revenue events recipients did within `days` of their first message (only when there are any). */
  revenue?: RevenueCounts & { by_variant?: Record<string, RevenueCounts>; event: string; currency: string; days: number };
  /** Contacts whose message was dropped by the send deadline (also counted in failed). */
  late?: number;
  /** M4: contacts per outcome reason, overall and per journey step; contacts with any outcome. */
  reasons: ReasonCount[];
  reasons_by_node?: Record<string, ReasonCount[]>;
  targeted: number;
  funnel: MessageFunnel;
}

// ---- M4: delivery truth and dashboards ----

export type ReasonKind = "failed" | "bounced" | "complained" | "suppressed" | "capped";
export type SkipGroup = "consent" | "reachability" | "cap" | "missing_value" | "deadline";
/** Contacts whose message ended for one normalised reason (package reasons in the server). */
export interface ReasonCount { kind: ReasonKind; code: string; label: string; group?: SkipGroup; contacts: number }
/** A broadcast's audience when its runs started (summed over runs). */
export interface AudienceAtSend { audience: number; after_exclude: number; after_consent: number; reachable: number; runs: number; ready: boolean }
/** A campaign's path from audience to engagement. */
export interface MessageFunnel {
  audience?: AudienceAtSend; // broadcasts
  enrolled?: number; // journeys
  targeted: number;
  skipped: Record<SkipGroup, number>;
  sent: number; delivered: number; opened: number; read: number; clicked: number; downloaded: number; converted: number;
  bounced: number; hard_bounced: number; soft_bounced: number; complained: number; failed: number; unsubscribed: number;
}

/** GET /v1/analytics/users */
export interface UsersDay { day: string; dau: number; wau: number; mau: number; new: number; returning: number; stickiness: number }
export interface KeyCount { key: string; count: number }
export interface UsersReport {
  days: UsersDay[]; active: number; dau: number; wau: number; mau: number; stickiness: number; new: number; returning: number;
  by_platform: KeyCount[]; by_source: KeyCount[];
}
export interface UsersDashboard { from: string; to: string; report: UsersReport; computed_at: string }

/** GET /v1/analytics/campaigns */
export interface CampaignDashRow extends CampaignRowBase {
  counts: MessageCounts; reasons: ReasonCount[]; revenue: number; buyers: number;
}
export interface CampaignRowBase {
  kind: "broadcast" | "journey"; id: number; name: string; category: "one_time" | "trigger" | "automation"; channels: string[];
  status: string; tags: string[]; audience: number | null; sent: number; last_activity?: string;
}
export interface CampaignsDashboard {
  from: string; to: string; campaigns: CampaignDashRow[]; reasons: ReasonCount[]; revenue_event: string; currency: string;
  window_days: number; delivery_reports: Record<string, boolean>; computed_at: string;
}

/** GET /v1/analytics/products */
export interface ProductRow {
  code: string; category?: string; views: number; viewers: number; carts: number; carters: number; purchases: number; buyers: number;
  revenue: number; abandoned: number;
}
export interface ProductsDashboard {
  from: string; to: string; currency: string; computed_at: string;
  events: { view: string; cart: string; purchase: string; amount: string };
  report: { totals: ProductRow; products: ProductRow[]; categories: ProductRow[]; days: { day: string; views: number; carts: number; purchases: number; revenue: number }[] };
}

/** GET /v1/live/events */
export interface LiveEvent { cursor: string; id: string; name: string; ts: string; external_id: string; contact_id: number; props?: Record<string, unknown>; truncated?: boolean }
export interface LiveFeed { events: LiveEvent[]; cursor: string; max_per_second: number }

/** GET /v1/contacts/{id}/profile */
export interface ChannelReach { channel: string; address?: string; has_address: boolean; opted_out: boolean; bounced: boolean; reachable: boolean; why?: string }
export interface ContactProfile {
  contact: Contact;
  devices: { platform: string; token: string }[];
  reach: ChannelReach[];
  products?: unknown;
  journeys: { journey_id: number; name: string; status: string; step: string; step_type: string; current: boolean; entered_at: string; updated_at: string }[];
  activity?: {
    events: Record<"1" | "7" | "30" | "365", number>; messages: Record<"1" | "7" | "30" | "365", number>;
    opens: Record<"1" | "7" | "30" | "365", number>; clicks: Record<"1" | "7" | "30" | "365", number>;
    top_events: { name: string; count: number }[]; last_active?: string;
  };
  purchases?: { items: { id: string; ts: string; amount: number; product_code?: string; product_category?: string }[]; count: number; total: number; event: string; currency: string };
}
/** GET /v1/contacts/{id}/messages */
export interface ContactMessage {
  message_id: string; channel: string; source: "broadcast" | "journey" | "other"; source_id: number; node?: string; variant?: string;
  template_id: number; at: string; last: string; status: string; kinds: string[]; reason_code?: string; reason_label?: string; reason?: string;
  opened: boolean; clicked: boolean; clicks: number; campaign?: string;
}
export interface RevenueCounts { revenue: number; orders: number; buyers: number }
/** A split step in the journey report: contacts (and, for A/B tests, hits of the metric) per path. */
export interface SplitReport { metric?: string; paths: PathStat[]; decision?: SplitDecision }
/** Contacts per step: entered, and per output (yes / no / v0, v1, …). */
export type StepStats = { entered?: number; yes?: number; no?: number; [output: string]: number | undefined };
export interface JourneyReport { messages: MessageReport; enrollments: Stats; splits?: Record<string, SplitReport>; steps?: Record<string, StepStats> }

export type FormFieldType = "text" | "email" | "tel" | "number" | "textarea" | "select" | "checkbox" | "hidden" | "file" | "page_break";
export interface FormField {
  key: string; label: string; type: FormFieldType; required?: boolean; options?: string[]; default?: string; progressive?: boolean;
  /** file: allowed extensions (empty: every upload type); size limit in MB (1-10, default 5) */
  accept?: string[]; max_mb?: number;
}
export interface FormSettings {
  submit_text?: string; success_message?: string; redirect_url?: string; list_ids?: number[]; tags?: string[];
  consent_text?: string; resubscribe?: Channel[]; description?: string; theme?: string;
  /** Double opt-in: the confirmation email (an email template with {{event.confirm_url}}) and what confirming shows. */
  double_optin?: boolean; confirm_template_id?: number; confirmed_message?: string; confirmed_redirect?: string;
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

export interface SSOConfig { enabled: boolean; issuer: string; discovery_url: string; client_id: string; client_secret?: string; has_client_secret: boolean; auto_create_users: boolean; default_role: RoleKey;
  /** The identity provider only issues verified addresses: ID tokens without email_verified are accepted. */
  idp_verifies_email?: boolean }

export interface Overview {
  counts: { contacts: number; segments: number; active_journeys: number; templates: number };
  days: number;
  activity?: { events_per_minute: { day: string; count: number }[]; events_per_day: { day: string; count: number }[]; top_events: { name: string; count: number }[]; messages: { name: string; count: number }[] };
}

// ---- integrations (Settings → Integrations) ----

export interface IntegrationField { key: string; label: string; placeholder?: string; hint?: string; default?: string; required: boolean }
export type IntegrationCategory = "crm" | "ecommerce" | "automation" | "ads";
/** One remote field ↔ Pulse attribute. dir: both (default), pull (into Pulse) or push (out to the CRM). */
export interface FieldMap { remote: string; attr: string; dir?: "both" | "pull" | "push" }
export interface IntegrationKind {
  id: string; name: string; category: IntegrationCategory; description: string;
  config: IntegrationField[]; secrets: IntegrationField[];
  pull: boolean; push: boolean; webhooks: boolean; audiences: boolean; hooks: boolean;
  default_mapping?: FieldMap[]; remote_id_attr?: string;
}
export interface Integration {
  id: number; kind: string; name: string; config: Record<string, string>;
  /** Masked: never the full secret. */
  secrets: Record<string, string>;
  mapping: FieldMap[]; active: boolean; sync_minutes: number; next_sync_at?: string; cursor?: string;
  status: "new" | "ok" | "error" | string; last_error?: string; last_sync_at?: string; key_hint?: string;
  created_at: string; updated_at: string;
}
export interface IntegrationLogEntry { id: number; at: string; action: string; ok: boolean; count: number; message?: string }
export interface AudienceSync {
  id: number; integration_id: number; segment_id: number; audience_id: string; audience_name: string; active: boolean;
  status: "new" | "syncing" | "ok" | "error" | string; last_error?: string;
  uploaded: number; added: number; removed: number; skipped: number; queued: number;
  last_sync_at?: string; next_full_at: string; created_at: string;
}

// ---- campaigns (M3) ----

export type CampaignCategory = "one_time" | "trigger" | "automation";
/** One line of the Campaigns hub: a broadcast (one-time) or a journey (trigger based, automation). */
export interface CampaignRow {
  kind: "broadcast" | "journey"; id: number; name: string; category: CampaignCategory; channels: Channel[]; status: string; tags: string[];
  /** A live journey the workers skip: why, and what to do. */
  problem?: string;
  repeat?: Repeat; next_run_at?: string | null; start_at: string; end_at?: string | null;
  trigger?: "segment" | "event" | "form"; trigger_event?: string; segment_id?: number;
  /** Size of the audience segment now (broadcasts and segment journeys); null for trigger-based journeys. */
  audience: number | null; sent: number; last_activity?: string;
}
/** An exact audience estimate after each step. */
export interface Estimate {
  audience: number; after_exclude: number; after_consent: number; reachable: number; will_send: number;
  /** Contacts (after the exclusions) unsubscribed from the channel or from everything. */
  opted_out?: number;
  /** Email with the "segment" email policy: opted_out contacts still get it unless a segment leaves them out. */
  unsubscribed_kept?: boolean;
  capped?: { capped: number; approximate: boolean; checked: number }; cap?: { max: number; hours: number };
  ready: boolean; computed_at: string; cached_until: string;
}
export interface SegmentReach { segment: number; size: number; ready: boolean; channels: { channel: Channel; after_consent: number; reachable: number }[] }

// ---- M5 Settings → Automation & limits (GET/PUT /v1/settings/automation) ----

/** Retention in days (0 = forever); null message/web-view days follow event_days. */
export interface AutomationRetention {
  event_days: number;
  message_event_days: number | null;
  web_view_days: number | null;
  event_overrides: Record<string, number> | null;
  anonymous_days: number;
  audit_days: number;
  import_days: number;
  webhook_delivery_days: number;
  integration_log_days: number;
  sandbox_days: number;
  live_feed_size: number;
}
export interface AutomationSettings {
  retention: AutomationRetention;
  retries: { attempts: number; max_backoff_seconds: number };
  schedules: { sweep_minutes: number; reach_rebuild_hour: number; daily_segment_at: string; daily_segment_tz: string };
  messages: { inbox_expiry_days: number; control_percent?: number };
  default_limits: Record<string, ProviderLimits> | null;
  /** Bounce rules per channel (email, sms, whatsapp, rcs). */
  bounces?: Record<string, ChannelBounces>;
  /** The unsubscribe link's mode, whether email unsubscribes stop email, and the topics. */
  unsubscribe?: UnsubscribeSettings;
  /** The keys that identify a person, in priority order (Settings → Identity). */
  identity?: { keys: IdentityKey[] };
  /** four_eyes: every campaign launch needs someone else's approval. */
  approvals?: { four_eyes: boolean };
}
export type IdentityMode = "merge" | "suggest";
/** An identity key: external_id, email, phone or a contact field. */
export interface IdentityKey { field: string; mode: IdentityMode }
/** How far an identity key's values are filled in for existing contacts. */
export interface IdentityBackfill { field: string; next_id: number; done_at?: string; contacts: number }
export type UnsubscribeLinkMode = "confirm" | "one_click";
export type EmailUnsubscribePolicy = "enforce" | "segment";
export interface UnsubscribeTopic { key: string; name: string; description?: string }
export interface UnsubscribeSettings { link_mode: UnsubscribeLinkMode; email_policy: EmailUnsubscribePolicy; topics: UnsubscribeTopic[] }
export type BounceAction = "record" | "stop" | "unsubscribe" | "leave_segments" | "do_not_contact" | "delete";
export interface BounceRule { count: number; action: BounceAction }
export interface ChannelBounces { hard: BounceRule; soft: BounceRule; complaint: BounceRule }
export interface AutomationLimit { min: number; max: number; zero: boolean }
export interface AutomationResponse {
  settings: AutomationSettings;
  defaults: AutomationSettings;
  limits: Record<string, AutomationLimit>;
  server: { sweep_minutes: number; web_view_names: string[]; limit_channels: string[]; max_overrides: number; retention_hour_utc: number; last_retention_run: string | null };
  identity_backfills?: IdentityBackfill[];
}
/** One delete a retention change would run now (POST /v1/settings/automation/preview). */
export interface RetentionPreviewItem {
  key: string;
  store: "clickhouse" | "postgres";
  table: string;
  names?: string[];
  not_in?: boolean;
  days: number;
  cutoff: string;
  old_days: number;
  old_days_by_name?: Record<string, number>;
  rows: number;
  error?: string;
}
export interface RetentionPreview { items: RetentionPreviewItem[]; approximate: boolean; cached: boolean }

/** A reusable content block: {{block.key}} in any template (HTML for email, text elsewhere). */
export interface ContentBlock { id: number; key: string; name: string; html: string; text: string; updated_at?: string }
