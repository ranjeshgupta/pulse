import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  api, CHANNEL_NAMES, CHANNELS, type Broadcast, type CampaignCategory, type CampaignRow, type Channel, type Journey, type Repeat, type Segment, type Template,
} from "../api";
import { useCan } from "../auth";
import { CAMPAIGN_GOALS, CATEGORIES, describeDeadline, newCampaignHref, stepProblem, WIZARD_STEPS, type WizardState, type WizardStep } from "../campaigns";
import { ContactCount } from "../ContactCount";
import { outcomeFilters } from "../contactLinks";
import { listActions, type ListAction } from "../journeyGraph";
import { AudienceEstimate, DeadlineFields, TagEditor } from "../campaignUI";
import { initialEmailMode, replacesLayout, type EmailMode } from "../editorModes";
import { TestSendModal } from "../messagePreview";
import { describeWindow, nowISO, ScheduleFields, scheduleProblem } from "../schedule";
import { ChannelLabel, Empty, ErrorNote, Field, Modal, PageHead, Status, STATUS_TEXT, timeAgo, useToast } from "../ui";
import { PersonalizeScopeProvider } from "../VarPicker";
import { browserZone, localClock } from "./Broadcasts";
import type { EmailBuilderHandle } from "./EmailBuilder";
import { SegmentPicker } from "./InApp";
import { ProviderPick } from "../ProviderPick";
import { TopicPick } from "../TopicPick";
import { BLANK, emailModeSwitcher, RICH_STARTER, TemplateBody } from "./Templates";

