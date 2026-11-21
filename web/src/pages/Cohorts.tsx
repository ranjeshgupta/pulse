import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, can, MAX_PERIODS, type CohortResult, type CohortSpec, type Period, type SavedReport } from "../api";
import { useMe } from "../auth";
import {
  averageCell, cell, clampInt, cohortLabel, cohortTitle, columnLabel, comingBack, formedBy, heat, lastBounds, MAX_RECENT,
  periodsIssue, periodsText, rangeIssue, unit, type Cell,
} from "../cohorts";
import { count, defaultRange, EVENT_RE, FilterRows, filterProblems, fromFilter, pct, RangePicker, rangeBounds, rangeProblem, SegmentPicker, toFilter, useDebounced, type Range } from "../insights";
import type { Where } from "../segmentRules";
import { Empty, ErrorNote, Field, PageHead, timeAgo, useToast } from "../ui";
import { askDelete, useEditGuard } from "../guard";
import "./cohorts.css";

type Cohort = SavedReport<CohortSpec>;

const PERIODS: Period[] = ["day", "week", "month"];
const EVERY: Record<Period, string> = { day: "Daily", week: "Weekly", month: "Monthly" };

// ---- list ----

export function CohortList() {
  const q = useQuery({ queryKey: ["cohorts"], queryFn: () => api.get<{ cohorts: Cohort[] }>("/v1/cohorts") });
  const cs = q.data?.cohorts ?? [];
  return (
    <div className="page">
      <PageHead
        title="Cohorts"
        intro="Group contacts by when they joined or first did something, and see how many keep coming back."
        actions={<Link className="btn primary" to="/cohorts/new">New cohort report</Link>}
      />
      <ErrorNote error={q.error} />
      <div className="panel">
        {cs.length === 0 && !q.isLoading ? (
          <Empty title="No cohort reports yet" action={<Link className="btn primary" to="/cohorts/new">Create a cohort report</Link>}>
            Of the people who signed up each week, how many came back a week later? A month later? Retention reports answer that, cohort by cohort.
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th>Cohorts</th><th>Followed for</th><th>Coming back</th><th>Updated</th></tr></thead>
            <tbody>{cs.map((c) => (
              <tr key={c.id}>
                <td><Link to={`/cohorts/${c.id}`}>{c.name}</Link></td>
                <td className="muted">{formedBy(c.spec)}</td>
                <td>{EVERY[c.spec.period]}, {periodsText(c.spec.period, c.spec.periods)}</td>
                <td className="muted">{comingBack(c.spec)}</td>
                <td className="muted">{c.updated_at ? timeAgo(c.updated_at) : ""}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// ---- editing model ----

type Draft = {
  by: CohortSpec["by"]; event: string; where: Where[];
  period: Period; periods: number;
  returnMode: "any" | "event"; returnEvent: string; returnWhere: Where[];
  segmentId: number;
};

const NEW_DRAFT: Draft = { by: "first_seen", event: "signup", where: [], period: "week", periods: 8, returnMode: "any", returnEvent: "", returnWhere: [], segmentId: 0 };

function fromSpec(s: CohortSpec): Draft {
  return {
    by: s.by, event: s.event ?? "", where: (s.where ?? []).map(fromFilter),
    period: s.period, periods: s.periods,
    returnMode: s.return.event ? "event" : "any", returnEvent: s.return.event ?? "", returnWhere: (s.return.where ?? []).map(fromFilter),
    segmentId: s.segment_id ?? 0,
  };
}

function toSpec(d: Draft): CohortSpec {
  const s: CohortSpec = { by: d.by, period: d.period, periods: d.periods, return: {} };
  if (d.by === "event") {
    s.event = d.event.trim();
    if (d.where.length) s.where = d.where.map(toFilter);
  }
  if (d.returnMode === "event") {
    s.return.event = d.returnEvent.trim();
    if (d.returnWhere.length) s.return.where = d.returnWhere.map(toFilter);
  }
  if (d.segmentId) s.segment_id = d.segmentId;
  return s;
}

function draftProblems(d: Draft): string[] {
  const out: string[] = [];
  if (d.by === "event") {
    if (!EVENT_RE.test(d.event.trim())) out.push("Choose the event that starts a cohort, e.g. signup.");
    out.push(...filterProblems("Cohort event", d.where));
  }
  if (d.returnMode === "event") {
    if (!EVENT_RE.test(d.returnEvent.trim())) out.push("Choose the event that counts as coming back, e.g. purchase.");
    out.push(...filterProblems("Coming back", d.returnWhere));
  }
  const p = periodsIssue(d.period, d.periods);
  if (p) out.push(p);
  return [...new Set(out)];
}

/** Which cohorts to show: the last N periods, or those starting in a date range. Not saved. */
type Show = { mode: "last"; last: number } | { mode: "range"; range: Range };

function showBounds(show: Show, period: Period): { from: string; to: string } {
  return show.mode === "last" ? lastBounds(period, show.last) : rangeBounds(show.range);
}

function showProblem(show: Show, period: Period): string | null {
  if (show.mode === "last") {
    const max = MAX_RECENT[period];
    if (!(Number.isInteger(show.last) && show.last >= 1 && show.last <= max)) return `Show 1 to ${max} ${unit(period, 2).toLowerCase()}.`;
    return null;
  }
  const b = rangeProblem(show.range);
  if (b) return b;
  const { from, to } = rangeBounds(show.range);
  return rangeIssue(period, from, to);
}

// ---- editor ----

export function CohortEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const canEdit = can(useMe().data?.permissions, "reports", "edit");
  const existing = useQuery({ queryKey: ["cohort", id], queryFn: () => api.get<Cohort>(`/v1/cohorts/${id}`), enabled: !isNew });
  const eventNames = useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names"), staleTime: 60_000 });

  const [name, setName] = useState("");
  const [draft, setDraft] = useState<Draft>(NEW_DRAFT);
  const [show, setShow] = useState<Show>({ mode: "last", last: 8 });
  const [loaded, setLoaded] = useState(isNew);

  useEffect(() => {
    if (existing.data && !loaded) {
      setName(existing.data.name);
      setDraft(fromSpec(existing.data.spec));
      setLoaded(true);
    }
  }, [existing.data, loaded]);

  const issues = useMemo(() => draftProblems(draft), [draft]);
  const showIssue = showProblem(show, draft.period);

  // Run live: debounce the request body, and only run a report the server will accept.
  const runKey = issues.length || showIssue ? "" : JSON.stringify({ spec: toSpec(draft), ...showBounds(show, draft.period) });
  const debounced = useDebounced(runKey);
  const run = useQuery({
    queryKey: ["cohort-run", debounced],
    queryFn: () => api.post<CohortResult>("/v1/cohorts/run", JSON.parse(debounced)),
    enabled: debounced !== "",
    placeholderData: (prev) => prev,
  });

  const body = () => ({ name: name.trim(), spec: toSpec(draft) });
  const save = useMutation({
    mutationFn: () => (isNew ? api.post<{ id: number }>("/v1/cohorts", body()) : api.put<{ id: number }>(`/v1/cohorts/${id}`, body())),
    onSuccess: (r) => {
      const savedId = isNew ? r.id : Number(id);
      guard.saved();
      qc.invalidateQueries({ queryKey: ["cohorts"] });
      qc.invalidateQueries({ queryKey: ["cohort", String(savedId)] });
      toast({ text: isNew ? "Cohort report created" : "Cohort report saved" });
      if (isNew) nav(`/cohorts/${savedId}`, { replace: true });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/cohorts/${id}`),
    onSuccess: () => { guard.saved(); qc.invalidateQueries({ queryKey: ["cohorts"] }); toast({ text: `Deleted ${existing.data?.name || "the cohort report"}` }); nav("/cohorts"); },
  });
  // Changes to the report's definition (not the range shown) are a draft until saved: leaving asks
  // first, for people who can save it; Ctrl/⌘+S saves.
  const saveBlocked = !name.trim() || issues.length > 0 || save.isPending;
  const guard = useEditGuard(canEdit && !saveBlocked ? () => save.mutate() : undefined);
  const edited = () => { if (canEdit) guard.markDirty(); };

  if (!loaded) return <div className="page"><ErrorNote error={existing.error} /></div>;

  const set = (patch: Partial<Draft>) => { edited(); setDraft({ ...draft, ...patch }); };
  const setPeriod = (period: Period) => {
    set({ period, periods: clampInt(draft.periods, MAX_PERIODS[period]) });
    if (show.mode === "last") setShow({ mode: "last", last: clampInt(show.last, MAX_RECENT[period]) });
  };
  const saveProblem = !name.trim() ? "Name the report." : issues[0];
  const plural = unit(draft.period, 2).toLowerCase();

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/cohorts">Cohorts</Link>}
        title={isNew ? "New cohort report" : name || "Cohort report"}
        intro={canEdit ? undefined : "You can run this report; saving it needs edit access to reports."}
        actions={canEdit ? <>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => void askDelete(existing.data?.name || "this cohort report", "The saved report goes; contacts and events aren't touched. This can't be undone.").then((ok) => ok && remove.mutate())}>Delete</button>}
          <button className="btn primary" disabled={!!saveProblem || save.isPending} title={saveProblem} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : isNew ? "Create report" : "Save report"}</button>
        </> : undefined}
      />
      <ErrorNote error={save.error ?? remove.error ?? existing.error} />
      <div className="cohort-split">
        <div>
          <Field label="Name"><input className="input" value={name} onChange={(e) => { edited(); setName(e.target.value); }} placeholder="Weekly signups coming back" autoFocus={isNew} /></Field>

          <div className="panel">
            <div className="panel-head"><h3>Group contacts by</h3></div>
            <div className="panel-body">
              <Toggle
                label="Group contacts by"
                value={draft.by}
                options={[["first_seen", "When they were first seen"], ["event", "The first time they did an event"]]}
                onChange={(by) => set({ by })}
              />
              {draft.by === "event" ? (
                <div className="cohort-rule">
                  <div className="rule-row">
                    <input className="input event" list="cohort-events" placeholder="event, e.g. signup" value={draft.event} onChange={(e) => set({ event: e.target.value })} aria-label="Event that starts a cohort" />
                  </div>
                  <FilterRows where={draft.where} onChange={(where) => set({ where })} />
                  <p className="xs muted" style={{ margin: "8px 0 0" }}>Each contact joins the cohort of the {unit(draft.period).toLowerCase()} they first did it in the date range.</p>
                </div>
              ) : (
                <p className="xs muted" style={{ margin: "10px 0 0" }}>Each contact joins the cohort of the {unit(draft.period).toLowerCase()} Pulse first saw them.</p>
              )}
            </div>
          </div>

          <div className="panel" style={{ marginTop: 16 }}>
            <div className="panel-head"><h3>Counts as coming back</h3></div>
            <div className="panel-body">
              <Toggle
                label="Counts as coming back"
                value={draft.returnMode}
                options={[["any", "Any activity"], ["event", "Doing an event"]]}
                onChange={(returnMode) => set({ returnMode })}
              />
              {draft.returnMode === "event" ? (
                <div className="cohort-rule">
                  <div className="rule-row">
                    <input className="input event" list="cohort-events" placeholder="event, e.g. purchase" value={draft.returnEvent} onChange={(e) => set({ returnEvent: e.target.value })} aria-label="Event that counts as coming back" />
                  </div>
                  <FilterRows where={draft.returnWhere} onChange={(returnWhere) => set({ returnWhere })} />
                </div>
              ) : (
                <p className="xs muted" style={{ margin: "10px 0 0" }}>Any event the contact does. Messages Pulse sends them and other bookkeeping don't count.</p>
              )}
            </div>
          </div>
          <datalist id="cohort-events">{eventNames.data?.names.map((n) => <option key={n} value={n} />)}</datalist>
          {issues.length > 0 && <ul className="small muted" style={{ marginTop: 10 }}>{issues.map((p) => <li key={p}>{p}</li>)}</ul>}
        </div>

        <aside className="panel cohort-side">
          <div className="panel-body cohort-controls">
            <Toggle label="Period" value={draft.period} options={PERIODS.map((p): [Period, string] => [p, EVERY[p]])} onChange={setPeriod} />
            <div>
              <p className="small muted cohort-label">Follow each cohort for</p>
              <div className="cohort-inline">
                <input
                  className="input" type="number" min={1} max={MAX_PERIODS[draft.period]} value={draft.periods || ""}
                  onChange={(e) => set({ periods: e.target.value === "" ? 0 : clampInt(Number(e.target.value), MAX_PERIODS[draft.period]) })}
                  aria-label={`Number of ${plural} to follow`}
                />
                <span className="small">{plural} <span className="muted">(up to {MAX_PERIODS[draft.period]})</span></span>
              </div>
            </div>

            <div>
              <Toggle
                label="Cohorts to show"
                value={show.mode}
                options={[["last", "Most recent"], ["range", "Starting between dates"]]}
                onChange={(mode) => setShow(mode === "last" ? { mode, last: 8 } : { mode, range: defaultRange(90) })}
              />
              <div style={{ marginTop: 10 }}>
                {show.mode === "last" ? (
                  <div className="cohort-inline">
                    <span className="small">Last</span>
                    <input
                      className="input" type="number" min={1} max={MAX_RECENT[draft.period]} value={show.last || ""}
                      onChange={(e) => setShow({ mode: "last", last: e.target.value === "" ? 0 : clampInt(Number(e.target.value), MAX_RECENT[draft.period]) })}
                      aria-label={`Number of recent ${plural} to show`}
                    />
                    <span className="small">{plural}</span>
                  </div>
                ) : (
                  <RangePicker range={show.range} onChange={(range) => setShow({ mode: "range", range })} label="Cohorts starting in" />
                )}
              </div>
              {showIssue && <p className="xs cohort-problem" style={{ marginTop: 6 }}>{showIssue}</p>}
            </div>

            <SegmentPicker value={draft.segmentId} onChange={(segmentId) => set({ segmentId })} />
            <Headline result={run.data} />
          </div>
        </aside>
      </div>

      <Results
        result={run.data}
        ready={debounced !== "" && runKey !== ""}
        stale={debounced !== runKey || run.isFetching}
        error={run.error}
        spec={toSpec(draft)}
      />
    </div>
  );
}

/** Toggle is a row of pressed/unpressed buttons (.seg-toggle) choosing one value. */
function Toggle<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: [T, string][]; onChange: (v: T) => void }) {
  return (
    <div>
      <p className="small muted cohort-label">{label}</p>
      <div className="seg-toggle" role="group" aria-label={label}>
        {options.map(([v, text]) => <button key={v} type="button" aria-pressed={value === v} onClick={() => onChange(v)}>{text}</button>)}
      </div>
    </div>
  );
}

/** Headline: average retention one period in. */
function Headline({ result }: { result?: CohortResult }) {
  const share = result?.average[1];
  if (!result || share === null || share === undefined) return null;
  return (
    <div className="cohort-headline">
      <p className="small muted" style={{ margin: "0 0 4px" }}>Back in {columnLabel(result.period, 1).toLowerCase()}, on average</p>
      <div className="big">{pct(share)}</div>
    </div>
  );
}

function Results({ result, ready, stale, error, spec }: { result?: CohortResult; ready: boolean; stale: boolean; error: unknown; spec: CohortSpec }) {
  // The query keeps the previous result as placeholder data, so it stays on screen (dimmed) while a
  // new one is counted or while the report has problems.
  const r = result;
  const busy = stale || !ready;
  return (
    <section className="panel cohort-results" aria-busy={busy}>
      {error && !stale ? <div className="panel-body"><ErrorNote error={error} /></div> : null}
      {!r ? (
        <div className="panel-body"><p className="muted small" style={{ margin: 0 }}>{ready ? "Counting…" : "Fix the problems above to see the cohorts."}</p></div>
      ) : (
        <div className={busy ? "busy" : undefined}>
          <Triangle result={r} spec={spec} />
          <div className="panel-body" style={{ borderTop: "1px solid var(--rule)" }}>
            <p className="xs muted" style={{ margin: 0 }}>
              {columnLabel(r.period, 0)} is the {unit(r.period).toLowerCase()} each cohort started, so it counts contacts who came back in that same {unit(r.period).toLowerCase()}
              {spec.by === "first_seen" && spec.return.event === undefined ? " (with any activity that's everyone)" : ""}.{" "}
              {r.period === "week" ? "Weeks start on Monday. " : ""}All {unit(r.period, 2).toLowerCase()} are in UTC. – means that {unit(r.period).toLowerCase()} hasn't started yet, or the cohort is empty.
            </p>
          </div>
        </div>
      )}
    </section>
  );
}

function Triangle({ result: r, spec }: { result: CohortResult; spec: CohortSpec }) {
  const columns = r.average.map((_, i) => i);
  const total = r.cohorts.reduce((n, c) => n + c.size, 0);
  const what = spec.return.event ? `did ${spec.return.event}` : "came back";
  return (
    <div className="table-scroll">
      <table className="table cohort-table">
        <caption>
          <strong>Retention by {unit(r.period).toLowerCase()}</strong>
          Share of each cohort who {what} in each {unit(r.period).toLowerCase()} after it started{total === 0 ? ". No contacts are in these cohorts yet." : "."}
        </caption>
        <thead>
          <tr>
            <th scope="col">Cohort</th>
            <th scope="col" className="num">Contacts</th>
            {columns.map((i) => <th scope="col" className="num" key={i}>{columnLabel(r.period, i)}</th>)}
          </tr>
        </thead>
        <tbody>
          <tr className="cohort-all">
            <th scope="row">All cohorts</th>
            <td className="num">{count(total)}</td>
            {r.average.map((a, i) => <CellTd key={i} cell={averageCell(a)} title={a === null ? undefined : `${pct(a)} of contacts in cohorts that reached ${columnLabel(r.period, i)}`} />)}
          </tr>
          {r.cohorts.map((c) => (
            <tr key={c.start}>
              <th scope="row" title={cohortTitle(c.start, r.period)}>{cohortLabel(c.start, r.period)}</th>
              <td className="num">{count(c.size)}</td>
              {columns.map((i) => {
                const x = cell(c.returned[i], c.size);
                return <CellTd key={i} cell={x} title={x.kind === "value" ? `${count(x.count ?? 0)} of ${count(c.size)} contacts` : undefined} />;
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CellTd({ cell: c, title }: { cell: Cell; title?: string }) {
  if (c.kind !== "value") {
    return (
      <td className="cohort-cell none">
        <span aria-hidden>–</span>
        <span className="cohort-sr">{c.kind === "pending" ? "Not reached yet" : "No contacts"}</span>
      </td>
    );
  }
  return (
    <td className="cohort-cell" style={heat(c.share)} title={title}>
      {pct(c.share)}
      {c.count !== undefined && <span className="cohort-count">{count(c.count)}</span>}
    </td>
  );
}
