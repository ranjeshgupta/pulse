import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import {
  api, CHANNEL_NAMES, type AutomationResponse, type AutomationRetention, type AutomationSettings, type Channel,
  type ProviderLimits, type RetentionPreview,
} from "../api";
import { useIsAdmin } from "../auth";
import {
  formatDays, formatDuration, overridesToRows, previewLabel, previewTotal, retentionShortened, retrySchedule, rowsToOverrides,
  sameSettings, validate, type OverrideRow,
} from "../automation";
import { ErrorNote, Modal, PageHead, timeAgo, useToast } from "../ui";
import "./automation.css";

// Settings → Automation & limits: every automatic behaviour of the workspace with its current
// value, its default and what it does. Shortening a retention shows what would be deleted now
// (POST /v1/settings/automation/preview) and needs a confirmation before saving.

const HOURS = Array.from({ length: 24 }, (_, h) => h);
const pad = (h: number) => String(h).padStart(2, "0");

export function AutomationSettingsPage() {
  const admin = useIsAdmin();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["automation"], queryFn: () => api.get<AutomationResponse>("/v1/settings/automation") });
  const [draft, setDraft] = useState<AutomationSettings | null>(null);
  const [rows, setRows] = useState<OverrideRow[] | null>(null);
  const [preview, setPreview] = useState<{ result: RetentionPreview; changes: string[] } | null>(null);
  const data = q.data;
  const save = useMutation({
    mutationFn: (s: AutomationSettings) => api.put<{ settings: AutomationSettings }>("/v1/settings/automation", s),
    onSuccess: () => {
      setDraft(null); setRows(null); setPreview(null);
      qc.invalidateQueries({ queryKey: ["automation"] });
      qc.invalidateQueries({ queryKey: ["data-settings"] });
      toast({ text: "Saved. Retention changes apply at this workspace's next daily cleanup." });
    },
  });
  const check = useMutation({
    mutationFn: (s: AutomationSettings) => api.post<RetentionPreview>("/v1/settings/automation/preview", s),
  });
  if (!data) return <div className="page"><ErrorNote error={q.error} /></div>;
  const { defaults: def, limits, server } = data;
  const cur = draft ?? data.settings;
  const overrideRows = rows ?? overridesToRows(cur.retention.event_overrides);
  const parsed = rowsToOverrides(overrideRows, limits["retention.event_overrides"]);
  const next: AutomationSettings = { ...cur, retention: { ...cur.retention, event_overrides: parsed.value } };
  const problem = parsed.error ?? validate(next, limits);
  const dirty = !sameSettings(next, data.settings);

  const set = (f: (s: AutomationSettings) => AutomationSettings) => setDraft(f(cur));
  const setR = (p: Partial<AutomationRetention>) => set((s) => ({ ...s, retention: { ...s.retention, ...p } }));

  const onSave = async () => {
    const changes = retentionShortened(data.settings.retention, next.retention, server.web_view_names);
    if (changes.length === 0) return save.mutate(next);
    try {
      const result = await check.mutateAsync(next);
      setPreview({ result, changes });
    } catch { /* shown below */ }
  };

  const days = (key: keyof AutomationRetention, label: string) => {
    const l = limits[`retention.${key}`];
    return (
      <input className="input inline num-input" type="number" min={l?.zero ? 0 : l?.min} max={l?.max} aria-label={label}
        value={cur.retention[key] as number} onChange={(e) => setR({ [key]: Math.trunc(Number(e.target.value) || 0) } as Partial<AutomationRetention>)} />
    );
  };
  const follow = (key: "message_event_days" | "web_view_days", label: string) => {
    const v = cur.retention[key];
    return (
      <div className="row">
        <label className="check inline-check">
          <input type="checkbox" checked={v === null} onChange={(e) => setR({ [key]: e.target.checked ? null : cur.retention.event_days } as Partial<AutomationRetention>)} />
          <span>Same as events</span>
        </label>
        {v !== null && (
          <input className="input inline num-input" type="number" min={0} max={3650} aria-label={label}
            value={v} onChange={(e) => setR({ [key]: Math.trunc(Number(e.target.value) || 0) } as Partial<AutomationRetention>)} />
        )}
      </div>
    );
  };
  const range = (key: string) => {
    const l = limits[key];
    return l ? <span className="xs faint"> {l.min}–{l.max}{l.zero ? ", 0 = forever" : ""}</span> : null;
  };
  const schedule = retrySchedule(cur.retries.attempts, cur.retries.max_backoff_seconds);

  return (
    <div className="page automation">
      <PageHead title="Automation & limits" intro="Everything Pulse does on its own for this workspace: how long it keeps data, how it retries failed sends, when it refreshes segments, and the limits new providers start with. Defaults are what Pulse has always done." />
      <ErrorNote error={q.error ?? save.error ?? check.error} />
      <fieldset disabled={!admin} className="bare">
        <Panel title="Keep data for" note={<>
          Days; 0 keeps forever. Cleanup runs once a day for this workspace, around {pad(server.retention_hour_utc)}:00 UTC
          {server.last_retention_run ? <>, last {timeAgo(server.last_retention_run)}</> : <>, and hasn't run yet</>}. Contacts are never deleted by retention, except anonymous visitors below.
        </>}>
          <Row label="Events" def={formatDays(def.retention.event_days)} help={<>The event log behind segments, timelines and reports, for every event name without its own setting below.{range("retention.event_days")}</>}>{days("event_days", "Events")}</Row>
          <Row label="Message events" def="same as events" help="Sends, deliveries, opens and clicks behind campaign reports and dashboards. Shorter keeps reports of old campaigns from being complete.">{follow("message_event_days", "Message events")}</Row>
          <Row label="Page and screen views" def="same as events" help={<>The usually largest events: {server.web_view_names.join(", ")}.</>}>{follow("web_view_days", "Page and screen views")}</Row>
          <Row label="By event name" def="none" help={<>Wins over the settings above, e.g. page_view 90 days and purchase 0 (forever). Up to {server.max_overrides} names.</>}>
            <div className="overrides">
              {overrideRows.map((r, i) => (
                <div className="row" key={i}>
                  <input className="input inline" placeholder="event name" aria-label="Event name" value={r.name}
                    onChange={(e) => setRows(overrideRows.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
                  <input className="input inline num-input" type="number" min={0} max={3650} aria-label={`Days for ${r.name || "this event"}`} value={r.days}
                    onChange={(e) => setRows(overrideRows.map((x, j) => (j === i ? { ...x, days: Math.trunc(Number(e.target.value) || 0) } : x)))} />
                  <span className="xs muted">{formatDays(r.days)}</span>
                  <button className="btn ghost sm" type="button" onClick={() => setRows(overrideRows.filter((_, j) => j !== i))}>Remove</button>
                </div>
              ))}
              {overrideRows.length < server.max_overrides && (
                <button className="btn sm" type="button" onClick={() => setRows([...overrideRows, { name: "", days: 90 }])}>Add event name</button>
              )}
            </div>
          </Row>
          <Row label="Anonymous visitors" def="off" help="Visitors who never signed in are deleted, with their events, after this many days without activity.">
            <div className="row">
              <label className="check inline-check">
                <input type="checkbox" checked={cur.retention.anonymous_days > 0} onChange={(e) => setR({ anonymous_days: e.target.checked ? 90 : 0 })} />
                <span>Delete idle visitors</span>
              </label>
              {cur.retention.anonymous_days > 0 && <>{days("anonymous_days", "Anonymous visitors, days idle")}<span className="xs muted">days idle</span></>}
            </div>
          </Row>
          <Row label="Audit log" def={formatDays(def.retention.audit_days)} help={<>Who changed what.{range("retention.audit_days")}</>}>{days("audit_days", "Audit log")}</Row>
          <Row label="Import history" def={formatDays(def.retention.import_days)} help={<>Finished background imports on the import page (the contacts stay).{range("retention.import_days")}</>}>{days("import_days", "Import history")}</Row>
          <Row label="Webhook deliveries" def={formatDays(def.retention.webhook_delivery_days)} help={<>The attempts shown on each webhook's page.{range("retention.webhook_delivery_days")}</>}>{days("webhook_delivery_days", "Webhook deliveries")}</Row>
          <Row label="Integration log" def={formatDays(def.retention.integration_log_days)} help={<>What each integration did.{range("retention.integration_log_days")}</>}>{days("integration_log_days", "Integration log")}</Row>
          <Row label="Test inbox" def={formatDays(def.retention.sandbox_days)} help={<>Messages sent through sandbox providers.{range("retention.sandbox_days")}</>}>{days("sandbox_days", "Test inbox")}</Row>
          <Row label="Live event feed" def={`${def.retention.live_feed_size} events`} help={<>How many recent events Analytics → Live keeps (a size, not days; it also forgets events after a day without new ones).{range("retention.live_feed_size")}</>}>
            <div className="row">{days("live_feed_size", "Live event feed size")}<span className="xs muted">events</span></div>
          </Row>
        </Panel>

        <Panel title="Retries of failed sends" note="A send that fails temporarily (network error, timeout, provider busy) is tried again later; after the last try it is recorded as failed with the reason. A provider's Retry-After is respected up to the longest wait.">
          <Row label="Tries in all" def={String(def.retries.attempts)} help={<>The first try included; 1 means no retries.{range("retries.attempts")}</>}>
            <input className="input inline num-input" type="number" min={1} max={20} aria-label="Tries in all" value={cur.retries.attempts}
              onChange={(e) => set((s) => ({ ...s, retries: { ...s.retries, attempts: Math.trunc(Number(e.target.value) || 0) } }))} />
          </Row>
          <Row label="Longest wait" def={formatDuration(def.retries.max_backoff_seconds)} help="The waits grow from 30 seconds; none is longer than this.">
            <div className="row">
              <input className="input inline num-input" type="number" min={1} max={1440} aria-label="Longest wait in minutes" value={Math.round(cur.retries.max_backoff_seconds / 60)}
                onChange={(e) => set((s) => ({ ...s, retries: { ...s.retries, max_backoff_seconds: Math.trunc(Number(e.target.value) || 0) * 60 } }))} />
              <span className="xs muted">minutes</span>
            </div>
          </Row>
          <p className="xs muted schedule">Waits: {schedule.length ? schedule.map(formatDuration).join(", ") : "none (no retries)"}; about {formatDuration(schedule.reduce((a, b) => a + b, 0))} until a message is given up.</p>
        </Panel>

        <Panel title="Schedules">
          <Row label="Segment sweep" def={`server default (${server.sweep_minutes} min)`} help="How often segments with time windows (“in the last 7 days”) are re-checked, so contacts whose events grew too old leave. Members join and leave on events at once either way.">
            <div className="row">
              <input className="input inline num-input" type="number" min={0} max={1440} aria-label="Sweep every minutes" value={cur.schedules.sweep_minutes}
                onChange={(e) => set((s) => ({ ...s, schedules: { ...s.schedules, sweep_minutes: Math.trunc(Number(e.target.value) || 0) } }))} />
              <span className="xs muted">minutes; 0 = the server's {server.sweep_minutes} min</span>
            </div>
          </Row>
          <Row label="Reachability rebuild" def="a day after the last one" help="Reachable counts (who has an email, a phone, push devices, consent) follow every change; once a day they are also rebuilt from scratch.">
            <select className="select inline" aria-label="Rebuild hour" value={cur.schedules.reach_rebuild_hour}
              onChange={(e) => set((s) => ({ ...s, schedules: { ...s.schedules, reach_rebuild_hour: Number(e.target.value) } }))}>
              <option value={-1}>A day after the last rebuild</option>
              {HOURS.map((h) => <option key={h} value={h}>Daily at {pad(h)}:00 UTC</option>)}
            </select>
          </Row>
          <Row label="New daily segments" def={`${def.schedules.daily_segment_at}, your browser's time zone`} help="The time a segment set to “refresh daily” starts with; each segment can change it.">
            <div className="row">
              <input className="input inline" type="time" aria-label="Daily segment time" value={cur.schedules.daily_segment_at}
                onChange={(e) => set((s) => ({ ...s, schedules: { ...s.schedules, daily_segment_at: e.target.value } }))} />
              <input className="input inline" placeholder="Browser's zone, or e.g. Asia/Kolkata" aria-label="Daily segment time zone" value={cur.schedules.daily_segment_tz}
                onChange={(e) => set((s) => ({ ...s, schedules: { ...s.schedules, daily_segment_tz: e.target.value.trim() } }))} />
            </div>
          </Row>
        </Panel>

        <Panel title="Messages">
          <Row label="App inbox expiry" def={formatDays(def.messages.inbox_expiry_days)} help={<>How long an inbox message stays when its template doesn't say.{range("messages.inbox_expiry_days")}</>}>
            <div className="row">
              <input className="input inline num-input" type="number" min={1} max={365} aria-label="Inbox expiry days" value={cur.messages.inbox_expiry_days}
                onChange={(e) => set((s) => ({ ...s, messages: { inbox_expiry_days: Math.trunc(Number(e.target.value) || 0) } }))} />
              <span className="xs muted">days</span>
            </div>
          </Row>
        </Panel>

        <Panel title="Default limits of new providers" note="A provider added from now on starts with these when it doesn't bring its own and its kind has no preset (Gmail's daily quota, for one). Existing providers keep theirs; change them on Channels.">
          {server.limit_channels.map((ch) => (
            <LimitsRow key={ch} channel={ch as Channel} value={cur.default_limits?.[ch] ?? {}}
              onChange={(l) => set((s) => ({ ...s, default_limits: { ...(s.default_limits ?? {}), [ch]: l } }))} />
          ))}
        </Panel>

        <Panel title="Fixed by the platform" note="These are the same for every workspace on this server, because they protect shared capacity or the correctness of sending:">
          <ul className="small fixed-list">
            <li>Sending speed per channel for the whole workspace (set by the server operator); a provider's own rate and daily quota are on Channels.</li>
            <li>A message is never sent twice: sends are remembered for 7 days, and delivery reports are matched for 3 days.</li>
            <li>Messages held for quiet hours, local time or a daily quota are released every few seconds, and dropped at their deadline.</li>
            <li>Deleted inbox messages are purged after 7 days; expired ones on the next hourly pass.</li>
            <li>Tracked short links in SMS work for 30 days; journey retry records are kept 30 days.</li>
            <li>The live feed takes at most 50 events a second per workspace.</li>
            <li>Webhook retries (30 s to 6 h) and the cleanup pass interval.</li>
          </ul>
        </Panel>

        {admin && (
          <div className="row save-bar">
            <button className="btn primary" disabled={!dirty || !!problem || save.isPending || check.isPending} onClick={onSave}>
              {check.isPending ? "Counting what would be deleted…" : "Save"}
            </button>
            {dirty && <button className="btn" type="button" onClick={() => { setDraft(null); setRows(null); }}>Discard changes</button>}
            {problem && dirty && <span className="small danger-text">{problem}</span>}
          </div>
        )}
      </fieldset>

      {preview && (
        <Modal title="What will be deleted" onClose={() => setPreview(null)} footer={<>
          <button className="btn" onClick={() => setPreview(null)}>Cancel</button>
          <button className="btn primary danger" disabled={save.isPending} onClick={() => save.mutate(next)}>
            {previewTotal(preview.result.items) > 0 ? `Delete about ${previewTotal(preview.result.items).toLocaleString("en-IN")} rows and save` : "Save"}
          </button>
        </>}>
          <p className="small">This change keeps some data for less time:</p>
          <ul className="small">{preview.changes.map((c) => <li key={c}>{c}</li>)}</ul>
          {preview.result.items.length > 0 ? (
            <table className="table">
              <thead><tr><th>What</th><th>Older than</th><th className="num">Rows now</th></tr></thead>
              <tbody>{preview.result.items.map((it) => (
                <tr key={it.key}>
                  <td>{previewLabel(it)}</td>
                  <td>{formatDays(it.days)} ({new Date(it.cutoff).toLocaleDateString()})</td>
                  <td className="num">{it.error ? <span className="muted">{it.error}</span> : it.rows.toLocaleString("en-IN")}</td>
                </tr>
              ))}</tbody>
            </table>
          ) : <p className="small muted">Nothing is old enough to be deleted yet.</p>}
          <p className="xs muted">Counts are approximate{preview.result.cached ? " (counted in the last minute)" : ""}. The rows are deleted at this workspace's next daily cleanup and can't be recovered.</p>
        </Modal>
      )}
    </div>
  );
}

function Panel({ title, note, children }: { title: string; note?: ReactNode; children: ReactNode }) {
  return (
    <section className="panel section">
      <div className="panel-head"><h3>{title}</h3></div>
      <div className="panel-body">
        {note && <p className="xs muted panel-note">{note}</p>}
        <div className="auto-rows">{children}</div>
      </div>
    </section>
  );
}

function Row({ label, def, help, children }: { label: string; def: string; help: ReactNode; children: ReactNode }) {
  return (
    <div className="auto-row">
      <div className="auto-label"><strong className="small">{label}</strong><span className="xs muted">{help}</span></div>
      <div className="auto-value">{children}</div>
      <div className="auto-default xs muted">Default: {def}</div>
    </div>
  );
}

function LimitsRow({ channel, value, onChange }: { channel: Channel; value: ProviderLimits; onChange: (l: ProviderLimits) => void }) {
  const num = (k: "rate_per_second" | "daily_quota", label: string) => (
    <input className="input inline num-input" type="number" min={0} aria-label={`${CHANNEL_NAMES[channel]} ${label}`} placeholder="none"
      value={value[k] ?? ""} onChange={(e) => onChange({ ...value, [k]: e.target.value === "" ? undefined : Math.max(0, Number(e.target.value)) })} />
  );
  return (
    <div className="auto-row">
      <div className="auto-label"><strong className="small">{CHANNEL_NAMES[channel]}</strong></div>
      <div className="auto-value row">
        {num("rate_per_second", "messages per second")}<span className="xs muted">/s</span>
        {num("daily_quota", "daily quota")}<span className="xs muted">a day, in</span>
        <input className="input inline" placeholder="UTC" aria-label={`${CHANNEL_NAMES[channel]} quota time zone`} value={value.timezone ?? ""}
          onChange={(e) => onChange({ ...value, timezone: e.target.value.trim() || undefined })} />
      </div>
      <div className="auto-default xs muted">Default: none</div>
    </div>
  );
}
