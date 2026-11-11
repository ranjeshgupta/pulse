import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  api, CHANNEL_NAMES, CHANNELS, type ABMetric, type ABTest, type Broadcast, type BroadcastRun, type Channel, type MessageReport, type Repeat,
  type Segment, type Template,
} from "../api";
import { TemplateActions } from "../messagePreview";
import { deadlineProblem } from "../campaigns";
import { AudienceEstimate, DeadlineFields, DuplicateButton, TagEditor } from "../campaignUI";
import { SegmentPicker } from "./InApp";
import { money } from "../insights";
import { CountsTable, DailyBars, metricsFor, ReportFigures, ReportPanel, TopLinks, type CountsRow } from "../reports";
import { FunnelPanel, ReasonsPanel } from "../funnel";
import { describeWindow, nowISO, ScheduleFields, scheduleProblem } from "../schedule";
import { ChannelLabel, Empty, ErrorNote, Field, LiveNumber, PageHead, Status, timeAgo, useToast } from "../ui";

const REPEAT_LABEL: Record<Repeat, string> = { none: "Once", daily: "Every day", weekly: "Every week", monthly: "Every month" };

const fmt = (s?: string | null) => (s ? new Date(s).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" }) : "");

export function BroadcastList() {
  const q = useQuery({ queryKey: ["broadcasts"], queryFn: () => api.get<{ broadcasts: Broadcast[] }>("/v1/broadcasts"), refetchInterval: 5000 });
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const segName = new Map(segs.data?.segments.map((s) => [s.id, s.name]));
  const bs = q.data?.broadcasts ?? [];
  return (
    <div className="page">
      <PageHead title="Broadcasts" intro={<>Send one message to everyone in a segment, once or on a repeat. Journeys react to each contact; broadcasts go out on a schedule. They are the one-time campaigns of <Link to="/campaigns?tab=one_time">Campaigns</Link>, where the step-by-step wizard writes the message for you.</>} actions={<><Link className="btn" to="/campaigns/new/email">One-time campaign wizard</Link><Link className="btn primary" to="/broadcasts/new">New broadcast</Link></>} />
      <ErrorNote error={q.error} />
      <div className="panel">
        {bs.length === 0 && !q.isLoading ? (
          <Empty title="No broadcasts yet" action={<Link className="btn primary" to="/broadcasts/new">Create a broadcast</Link>}>A Friday newsletter to all subscribers, or a festive WhatsApp offer to "Pro plan".</Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th>Channel</th><th>To</th><th>Repeats</th><th>Next send</th><th>Status</th></tr></thead>
            <tbody>{bs.map((b) => (
              <tr key={b.id}>
                <td><Link to={`/broadcasts/${b.id}`}>{b.name}</Link></td>
                <td><ChannelLabel channel={b.channel} /></td>
                <td>{segName.get(b.segment_id) ?? `Segment ${b.segment_id}`}</td>
                <td>{REPEAT_LABEL[b.repeat]}</td>
                <td className="muted">{b.status === "scheduled" ? fmt(b.next_run_at) : ""}</td>
                <td><Status value={b.status} /></td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </div>
    </div>
  );
}

/** browserZone is the time zone this browser is in (the zone a local-time broadcast's time is chosen in). */
export function browserZone(): string {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"; } catch { return "UTC"; }
}

/** localClock shows an instant as clock time (e.g. 9:00 AM) in a zone. */
export function localClock(iso: string, zone?: string): string {
  if (!iso) return "the chosen time";
  try { return new Date(iso).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", timeZone: zone || undefined }); } catch { return new Date(iso).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }); }
}

const NEW: Broadcast = { id: 0, name: "", channel: "email", template_id: 0, segment_id: 0, start_at: "", repeat: "none", status: "draft", conversion_days: 3 };

// ---- A/B testing ----

const LETTERS = "ABCDE";
const relabel = (vs: ABTest["variants"]) => vs.map((v, i) => ({ ...v, label: LETTERS[i] }));
const METRIC_TEXT: Record<ABMetric, string> = { opens: "open rate", clicks: "click rate", manual: "" };
/** A run's key in reports: the unix seconds of its scheduled time. */
const runKey = (iso: string) => String(Math.floor(new Date(iso).getTime() / 1000));

function abProblem(ab: ABTest, channel: Channel): string | null {
  if (ab.variants.some((v) => !v.template_id)) return "Choose a template for each variant.";
  if (!(ab.test_percent >= 2 && ab.test_percent <= 100)) return "The test group must be between 2% and 100%.";
  if (ab.test_percent < 100 && ab.metric !== "manual") {
    if (channel !== "email") return "Opens and clicks are only tracked for email. Pick the winner by hand instead.";
    if (!(ab.wait_hours >= 1 && ab.wait_hours <= 72)) return "Wait between 1 and 72 hours before picking the winner.";
  }
  return null;
}

function ABFields({ ab, channel, templates, onChange }: { ab: ABTest; channel: Channel; templates: Template[]; onChange: (ab: ABTest) => void }) {
  const set = (p: Partial<ABTest>) => onChange({ ...ab, ...p });
  const vs = ab.variants;
  const p = ab.test_percent;
  const whole = p >= 100;
  const each = Math.round((Math.min(100, Math.max(0, p)) / vs.length) * 10) / 10;
  return (
    <div className="ab-box">
      <h4 className="small" style={{ marginBottom: 10 }}>Variants</h4>
      {vs.map((v, i) => (
        <div className="ab-variant" key={i}>
          <span className="ab-label" title={`Variant ${v.label}`}>{v.label}</span>
          <select className="select" aria-label={`Template for variant ${v.label}`} value={v.template_id || ""} onChange={(e) => set({ variants: vs.map((x, j) => (j === i ? { ...x, template_id: Number(e.target.value) || 0 } : x)) })}>
            <option value="">Choose a template</option>
            {templates.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
          <TemplateActions templateId={v.template_id || undefined} />
          {vs.length > 2 ? <button type="button" className="btn ghost sm" onClick={() => set({ variants: relabel(vs.filter((_, j) => j !== i)) })}>Remove</button> : <span />}
        </div>
      ))}
      {vs.length < LETTERS.length && (
        <button type="button" className="btn sm" style={{ marginBottom: 16 }} onClick={() => set({ variants: relabel([...vs, { label: "", template_id: 0 }]) })}>Add variant</button>
      )}
      <Field
        label="Test group"
        hint={whole
          ? `Send variants to everyone (no winner). Each variant goes to about ${each}% of the audience.`
          : `${p}% get a variant (split evenly, about ${each}% each), the other ${Math.max(0, 100 - p)}% get the winner.`}
      >
        <div className="row" style={{ flexWrap: "nowrap" }}>
          <input className="ab-range" type="range" min={2} max={100} value={Math.min(100, Math.max(2, p || 2))} onChange={(e) => set({ test_percent: Number(e.target.value) })} aria-label="Test group percent" />
          <input className="input" style={{ width: 80 }} type="number" min={2} max={100} value={p || ""} onChange={(e) => set({ test_percent: Number(e.target.value) })} aria-label="Test group percent" />
          <span className="small muted">%</span>
        </div>
      </Field>
      {!whole && (
        <div className="grid-2">
          <Field label="Pick the winner by">
            <select className="select" value={ab.metric} onChange={(e) => set({ metric: e.target.value as ABMetric })}>
              <option value="opens" disabled={channel !== "email"}>Open rate{channel !== "email" ? " (email only)" : ""}</option>
              <option value="clicks" disabled={channel !== "email"}>Click rate{channel !== "email" ? " (email only)" : ""}</option>
              <option value="manual">By hand</option>
            </select>
          </Field>
          {ab.metric === "manual" ? (
            <p className="xs muted" style={{ alignSelf: "center" }}>You pick the winner on this page once results come in. The rest of the audience waits until you do.</p>
          ) : (
            <Field label="After" hint="Between 1 and 72 hours after the test goes out.">
              <div className="row" style={{ flexWrap: "nowrap" }}>
                <input className="input" style={{ width: 90 }} type="number" min={1} max={72} value={ab.wait_hours || ""} onChange={(e) => set({ wait_hours: Number(e.target.value) })} />
                <span className="small muted">hours</span>
              </div>
            </Field>
          )}
        </div>
      )}
    </div>
  );
}

export function BroadcastEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const existing = useQuery({ queryKey: ["broadcast", id], queryFn: () => api.get<{ broadcast: Broadcast; runs: BroadcastRun[] }>(`/v1/broadcasts/${id}`), enabled: !isNew, refetchInterval: 4000 });
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const tpls = useQuery({ queryKey: ["templates", ""], queryFn: () => api.get<{ templates: Template[] }>("/v1/templates") });
  const events = useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names") });
  const [b, setB] = useState<Broadcast | null>(isNew ? { ...NEW, start_at: nowISO() } : null);
  useEffect(() => { if (existing.data && !b) setB(existing.data.broadcast); }, [existing.data, b]);

  const save = useMutation({
    mutationFn: () => {
      const body = {
        ...b!, end_at: b!.end_at ?? null, fallback_template_id: b!.fallback_template_id || 0,
        template_id: b!.ab ? b!.ab.variants[0]?.template_id ?? 0 : b!.template_id, ab: b!.ab ?? null,
        conversion_event: (b!.conversion_event ?? "").trim(), conversion_days: b!.conversion_days || 3,
        tags: existing.data?.broadcast.tags ?? b!.tags ?? [], // edited on their own (BroadcastTags)
      };
      return isNew ? api.post<{ id: number }>("/v1/broadcasts", body) : api.put<{ id: number }>(`/v1/broadcasts/${id}`, body);
    },
  });
  const schedule = useMutation({
    mutationFn: async () => {
      const r = await save.mutateAsync();
      return { id: r.id, res: await api.post<{ next_run_at: string }>(`/v1/broadcasts/${r.id}/schedule`) };
    },
    onSuccess: ({ id: bid, res }) => {
      qc.invalidateQueries({ queryKey: ["broadcasts"] });
      qc.invalidateQueries({ queryKey: ["broadcast", String(bid)] });
      toast({ text: `Scheduled. First send ${fmt(res.next_run_at)}.` });
      setB(null);
      if (isNew) nav(`/broadcasts/${bid}`, { replace: true });
    },
  });
  const saveDraft = useMutation({
    mutationFn: () => save.mutateAsync(),
    onSuccess: (r) => { qc.invalidateQueries({ queryKey: ["broadcasts"] }); toast({ text: "Saved as draft" }); setB(null); if (isNew) nav(`/broadcasts/${r.id}`, { replace: true }); else qc.invalidateQueries({ queryKey: ["broadcast", id] }); },
  });
  const cancel = useMutation({
    mutationFn: () => api.post(`/v1/broadcasts/${id}/cancel`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["broadcast", id] }); setB(null); toast({ text: "Broadcast cancelled. Nothing more will be sent." }); },
  });
  const remove = useMutation({ mutationFn: () => api.del(`/v1/broadcasts/${id}`), onSuccess: () => { qc.invalidateQueries({ queryKey: ["broadcasts"] }); nav("/broadcasts"); } });

  if (!b) return <div className="page"><ErrorNote error={existing.error} /></div>;
  const set = (p: Partial<Broadcast>) => setB({ ...b, ...p });
  const templates = tpls.data?.templates ?? [];
  const channelTemplates = templates.filter((t) => t.channel === b.channel);
  const sms = templates.filter((t) => t.channel === "sms");
  const seg = segs.data?.segments.find((s) => s.id === b.segment_id);
  // The saved status moves on its own (scheduled, running, testing…); the form keeps the draft.
  const status = existing.data?.broadcast.status ?? b.status;
  const testing = status === "testing";
  const live = status === "scheduled" || status === "running" || testing;
  const days = b.conversion_days || 3;
  const problem = !b.name ? "Name the broadcast." : !b.segment_id ? "Choose who receives it."
    : b.ab ? abProblem(b.ab, b.channel)
    : !b.template_id ? "Choose a template." : null;
  const deadlineIssue = deadlineProblem(b.deadline);
  const fullProblem = problem
    ?? (b.conversion_event?.trim() && !(days >= 1 && days <= 30) ? "Count conversions within 1 to 30 days." : null)
    ?? deadlineIssue
    ?? scheduleProblem(b.start_at, b.end_at);
  const runs = existing.data?.runs ?? [];
  const saved = existing.data?.broadcast;

  const setChannel = (c: Channel) => set({
    channel: c, template_id: 0, fallback_template_id: 0,
    ab: b.ab ? { ...b.ab, variants: b.ab.variants.map((v) => ({ ...v, template_id: 0 })), metric: c !== "email" && b.ab.metric !== "manual" ? "manual" : b.ab.metric } : b.ab,
  });
  const setAB = (on: boolean) => set(on
    ? { ab: { variants: [{ label: "A", template_id: b.template_id }, { label: "B", template_id: 0 }], test_percent: 20, metric: b.channel === "email" ? "opens" : "manual", wait_hours: 4 } }
    : { ab: null, template_id: b.ab?.variants[0]?.template_id ?? b.template_id });
  const abWinner = saved?.ab && saved.ab.test_percent < 100;

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/broadcasts">Broadcasts</Link>}
        title={isNew ? "New broadcast" : b.name}
        actions={<>
          {!isNew && <Status value={status} />}
          {!isNew && !live && <button className="btn danger" onClick={() => confirm("Delete this broadcast?") && remove.mutate()}>Delete</button>}
          {!isNew && <DuplicateButton path={`/v1/broadcasts/${id}/duplicate`} open={(nid) => `/broadcasts/${nid}`} invalidate={["broadcasts", "campaigns"]} />}
          {!isNew && !live && <Link className="btn" to={`/campaigns/one-time/${id}`}>Open in the wizard</Link>}
          {live ? <button className="btn" onClick={() => confirm(testing ? "Cancel this broadcast? The winner won't be sent to the rest of the audience." : "Cancel all future sends of this broadcast?") && cancel.mutate()}>Cancel broadcast</button> : (
            <>
              <button className="btn" disabled={saveDraft.isPending} onClick={() => saveDraft.mutate()}>Save draft</button>
              <button className="btn primary" disabled={!!fullProblem || schedule.isPending} title={fullProblem ?? undefined} onClick={() => schedule.mutate()}>Schedule</button>
            </>
          )}
        </>}
      />
      <div style={{ whiteSpace: "pre-line" }}><ErrorNote error={save.error ?? schedule.error ?? cancel.error ?? remove.error} /></div>
      {live && !testing && <div className="alert info">This broadcast is scheduled{saved?.next_run_at ? `; next send ${fmt(saved.next_run_at)}` : ""}. Cancel it to make changes.</div>}
      {testing && <div className="alert info">The A/B test has gone out and is waiting for its winner. You can't edit the broadcast now, only cancel it.</div>}
      <fieldset disabled={live} style={{ border: 0, padding: 0, margin: 0 }}>
        <div className="grid-2">
          <Field label="Name"><input className="input" value={b.name} onChange={(e) => set({ name: e.target.value })} placeholder="Diwali offer, WhatsApp" /></Field>
          <Field label="Send to" hint={seg ? <>About <LiveNumber value={seg.size} /> contacts are in it right now. Membership is taken at send time.</> : undefined}>
            <select className="select" value={b.segment_id || ""} onChange={(e) => set({ segment_id: Number(e.target.value) })}>
              <option value="">Choose a segment</option>
              {segs.data?.segments.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </Field>
          <SegmentPicker label="Also send to members of (optional)" hint="The audience is everyone in any of the chosen segments."
            segments={(segs.data?.segments ?? []).filter((s) => s.id !== b.segment_id && !(b.exclude_segment_ids ?? []).includes(s.id))} value={b.include_segment_ids ?? []}
            onChange={(include_segment_ids) => set({ include_segment_ids })} />
          <SegmentPicker label="Leave out members of (optional)" hint="Nobody in these segments gets it, even if they're in the audience."
            segments={(segs.data?.segments ?? []).filter((s) => s.id !== b.segment_id && !(b.include_segment_ids ?? []).includes(s.id))} value={b.exclude_segment_ids ?? []}
            onChange={(exclude_segment_ids) => set({ exclude_segment_ids })} />
          <Field label="Channel">
            <select className="select" value={b.channel} onChange={(e) => setChannel(e.target.value as Channel)}>
              {CHANNELS.map((c) => <option key={c} value={c}>{CHANNEL_NAMES[c]}</option>)}
            </select>
          </Field>
          {!b.ab && (
            <Field label="Template">
              <select className="select" value={b.template_id || ""} onChange={(e) => set({ template_id: Number(e.target.value) })}>
                <option value="">Choose a template</option>
                {b.template_id > 0 && !channelTemplates.some((t) => t.id === b.template_id) && <option value={b.template_id}>Written in this campaign</option>}
                {channelTemplates.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
              </select>
            </Field>
          )}
        </div>
        {!b.ab && <TemplateActions templateId={b.template_id || undefined} size="" />}
        <label className="check"><input type="checkbox" checked={!!b.ab} onChange={(e) => setAB(e.target.checked)} />A/B test: try up to 5 versions on part of the audience first</label>
        {b.ab && <ABFields ab={b.ab} channel={b.channel} templates={channelTemplates} onChange={(ab) => set({ ab })} />}
        {(b.channel === "whatsapp" || b.channel === "rcs") && (
          <Field label="If it can't be delivered, send this SMS instead" hint={`Used when a number isn't on ${CHANNEL_NAMES[b.channel]} or delivery fails. Sent at most once per contact.`}>
            <select className="select" value={b.fallback_template_id || ""} onChange={(e) => set({ fallback_template_id: Number(e.target.value) || 0 })}>
              <option value="">No fallback</option>
              {sms.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
            </select>
          </Field>
        )}
        <h3 style={{ margin: "10px 0 12px" }}>When</h3>
        <div className="grid-2">
          <Field label="Repeat">
            <select className="select" value={b.repeat} onChange={(e) => set({ repeat: e.target.value as Repeat, local_time: e.target.value === "none" ? b.local_time : false })}>
              {Object.entries(REPEAT_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
        </div>
        <ScheduleFields start={b.start_at} end={b.end_at} onChange={(w) => set({ start_at: w.start_at ?? "", end_at: w.end_at ?? null })} startLabel={b.repeat === "none" ? "Send at" : "First send"} />
        {b.repeat !== "none" && <p className="xs muted" style={{ marginTop: -6 }}>Sends {REPEAT_LABEL[b.repeat].toLowerCase()} at the same time. {b.end_at ? "" : "With no end, it keeps going until you cancel it."}</p>}
        {b.repeat === "none" && !b.local_time && new Date(b.start_at) < new Date() && <p className="xs muted" style={{ marginTop: -6 }}>The time has passed, so scheduling sends it right away.</p>}
        {b.repeat === "none" && (
          <label className="check">
            <input type="checkbox" checked={!!b.local_time} onChange={(e) => set({ local_time: e.target.checked, timezone: e.target.checked ? browserZone() : "" })} />
            Send at this time in each contact's own time zone
          </label>
        )}
        {b.repeat === "none" && b.local_time && (
          <p className="xs muted" style={{ marginTop: -6, maxWidth: "75ch" }}>
            Each contact gets it at {localClock(b.start_at, b.timezone)} their time, from their <code>timezone</code> attribute (or the zone located from their IP); contacts with neither get it at {localClock(b.start_at, b.timezone)} in {b.timezone || "UTC"}. Sending starts when that time comes in the first time zone (UTC+14), up to a day before; anyone whose time has already passed when you schedule gets it right away.
          </p>
        )}
        <DeadlineFields value={b.deadline} onChange={(deadline) => set({ deadline })} localTime={b.repeat === "none" && !!b.local_time} zone={b.timezone || browserZone()} />
        <h3 style={{ margin: "18px 0 12px" }}>Conversion goal</h3>
        <div className="grid-2">
          <Field label="Event (optional)" hint="The results count recipients who do this after getting the message, e.g. purchase.">
            <input className="input" list="broadcast-event-names" value={b.conversion_event ?? ""} placeholder="purchase" onChange={(e) => set({ conversion_event: e.target.value })} />
          </Field>
          <Field label="Within">
            <div className="row" style={{ flexWrap: "nowrap" }}>
              <input className="input" style={{ width: 90 }} type="number" min={1} max={30} disabled={!b.conversion_event?.trim()} placeholder="3" value={b.conversion_days || ""} onChange={(e) => set({ conversion_days: Number(e.target.value) })} />
              <span className="small muted">days of their send</span>
            </div>
          </Field>
        </div>
        <datalist id="broadcast-event-names">{events.data?.names.map((n) => <option key={n} value={n} />)}</datalist>
        <h3 style={{ margin: "18px 0 12px" }}>Who it reaches now</h3>
        {b.segment_id ? <div style={{ maxWidth: 560 }}><AudienceEstimate include={[b.segment_id, ...(b.include_segment_ids ?? [])]} exclude={b.exclude_segment_ids ?? []} channel={b.channel} /></div> : <p className="small muted">Choose who receives it.</p>}
      </fieldset>
      {!isNew && <BroadcastTags id={id!} tags={saved?.tags ?? []} />}

      {!isNew && (
        <section className="section">
          <h3>Sends</h3>
          {runs.length === 0 ? <p className="muted small" style={{ marginTop: 8 }}>{describeWindow(b.start_at, b.end_at) || "Not sent yet."}</p> : (
            <div className="panel" style={{ marginTop: 10 }}>
              <SendsTable runs={runs} abWinner={!!abWinner} ab={runs.some((r) => r.test_recipients > 0 || !!r.winner) || !!saved?.ab} />
            </div>
          )}
        </section>
      )}

      {!isNew && saved && runs.length > 0 && <BroadcastResults id={id!} bc={saved} runs={runs} templates={templates} />}
    </div>
  );
}

function SendsTable({ runs, ab, abWinner }: { runs: BroadcastRun[]; ab: boolean; abWinner: boolean }) {
  const winnerCell = (r: BroadcastRun) => {
    if (r.winner && r.winner_sent_at) return <>{r.winner} to <LiveNumber value={r.winner_recipients} /></>;
    if (r.winner) return <span className="muted">Sending {r.winner}…</span>;
    if (r.winner_due_at) return <span className="muted">Picked {fmt(r.winner_due_at)}</span>;
    if (abWinner && r.test_recipients > 0) return <span className="muted">Waiting for your pick</span>;
    return <span className="faint">–</span>;
  };
  return (
    <table className="table">
      <thead><tr>
        <th>Scheduled for</th><th>Sent</th>
        {ab && <><th className="num">Test group</th><th>Winner</th></>}
        <th className="num">Recipients</th>
      </tr></thead>
      <tbody>{runs.map((r) => (
        <tr key={r.id}>
          <td>{fmt(r.scheduled_at)}</td>
          <td className="muted">{r.finished_at ? timeAgo(r.finished_at) : "sending…"}</td>
          {ab && <><td className="num"><LiveNumber value={r.test_recipients || r.recipients} /></td><td>{winnerCell(r)}</td></>}
          <td className="num"><LiveNumber value={r.recipients + (r.winner_recipients || 0)} /></td>
        </tr>
      ))}</tbody>
    </table>
  );
}

// BroadcastResults is the report: headline figures, A/B variants (with the winner pick), sends, links, days.
function BroadcastResults({ id, bc, runs, templates }: { id: string; bc: Broadcast; runs: BroadcastRun[]; templates: Template[] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [run, setRun] = useState(""); // "" is all runs; otherwise a run's unix-seconds key
  const report = useQuery({
    queryKey: ["broadcast-report", id, run],
    queryFn: () => api.get<MessageReport>(`/v1/broadcasts/${id}/report${run ? `?run=${run}` : ""}`),
    refetchInterval: 5000,
  });
  const pick = useMutation({
    mutationFn: (v: { runId: number; variant: string }) => api.post(`/v1/broadcasts/${id}/runs/${v.runId}/winner`, { variant: v.variant }),
    onSuccess: (_, v) => {
      qc.invalidateQueries({ queryKey: ["broadcast", id] });
      qc.invalidateQueries({ queryKey: ["broadcast-report", id] });
      toast({ text: `${v.variant} is the winner. It goes to the rest of the audience in a few seconds.` });
    },
  });

  const r = report.data;
  const ab = bc.ab;
  const winnerStep = !!ab && ab.test_percent < 100;
  const rest = ab ? Math.max(0, 100 - ab.test_percent) : 0;
  const newestFirst = [...runs].sort((x, y) => y.scheduled_at.localeCompare(x.scheduled_at));
  const awaiting = (x: BroadcastRun) => winnerStep && !x.winner && !x.winner_sent_at;
  const selected = run ? runs.find((x) => runKey(x.scheduled_at) === run) : runs.length === 1 ? runs[0] : undefined;
  // The run a winner can be picked for: the one chosen, or the newest still waiting.
  const target = selected ? (awaiting(selected) ? selected : undefined) : newestFirst.find(awaiting);
  const shown = selected ?? target;

  const channels: string[] = [bc.channel, ...Object.keys(r?.by_channel ?? {})];
  const conversion = !!bc.conversion_event || (r?.totals.converted ?? 0) > 0;
  const keys = metricsFor([bc.channel], { conversion });
  const variantKeys = metricsFor([bc.channel], { conversion }).filter((k) => k !== "delivered" && k !== "unsubscribed");
  const tplName = new Map(templates.map((t) => [t.id, t.name]));
  const variantTpl = new Map(ab?.variants.map((v) => [v.label, tplName.get(v.template_id) ?? `Template ${v.template_id}`]));

  const labels = [...new Set([...(ab?.variants.map((v) => v.label) ?? []), ...Object.keys(r?.by_variant ?? {}).filter((k) => k !== "winner").sort()])];
  const variantRows: CountsRow[] = labels.map((label) => ({
    key: label,
    lead: [<strong key="l">{label}</strong>, variantTpl.get(label) ?? <span className="faint">–</span>],
    counts: r?.by_variant?.[label],
    highlight: !!shown?.winner && shown.winner === label,
    trail: target ? [
      <button key="p" className="btn sm" disabled={pick.isPending} onClick={() => confirm(`Send ${label} to the other ${rest}% of the audience? This can't be undone.`) && pick.mutate({ runId: target.id, variant: label })}>Pick {label} as winner</button>,
    ] : undefined,
  }));
  if (r?.by_variant?.winner) {
    const w = shown?.winner;
    variantRows.push({ key: "winner", lead: [<span key="w">Winner{w ? ` (${w})` : ""}</span>, <span key="t" className="muted">Sent to the rest</span>], counts: r.by_variant.winner, highlight: !!w });
  }

  let winnerLine: string | null = null;
  if (target) {
    winnerLine = target.winner_due_at
      ? `The variant with the best ${METRIC_TEXT[ab!.metric] || "result"} is picked ${fmt(target.winner_due_at)} and sent to the other ${rest}%. You can pick one yourself before then.`
      : `Pick a winner to send it to the other ${rest}% of the audience.`;
    if (runs.length > 1 && !run) winnerLine = `Send of ${fmt(target.scheduled_at)}: ${winnerLine}`;
  } else if (shown?.winner) {
    winnerLine = `${shown.winner} won${shown.winner_reason ? `: ${shown.winner_reason}` : ""}.`
      + (shown.winner_sent_at ? ` Sent to ${shown.winner_recipients.toLocaleString("en-IN")} more contacts ${timeAgo(shown.winner_sent_at)}.` : " Sending it to the rest now.");
  }

  const runRows: CountsRow[] = Object.entries(r?.by_run ?? {})
    .sort(([a], [b]) => Number(b) - Number(a))
    .map(([k, counts]) => ({ key: k, lead: [fmt(new Date(Number(k) * 1000).toISOString())], counts, onClick: () => setRun(k) }));
  const channelRows: CountsRow[] = Object.entries(r?.by_channel ?? {}).map(([c, counts]) => ({
    key: c, lead: [<span key="c" className={`chan chan-${c}`}>{CHANNEL_NAMES[c as Channel] ?? c}</span>], counts,
  }));

  return (
    <section className="section">
      <div className="row" style={{ justifyContent: "space-between", marginBottom: 12 }}>
        <h3>Results</h3>
        {runs.length > 1 && (
          <select className="select inline" value={run} onChange={(e) => setRun(e.target.value)} aria-label="Which send">
            <option value="">All sends</option>
            {newestFirst.map((x) => <option key={x.id} value={runKey(x.scheduled_at)}>{fmt(x.scheduled_at)}</option>)}
          </select>
        )}
      </div>
      <ErrorNote error={report.error ?? pick.error} />
      {r && (
        <>
          <ReportFigures totals={r.totals} keys={keys} />
          {r.revenue && (
            <div className="figures" style={{ marginTop: 0, marginBottom: 18 }}>
              <div><strong>{money(r.revenue.revenue, r.revenue.currency)}</strong><span>revenue from recipients <small>({r.revenue.event} within {r.revenue.days} days of their send)</small></span></div>
              <div><strong>{r.revenue.orders.toLocaleString("en-IN")}</strong><span>{r.revenue.orders === 1 ? "order" : "orders"}</span></div>
              <div><strong>{r.revenue.buyers.toLocaleString("en-IN")}</strong><span>{r.revenue.buyers === 1 ? "buyer" : "buyers"}</span></div>
              {Object.entries(r.revenue.by_variant ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([v, rv]) => (
                <div key={v}><strong>{money(rv.revenue, r.revenue!.currency)}</strong><span>{v === "winner" ? "winner send" : `variant ${v}`} · {rv.orders} {rv.orders === 1 ? "order" : "orders"}</span></div>
              ))}
            </div>
          )}
          {(r.late ?? 0) > 0 && <div className="alert info">{r.late!.toLocaleString("en-IN")} {r.late === 1 ? "contact's message was" : "contacts' messages were"} dropped by the send deadline instead of going out late (counted in Failed).</div>}
          <p className="xs muted" style={{ margin: "0 0 18px" }}>
            Each contact counts once. Rates are out of messages sent.{bc.conversion_event ? ` Converted means they did ${bc.conversion_event} within ${bc.conversion_days || 3} days of their send.` : ""}
          </p>
          {r.funnel && <div style={{ marginBottom: 24 }}><FunnelPanel funnel={r.funnel} channels={channels} /></div>}
          <div style={{ marginBottom: 24 }}><ReasonsPanel reasons={r.reasons} /></div>
          {variantRows.length > 0 && (
            <div style={{ marginBottom: 24 }}>
              <ReportPanel title="Variants" aside={winnerStep && ab?.metric !== "manual" ? <span className="xs muted">Winner by {METRIC_TEXT[ab!.metric]}</span> : undefined}>
                {winnerLine && <p className="small" style={{ padding: "12px 20px 0" }}>{winnerLine}</p>}
                <CountsTable lead={["Variant", "Template"]} rows={variantRows} keys={variantKeys} trail={target ? [""] : []} />
              </ReportPanel>
            </div>
          )}
          {!run && runRows.length > 1 && (
            <div style={{ marginBottom: 24 }}>
              <ReportPanel title="By send" aside={<span className="xs muted">Select a send to see only its results</span>}>
                <CountsTable lead={["Scheduled for"]} rows={runRows} keys={metricsFor([bc.channel], { conversion, detail: true })} />
              </ReportPanel>
            </div>
          )}
          {channelRows.length > 1 && (
            <div style={{ marginBottom: 24 }}>
              <ReportPanel title="By channel" aside={<span className="xs muted">SMS is the fallback</span>}>
                <CountsTable lead={["Channel"]} rows={channelRows} keys={metricsFor(channels, { conversion, detail: true })} />
              </ReportPanel>
            </div>
          )}
          <div className="report-cols" style={{ marginTop: 0 }}>
            {(bc.channel === "email" || r.links.length > 0) && <TopLinks links={r.links} />}
            <DailyBars daily={r.daily} />
          </div>
        </>
      )}
    </section>
  );
}

/** BroadcastTags edits a saved broadcast's tags; they can change at any time, even while it sends. */
function BroadcastTags({ id, tags }: { id: string; tags: string[] }) {
  const qc = useQueryClient();
  const save = useMutation({
    mutationFn: (t: string[]) => api.put(`/v1/broadcasts/${id}/tags`, { tags: t }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["broadcast", id] }); qc.invalidateQueries({ queryKey: ["campaign-tags"] }); },
  });
  return (
    <section className="section" style={{ maxWidth: 640 }}>
      <TagEditor value={tags} onChange={(t) => save.mutate(t)} />
      <ErrorNote error={save.error} />
    </section>
  );
}