const fmt = (s?: string | null) => (s ? new Date(s).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" }) : "");
const REPEAT_LABEL: Record<string, string> = { none: "Once", daily: "Every day", weekly: "Every week", monthly: "Every month" };
const STATUSES = ["draft", "scheduled", "running", "testing", "completed", "cancelled", "active", "paused", "archived"];

function schedule(r: CampaignRow): string {
  if (r.kind === "broadcast") {
    if (r.status === "scheduled" && r.next_run_at) return `${REPEAT_LABEL[r.repeat ?? "none"]}, next ${fmt(r.next_run_at)}`;
    return REPEAT_LABEL[r.repeat ?? "none"];
  }
  const by = r.trigger === "event" ? `When someone does ${r.trigger_event}` : r.trigger === "form" ? "When someone submits a form" : "When someone joins the segment";
  return r.end_at ? `${by}, until ${fmt(r.end_at)}` : by;
}

const href = (r: CampaignRow) => (r.kind === "broadcast" ? `/broadcasts/${r.id}` : `/journeys/${r.id}`);

/** CampaignHub is the one place for campaigns: one-time, triggered and automation, with what
 * each type means, a "New campaign" chooser by goal, and the journeys' progress and actions. */
export function CampaignHub() {
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") ?? "") as CampaignCategory | "";
  const qc = useQueryClient();
  const toast = useToast();
  const canBroadcast = useCan("broadcasts", "edit");
  const canJourney = useCan("journeys", "edit");
  const canPublishJourney = useCan("journeys", "publish");
  const canViewJourneys = useCan("journeys", "view");
  const [choosing, setChoosing] = useState(params.get("new") === "1");
  const qs = new URLSearchParams();
  if (tab) qs.set("category", tab);
  for (const k of ["status", "channel", "tag", "q"]) { const v = params.get(k); if (v) qs.set(k, v); }
  const q = useQuery({ queryKey: ["campaigns", qs.toString()], queryFn: () => api.get<{ campaigns: CampaignRow[]; counts: Record<CampaignCategory, number> }>(`/v1/campaigns?${qs}`), refetchInterval: 10_000 });
  const journeys = useQuery({ queryKey: ["journeys"], queryFn: () => api.get<{ journeys: Journey[] }>("/v1/journeys"), enabled: canViewJourneys, refetchInterval: 10_000 });
  const jById = new Map((journeys.data?.journeys ?? []).map((j) => [j.id, j]));
  const tags = useQuery({ queryKey: ["campaign-tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/campaigns/tags") });
  const set = (k: string, v: string) => { const p = new URLSearchParams(params); if (v) p.set(k, v); else p.delete(k); setParams(p, { replace: true }); };
  const [search, setSearch] = useState(params.get("q") ?? "");
  useEffect(() => { const t = window.setTimeout(() => { if (search !== (params.get("q") ?? "")) set("q", search); }, 300); return () => window.clearTimeout(t); });
  const rows = q.data?.campaigns ?? [];
  const counts = q.data?.counts;
  const total = counts ? counts.one_time + counts.trigger + counts.automation : undefined;
  const filtered = !!(params.get("status") || params.get("channel") || params.get("tag") || params.get("q"));
  const canCreate = canBroadcast || canJourney;
  const current = CATEGORIES.find((c) => c.key === tab);

  const act = useMutation({
    mutationFn: ({ j, action }: { j: Journey; action: ListAction }) => action === "delete"
      ? api.del(`/v1/journeys/${j.id}`) : api.post(`/v1/journeys/${j.id}/${action === "archive" ? "archive" : "unarchive"}`),
    onSuccess: (_, { j, action }) => {
      qc.invalidateQueries({ queryKey: ["journeys"] });
      qc.invalidateQueries({ queryKey: ["campaigns"] });
      toast({ text: action === "delete" ? `Deleted ${j.name}` : action === "archive" ? `Archived ${j.name}` : `Restored ${j.name}` });
    },
    onError: (e) => toast({ text: (e as Error).message, error: true }),
  });
  const run = (j: Journey, action: ListAction) => {
    const ask = action === "delete" ? `Delete "${j.name}"? This can't be undone.`
      : action === "archive" ? `Archive "${j.name}"? It's hidden from this list (choose the Archived status to see it); its history and reports are kept, and you can restore it.` : null;
    if (!ask || confirm(ask)) act.mutate({ j, action });
  };

  return (
    <div className="page">
      <PageHead
        title="Campaigns"
        intro="Every email, SMS, WhatsApp, RCS, push and app inbox campaign, in one place. Pick a type below to see only those."
        actions={canCreate && <button className="btn primary" onClick={() => setChoosing(true)}>New campaign</button>}
      />
      {choosing && <NewCampaignModal canOneTime={canBroadcast} canJourney={canJourney} onClose={() => setChoosing(false)} />}
      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={tab === ""} onClick={() => set("tab", "")}>All{total !== undefined ? ` (${total})` : ""}</button>
        {CATEGORIES.map((c) => (
          <button key={c.key} role="tab" aria-selected={tab === c.key} title={c.hint} onClick={() => set("tab", c.key)}>{c.label}{counts ? ` (${counts[c.key]})` : ""}</button>
        ))}
      </div>
      <p className="small muted hub-type-hint">
        {current ? <><strong>{current.label}:</strong> {current.hint}</> : <>
          <strong>One-time</strong> campaigns send one message to a segment. <strong>Triggered</strong> campaigns start when someone does something;
          <strong> automations</strong> start when someone joins a segment. Triggered campaigns and automations are built as journeys: a flow of messages, waits and checks.
        </>}
      </p>
      <div className="row hub-filters">
        <input className="input" style={{ maxWidth: 260 }} type="search" placeholder="Search names and tags" value={search} onChange={(e) => setSearch(e.target.value)} aria-label="Search campaigns" />
        <select className="select inline" value={params.get("status") ?? ""} onChange={(e) => set("status", e.target.value)} aria-label="Status">
          <option value="">Any status (archived hidden)</option>
          {STATUSES.map((st) => <option key={st} value={st}>{STATUS_WORDS[st]}</option>)}
        </select>
        <select className="select inline" value={params.get("channel") ?? ""} onChange={(e) => set("channel", e.target.value)} aria-label="Channel">
          <option value="">Any channel</option>
          {CHANNELS.map((c) => <option key={c} value={c}>{CHANNEL_NAMES[c]}</option>)}
        </select>
        <select className="select inline" value={params.get("tag") ?? ""} onChange={(e) => set("tag", e.target.value)} aria-label="Tag">
          <option value="">Any tag</option>
          {(tags.data?.tags ?? []).map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
        {filtered && <button className="btn ghost sm" onClick={() => { setSearch(""); setParams(tab ? { tab } : {}, { replace: true }); }}>Clear filters</button>}
      </div>
      <ErrorNote error={q.error} />
      <div className="panel">
        {rows.length === 0 && !q.isLoading ? (
          <Empty title={filtered ? "Nothing matches these filters" : "No campaigns yet"} action={canCreate ? <button className="btn primary" onClick={() => setChoosing(true)}>New campaign</button> : undefined}>
            {filtered ? "Clear the filters to see every campaign." : "Send one message to a segment, or set up messages that go out when people act."}
          </Empty>
        ) : (
          <div className="table-scroll">
          <table className="table hub-table">
            <thead><tr>
              <th>Name</th><th>Type</th><th>Channels</th><th>Status</th><th>When it sends</th>
              <th className="num" title="One-time and automation: people in the segment now. Triggered campaigns have no fixed audience.">Audience now</th>
              <th className="num" title="Journeys: people still going through its steps">In progress</th>
              <th className="num" title="Contacts sent at least one message (all time)">Sent to</th>
              <th>Last activity</th><th aria-label="Actions" />
            </tr></thead>
            <tbody>{rows.map((r) => {
              const j = r.kind === "journey" ? jById.get(r.id) : undefined;
              const st = j?.stats?.status ?? {};
              return (
                <tr key={`${r.kind}${r.id}`}>
                  <td>
                    <Link to={href(r)}>{r.name}</Link>
                    {r.tags.length > 0 && <div className="row" style={{ gap: 4, marginTop: 4 }}>{r.tags.map((t) => <button key={t} type="button" className="tag-chip tag-link" onClick={() => set("tag", t)} title={`Show campaigns tagged ${t}`}>{t}</button>)}</div>}
                  </td>
                  <td className="small" title={CATEGORIES.find((c) => c.key === r.category)?.hint}>{CATEGORIES.find((c) => c.key === r.category)?.label}</td>
                  <td><div className="row" style={{ gap: 4 }}>{r.channels.length ? r.channels.map((c) => <ChannelLabel key={c} channel={c} />) : <span className="muted small">None yet</span>}</div></td>
                  <td>
                    <Status value={r.status} />
                    {r.problem && <div className="xs camp-problem" title={`Not running: ${r.problem}. Open it, choose Pause to edit and fix that step.`}>Not running: needs a fix</div>}
                  </td>
                  <td className="small muted">{schedule(r)}</td>
                  <td className="num">{r.audience === null ? <span className="muted" title="Contacts start when they do the trigger">—</span> : <ContactCount live value={r.audience} filters={r.segment_id ? [["in", r.segment_id]] : null} />}</td>
                  <td className="num">{r.kind === "journey" && j ? <ContactCount live value={(st.active ?? 0) + (st.waiting ?? 0)} filters={[["journey", r.id], ["journey_status", "in_progress"]]} /> : <span className="faint">—</span>}</td>
                  <td className="num"><ContactCount value={r.sent} filters={outcomeFilters(r.kind, r.id, "sent")} /></td>
                  <td className="small muted">{timeAgo(r.last_activity)}</td>
                  <td className="row-actions">
                    {(r.kind === "broadcast" ? canBroadcast : canJourney) && <DuplicateLink r={r} />}
                    {j && listActions(j).map((a) => (a === "delete" ? canJourney : canPublishJourney) && (
                      <button key={a} className={`btn ghost sm${a === "delete" ? " danger" : ""}`} disabled={act.isPending} onClick={() => run(j, a)}>
                        {a === "delete" ? "Delete" : a === "archive" ? "Archive" : "Restore"}
                      </button>
                    ))}
                  </td>
                </tr>
              );
            })}</tbody>
          </table>
          </div>
        )}
      </div>
    </div>
  );
}

const STATUS_WORDS = STATUS_TEXT;

/** DuplicateLink copies a campaign as a draft and opens the copy. */
function DuplicateLink({ r }: { r: CampaignRow }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const dup = useMutation({
    mutationFn: () => api.post<{ id: number }>(`/v1/${r.kind === "broadcast" ? "broadcasts" : "journeys"}/${r.id}/duplicate`),
    onSuccess: (x) => {
      qc.invalidateQueries({ queryKey: ["campaigns"] });
      qc.invalidateQueries({ queryKey: ["journeys"] });
      toast({ text: "Copy created as a draft" });
      nav(r.kind === "broadcast" ? `/broadcasts/${x.id}` : `/journeys/${x.id}`);
    },
    onError: (e) => toast({ text: (e as Error).message, error: true }),
  });
  return <button className="btn ghost sm" disabled={dup.isPending} onClick={() => dup.mutate()}>Duplicate</button>;
}

/** NewCampaignModal asks what the marketer wants to do, then (one-time) on which channel. */
export function NewCampaignModal({ canOneTime, canJourney, onClose }: { canOneTime: boolean; canJourney: boolean; onClose: () => void }) {
  const nav = useNavigate();
  const [goal, setGoal] = useState<CampaignCategory | null>(null);
  const goals = CAMPAIGN_GOALS.filter((g) => (g.key === "one_time" ? canOneTime : canJourney));
  return (
    <Modal title={goal === "one_time" ? "Which channel?" : "What do you want to do?"} onClose={onClose}>
      {goal !== "one_time" ? (
        <div className="goal-list">
          {goals.map((g) => (
            <button key={g.key} type="button" className="goal-card" onClick={() => (g.key === "one_time" ? setGoal("one_time") : nav(newCampaignHref(g.key)))}>
              <strong>{g.title}</strong>
              <span className="small">{g.text}</span>
              <span className="xs muted">For example: {g.examples}</span>
              <span className="xs faint">{CATEGORIES.find((c) => c.key === g.key)?.label} campaign</span>
            </button>
          ))}
          {goals.length < CAMPAIGN_GOALS.length && <p className="xs muted">Your role can't create the other types.</p>}
        </div>
      ) : (
        <div className="goal-list">
          {CHANNELS.map((c) => (
            <button key={c} type="button" className="goal-card goal-channel" onClick={() => nav(newCampaignHref("one_time", c))}>
              <ChannelLabel channel={c} />
            </button>
          ))}
          <button type="button" className="btn ghost sm" onClick={() => setGoal(null)}>Back</button>
        </div>
      )}
    </Modal>
  );
}

// ---- the one-time wizard ----

const blankTemplate = (ch: Channel): Template =>
  ({ id: 0, name: "", channel: ch, subject: "", html: ch === "email" ? RICH_STARTER : "", content: { ...BLANK[ch] }, updated_at: "", hidden: true });

/** hasBody reports whether a template has something to send. */
function hasBody(t: Template): boolean {
  const c = t.content ?? {};
  switch (t.channel) {
    case "email": return t.html.replace(/<[^>]*>/g, "").replace(/\s|&nbsp;/g, "").length > 0 || t.html.includes("<img");
    case "sms": return !!c.text?.trim();
    case "whatsapp": return !!c.template_name?.trim();
    case "rcs": return !!(c.text?.trim() || c.card?.title);
    default: return !!(c.title?.trim() && c.body?.trim());
  }
}

/** OneTimeWizard creates (or edits) a one-time campaign step by step: audience, content written
 * here, schedule, then a review with the exact estimate. It saves a broadcast and its template. */
export function OneTimeWizard() {
  const { channel: chParam, id } = useParams();
  const editing = !!id;
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const existing = useQuery({ queryKey: ["broadcast", id], queryFn: () => api.get<{ broadcast: Broadcast }>(`/v1/broadcasts/${id}`), enabled: editing });
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const tpls = useQuery({ queryKey: ["templates", ""], queryFn: () => api.get<{ templates: Template[] }>("/v1/templates") });
  const [step, setStep] = useState<WizardStep>("audience");
  const [s, setS] = useState<WizardState | null>(null);
  const [tags, setTags] = useState<string[]>([]);
  const [fallback, setFallback] = useState(0);
  const [provider, setProvider] = useState(0);
  const [topic, setTopic] = useState("");
  const [t, setT] = useState<Template | null>(null);
  const [reusable, setReusable] = useState(false);
  const [emailMode, setEmailMode] = useState<EmailMode>("richtext");
  const [scopeEvent, setScope] = useState("");
  const [testing, setTesting] = useState<Template | null>(null);
  const builder = useRef<EmailBuilderHandle>(null);
  const [bcId, setBcId] = useState<number>(editing ? Number(id) : 0);
  const status = existing.data?.broadcast.status;
  const live = status === "scheduled" || status === "running" || status === "testing";

  useEffect(() => {
    if (s) return;
    if (!editing) {
      const ch = (CHANNELS.includes(chParam as Channel) ? chParam : "email") as Channel;
      setS({ name: "", channel: ch, include: [], exclude: [], templateMode: "write", start_at: nowISO(), end_at: null, repeat: "none", deadline: null });
      setT(blankTemplate(ch));
      return;
    }
    const b = existing.data?.broadcast;
    if (!b) return;
    setS({ name: b.name, channel: b.channel, include: [b.segment_id, ...(b.include_segment_ids ?? [])].filter(Boolean), exclude: b.exclude_segment_ids ?? [],
      templateMode: "write", start_at: b.start_at, end_at: b.end_at, repeat: b.repeat, local_time: b.local_time, deadline: b.deadline ?? null });
    setTags(b.tags ?? []);
    setFallback(b.fallback_template_id ?? 0);
    setProvider(b.provider_id ?? 0);
    setTopic(b.topic ?? "");
    api.get<Template>(`/v1/templates/${b.template_id}`).then((tpl) => {
      if (tpl.hidden) {
        setT({ ...tpl, content: tpl.content ?? {} });
        if (tpl.channel === "email") setEmailMode(initialEmailMode(tpl));
      } else {
        setS((cur) => cur && { ...cur, templateMode: "pick", pickedTemplate: tpl.id });
        setT(blankTemplate(b.channel));
      }
    }).catch(() => setT(blankTemplate(b.channel)));
  }, [editing, existing.data, chParam, s]);

  /** readBuilder pulls the visual builder's HTML into the template before it unmounts or saves. */
  const readBuilder = (): Template | null => {
    if (!t) return t;
    if (t.channel === "email" && emailMode === "visual" && builder.current) {
      const r = builder.current.read();
      const next = { ...t, html: r.html, builder: r.project };
      setT(next);
      return next;
    }
    return t;
  };

  /** saveTemplate saves the written template (hidden unless reusable) and returns its id. */
  const saveTemplate = async (): Promise<number> => {
    if (!s) throw new Error("not ready");
    if (s.templateMode === "pick") return s.pickedTemplate ?? 0;
    const cur = readBuilder()!;
    const body = { ...cur, name: cur.name.trim() || `${s.name.trim() || "One-time campaign"} (${CHANNEL_NAMES[s.channel]})`, hidden: !reusable,
      builder: cur.channel === "email" && replacesLayout(emailMode) ? undefined : cur.builder };
    if (cur.id) {
      await api.put(`/v1/templates/${cur.id}`, body);
      return cur.id;
    }
    const r = await api.post<{ id: number }>("/v1/templates", body);
    setT({ ...cur, id: r.id, name: body.name });
    return r.id;
  };

  const save = async (): Promise<number> => {
    const tid = await saveTemplate();
    const st = s!;
    const body = {
      name: st.name.trim(), channel: st.channel, template_id: tid, segment_id: st.include[0], include_segment_ids: st.include.slice(1),
      exclude_segment_ids: st.exclude, start_at: st.start_at, end_at: st.end_at ?? null, repeat: st.repeat as Repeat,
      local_time: !!st.local_time, timezone: st.local_time ? browserZone() : "", deadline: st.deadline ?? null, tags,
      fallback_template_id: fallback || 0, provider_id: provider || 0, topic, conversion_days: 3,
    };
    if (bcId) {
      await api.put(`/v1/broadcasts/${bcId}`, body);
      return bcId;
    }
    const r = await api.post<{ id: number }>("/v1/broadcasts", body);
    setBcId(r.id);
    return r.id;
  };
  const draft = useMutation({
    mutationFn: save,
    onSuccess: (bid) => { qc.invalidateQueries({ queryKey: ["campaigns"] }); qc.invalidateQueries({ queryKey: ["broadcasts"] }); toast({ text: "Saved as draft" }); if (!editing) nav(`/campaigns/one-time/${bid}`, { replace: true }); },
  });
  const launch = useMutation({
    mutationFn: async () => {
      const bid = await save();
      const r = await api.post<{ next_run_at: string }>(`/v1/broadcasts/${bid}/schedule`);
      return { bid, next: r.next_run_at };
    },
    onSuccess: ({ bid, next }) => {
      qc.invalidateQueries({ queryKey: ["campaigns"] });
      qc.invalidateQueries({ queryKey: ["broadcasts"] });
      qc.invalidateQueries({ queryKey: ["broadcast", String(bid)] });
      toast({ text: new Date(next).getTime() <= Date.now() + 60_000 ? "Sending now." : `Scheduled for ${fmt(next)}.` });
      nav(`/broadcasts/${bid}`);
    },
  });
  const test = useMutation({
    mutationFn: async () => {
      const tid = await saveTemplate();
      return api.get<Template>(`/v1/templates/${tid}`);
    },
    onSuccess: (tpl) => setTesting(tpl),
  });

  if (!s || !t) return <div className="page"><ErrorNote error={existing.error} /></div>;
  const segments = segs.data?.segments ?? [];
  const reusableTemplates = (tpls.data?.templates ?? []).filter((x) => x.channel === s.channel);
  const set = (p: Partial<WizardState>) => setS({ ...s, ...p });
  const content = { subject: t.subject, body: hasBody(t) };
  const problem = stepProblem(s, step, content) ?? (step === "schedule" ? scheduleProblem(s.start_at, s.end_at) : null);
  const allProblem = (["audience", "content", "schedule"] as WizardStep[]).map((x) => stepProblem(s, x, content)).find(Boolean) ?? scheduleProblem(s.start_at, s.end_at);
  const idx = WIZARD_STEPS.findIndex((w) => w.key === step);
  const go = (to: WizardStep) => { if (step === "content") readBuilder(); setStep(to); window.scrollTo({ top: 0 }); };
  const segName = (sid: number) => segments.find((x) => x.id === sid)?.name ?? `Segment ${sid}`;
  const sendsNow = s.repeat === "none" && !s.local_time && new Date(s.start_at) <= new Date();
  const busy = draft.isPending || launch.isPending;

  return (
    <PersonalizeScopeProvider value={{ event: scopeEvent || undefined, channel: s.channel }}>
    <div className="page" style={step === "content" && s.channel === "email" ? { maxWidth: "none" } : undefined}>
      <PageHead
        back={<Link className="back" to="/campaigns?tab=one_time">Campaigns · One-time</Link>}
        title={s.name || `New one-time ${CHANNEL_NAMES[s.channel]} campaign`}
        actions={<>
          {status && <Status value={status} />}
          {editing && <Link className="btn ghost" to={`/broadcasts/${id}`} title="A/B tests, conversion goal, results">All settings and results</Link>}
          {!live && <button className="btn" disabled={busy || !s.name.trim() || s.include.length === 0} title={!s.name.trim() || s.include.length === 0 ? "Name it and choose an audience first" : undefined} onClick={() => draft.mutate()}>Save draft</button>}
        </>}
      />
      <ErrorNote error={draft.error ?? launch.error ?? test.error ?? existing.error} />
      {live && <div className="alert info">This campaign is scheduled or sending. <Link to={`/broadcasts/${id}`}>Open it</Link> to follow it or cancel it before changing anything.</div>}
      <ol className="wizard-steps" aria-label="Steps">
        {WIZARD_STEPS.map((w, i) => (
          <li key={w.key} className={w.key === step ? "current" : i < idx ? "done" : ""}>
            <button type="button" disabled={i > idx && !!WIZARD_STEPS.slice(0, i).map((x) => stepProblem(s, x.key, content)).find(Boolean)} onClick={() => go(w.key)} aria-current={w.key === step ? "step" : undefined}>
              <span className="wizard-n">{i + 1}</span>{w.label}
            </button>
          </li>
        ))}
      </ol>
      <fieldset disabled={live} style={{ border: 0, padding: 0, margin: 0 }}>
        {step === "audience" && (
          <div className="wizard-split">
            <div>
              <Field label="Campaign name"><input className="input" value={s.name} onChange={(e) => set({ name: e.target.value })} placeholder="Diwali offer" autoFocus /></Field>
              <Field label="Channel"><div><ChannelLabel channel={s.channel} /> <span className="xs muted">To use another channel, start a new campaign for it.</span></div></Field>
              <SegmentPicker label="Send to members of" hint="Anyone in any of these segments, when it sends." segments={segments.filter((x) => !s.exclude.includes(x.id))} value={s.include} onChange={(include) => set({ include })} />
              <SegmentPicker label="Leave out members of (optional)" hint="Nobody in these segments gets it, even if they're in the audience." segments={segments.filter((x) => !s.include.includes(x.id))} value={s.exclude} onChange={(exclude) => set({ exclude })} />
              <TagEditor value={tags} onChange={setTags} />
            </div>
            <aside className="wizard-aside"><h3 className="small">Who it reaches</h3><AudienceEstimate include={s.include} exclude={s.exclude} channel={s.channel} /></aside>
          </div>
        )}
        {step === "content" && (
          <div>
            <div className="row" style={{ marginBottom: 12 }}>
              <label className="check" style={{ margin: 0 }}><input type="radio" name="mode" checked={s.templateMode === "write"} onChange={() => set({ templateMode: "write" })} />Write it here</label>
              <label className="check" style={{ margin: 0 }}><input type="radio" name="mode" checked={s.templateMode === "pick"} onChange={() => set({ templateMode: "pick" })} disabled={reusableTemplates.length === 0} />Use a saved template{reusableTemplates.length === 0 ? ` (none for ${CHANNEL_NAMES[s.channel]} yet)` : ""}</label>
            </div>
            {s.templateMode === "pick" ? (
              <Field label="Template">
                <select className="select" value={s.pickedTemplate ?? ""} onChange={(e) => set({ pickedTemplate: Number(e.target.value) || undefined })}>
                  <option value="">Choose a template</option>
                  {reusableTemplates.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                </select>
              </Field>
            ) : (
              <>
                <TemplateBody t={t} setT={setT} emailMode={emailMode} switchEmailMode={emailModeSwitcher({ t, setT, emailMode, setEmailMode, builder, fresh: !t.id })}
                  builder={builder} scopeEvent={scopeEvent} setScope={setScope} hadBuilder={false} />
                <label className="check"><input type="checkbox" checked={reusable} onChange={(e) => setReusable(e.target.checked)} />Also save it as a reusable template (it then shows in template lists and journey steps)</label>
              </>
            )}
            {(s.channel === "whatsapp" || s.channel === "rcs") && (
              <Field label="If it can't be delivered, send this SMS instead (optional)">
                <FallbackPick value={fallback} onChange={setFallback} />
              </Field>
            )}
            <ProviderPick channel={s.channel} value={provider} onChange={setProvider} />
            <TopicPick value={topic} onChange={setTopic} />
            <div className="row">
              <button type="button" className="btn" disabled={test.isPending || !content.body && s.templateMode === "write" || s.templateMode === "pick" && !s.pickedTemplate} onClick={() => test.mutate()}>Send a test</button>
              <span className="xs muted">Saves the content, then sends it to a contact or a saved test contact.</span>
            </div>
          </div>
        )}
        {step === "schedule" && (
          <div style={{ maxWidth: 760 }}>
            <Field label="Repeat">
              <select className="select" value={s.repeat} onChange={(e) => set({ repeat: e.target.value, local_time: e.target.value === "none" ? s.local_time : false, ...(e.target.value === "none" ? { end_at: null } : {}) })}>
                {Object.entries(REPEAT_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </Field>
            <div className="row" style={{ marginBottom: 8 }}>
              <button type="button" className="btn sm" onClick={() => set({ start_at: nowISO(), local_time: false })}>Send now</button>
              <span className="xs muted">or choose a time:</span>
            </div>
            <ScheduleFields start={s.start_at} end={s.end_at} onChange={(w) => set({ start_at: w.start_at ?? "", end_at: w.end_at ?? null })} startLabel={s.repeat === "none" ? "Send at" : "First send"} once={s.repeat === "none"} />
            {s.repeat === "none" && (
              <label className="check"><input type="checkbox" checked={!!s.local_time} onChange={(e) => set({ local_time: e.target.checked })} />Send at {localClock(s.start_at)} in each contact's own time zone</label>
            )}
            <DeadlineFields value={s.deadline} onChange={(deadline) => set({ deadline })} localTime={!!s.local_time} zone={browserZone()} />
          </div>
        )}
        {step === "review" && (
          <div className="wizard-split">
            <dl className="review-list">
              <dt>Name</dt><dd>{s.name}{tags.length > 0 && <span className="row" style={{ gap: 4, marginLeft: 8, display: "inline-flex" }}>{tags.map((x) => <span key={x} className="tag-chip">{x}</span>)}</span>}</dd>
              <dt>Channel</dt><dd><ChannelLabel channel={s.channel} />{fallback ? " with an SMS fallback" : ""}</dd>
              <dt>Audience</dt><dd>{s.include.map(segName).join(", ")}{s.exclude.length > 0 && <>, leaving out {s.exclude.map(segName).join(", ")}</>}</dd>
              <dt>Content</dt><dd>{s.templateMode === "pick" ? `Template "${reusableTemplates.find((x) => x.id === s.pickedTemplate)?.name}"` : s.channel === "email" ? `Email "${t.subject}"` : "Written here"}{reusable && s.templateMode === "write" ? ", also saved as a reusable template" : ""}</dd>
              <dt>When</dt><dd>{sendsNow ? "Right away" : s.local_time ? `${fmt(s.start_at)} in each contact's time zone` : REPEAT_LABEL[s.repeat] + ", " + (describeWindow(s.start_at, s.end_at) || fmt(s.start_at))}</dd>
              <dt>Deadline</dt><dd>{describeDeadline(s.deadline, !!s.local_time)}</dd>
            </dl>
            <aside className="wizard-aside"><h3 className="small">Exact audience now</h3><AudienceEstimate include={s.include} exclude={s.exclude} channel={s.channel} /></aside>
          </div>
        )}
      </fieldset>
      <div className="wizard-foot">
        {idx > 0 && <button type="button" className="btn" onClick={() => go(WIZARD_STEPS[idx - 1].key)}>Back</button>}
        <span className="xs" style={{ color: "var(--danger)" }}>{step !== "review" ? problem : ""}</span>
        {step !== "review" ? (
          <button type="button" className="btn primary" disabled={!!problem} onClick={() => go(WIZARD_STEPS[idx + 1].key)}>Next: {WIZARD_STEPS[idx + 1].label}</button>
        ) : !live && (
          <button type="button" className="btn primary" disabled={!!allProblem || busy} title={allProblem ?? undefined} onClick={() => launch.mutate()}>{sendsNow ? "Send now" : "Schedule"}</button>
        )}
      </div>
      {testing && <TestSendModal template={testing} event={scopeEvent} onClose={() => setTesting(null)} />}
    </div>
    </PersonalizeScopeProvider>
  );
}

function FallbackPick({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  const sms = useQuery({ queryKey: ["templates", "sms"], queryFn: () => api.get<{ templates: Template[] }>("/v1/templates?channel=sms") });
  return (
    <select className="select" value={value || ""} onChange={(e) => onChange(Number(e.target.value) || 0)}>
      <option value="">No fallback</option>
      {sms.data?.templates.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
    </select>
  );
}
