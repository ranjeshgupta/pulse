// Surveys and NPS (O6): build a survey, put its link in any message ({{survey.ID}}, or the 0–10 row
// for email), share a public link, and read the results: NPS, its trend, every question's answers.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, can } from "../api";
import { useMe } from "../auth";
import { askDelete, useEditGuard } from "../guard";
import { NPS_STARTER, newQuestion, npsSnippet, npsTone, surveyProblem, TYPE_LABEL, type QuestionType, type Survey, type SurveyQuestion } from "../surveys";
import { Empty, ErrorNote, Field, PageHead, timeAgo, useCopy, useToast } from "../ui";
import "./surveys.css";

const STATUS_LABEL = { draft: "Draft", live: "Taking answers", closed: "Closed" } as const;

export function SurveyList() {
  const q = useQuery({ queryKey: ["surveys"], queryFn: () => api.get<{ surveys: Survey[] }>("/v1/surveys") });
  const canEdit = can(useMe().data?.permissions, "content", "edit");
  const list = q.data?.surveys ?? [];
  return (
    <div className="page">
      <PageHead title="Surveys" intro="Ask for an NPS score, a rating, a choice or a comment. Put a survey's link in any message: each contact gets their own, and their answer lands on their profile as survey_answered, ready for segments and journeys."
        actions={canEdit ? <Link className="btn primary" to="/surveys/new">New survey</Link> : undefined} />
      <ErrorNote error={q.error} />
      {list.length === 0 && !q.isLoading ? (
        <div className="panel"><Empty title="No surveys yet">An NPS survey after a purchase or a support ticket is a good first one.</Empty></div>
      ) : (
        <div className="panel"><div className="table-scroll"><table className="table">
          <thead><tr><th>Survey</th><th>Status</th><th>Questions</th><th>Changed</th></tr></thead>
          <tbody>{list.map((s) => (
            <tr key={s.id}>
              <td><Link to={`/surveys/${s.id}`}><strong>📋 {s.name}</strong></Link><div className="xs faint"><code>{`{{survey.${s.id}}}`}</code></div></td>
              <td><span className={`badge ${s.status === "live" ? "ok" : ""}`}>{STATUS_LABEL[s.status]}</span></td>
              <td className="small">{s.questions.length}</td>
              <td className="small muted">{s.updated_at ? timeAgo(s.updated_at) : ""}</td>
            </tr>
          ))}</tbody>
        </table></div></div>
      )}
    </div>
  );
}

interface Links { public: string; variable: string }

export function SurveyEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const copy = useCopy();
  const canEdit = can(useMe().data?.permissions, "content", "edit");
  const existing = useQuery({ queryKey: ["survey", id], queryFn: () => api.get<{ survey: Survey; links: Links }>(`/v1/surveys/${id}`), enabled: !isNew });
  const [draft, setDraft] = useState<Survey | null>(null);
  const [tab, setTab] = useState<"questions" | "results">(isNew ? "questions" : "results");
  const s: Survey = draft ?? existing.data?.survey ?? { id: 0, name: "", status: "draft", questions: NPS_STARTER, thank_you: "" };
  const problem = surveyProblem(s);
  const save = useMutation({
    mutationFn: () => (isNew ? api.post<{ id: number }>("/v1/surveys", s) : api.put<{ id: number }>(`/v1/surveys/${id}`, s)),
    onSuccess: (r) => {
      guard.saved(); setDraft(null);
      qc.invalidateQueries({ queryKey: ["surveys"] }); qc.invalidateQueries({ queryKey: ["survey", id] }); qc.invalidateQueries({ queryKey: ["survey-report"] });
      toast({ text: `Saved ${s.name}` });
      if (isNew) nav(`/surveys/${r.id}`, { replace: true });
    },
  });
  const guard = useEditGuard(canEdit && !problem ? () => save.mutate() : undefined);
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/surveys/${id}`),
    onSuccess: () => { guard.saved(); qc.invalidateQueries({ queryKey: ["surveys"] }); toast({ text: `Deleted ${s.name}` }); nav("/surveys"); },
  });
  const set = (p: Partial<Survey>) => { setDraft({ ...s, ...p }); guard.markDirty(); };
  const setQ = (i: number, p: Partial<SurveyQuestion>) => set({ questions: s.questions.map((q, j) => (j === i ? { ...q, ...p } : q)) });
  const move = (i: number, d: -1 | 1) => { const qs = [...s.questions]; [qs[i], qs[i + d]] = [qs[i + d], qs[i]]; set({ questions: qs }); };
  const links = existing.data?.links;
  if (!isNew && existing.isLoading) return <div className="page" />;
  return (
    <div className="page">
      <PageHead back={<Link className="back" to="/surveys">Surveys</Link>} title={isNew ? "New survey" : s.name || "Survey"}
        actions={canEdit ? <>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => void askDelete(s.name, "Its answers are deleted too; the survey_answered events on contacts stay.").then((ok) => ok && remove.mutate())}>Delete</button>}
          <button className="btn primary" disabled={!!problem || save.isPending || (!draft && !isNew)} title={problem ?? undefined} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : "Save"}</button>
        </> : undefined} />
      <ErrorNote error={existing.error ?? save.error ?? remove.error} />
      {!isNew && (
        <div className="tabs" role="tablist" aria-label="Survey">
          <button type="button" role="tab" aria-selected={tab === "results"} onClick={() => setTab("results")}>Results</button>
          <button type="button" role="tab" aria-selected={tab === "questions"} onClick={() => setTab("questions")}>Questions and links</button>
        </div>
      )}
      {tab === "results" && !isNew ? <SurveyResults id={Number(id)} /> : (
        <fieldset disabled={!canEdit} className="survey-form">
          <div className="grid-2">
            <Field label="Name (shown as the page title)"><input className="input" autoFocus={isNew} value={s.name} placeholder="How did we do?" onChange={(e) => set({ name: e.target.value })} /></Field>
            <Field label="Status" hint={s.status === "live" ? "Links take answers." : "Links show that the survey isn't taking answers."}>
              <select className="select" value={s.status} onChange={(e) => set({ status: e.target.value as Survey["status"] })}>
                {Object.entries(STATUS_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </Field>
          </div>
          <Field label="Intro (optional)"><input className="input" value={s.intro ?? ""} onChange={(e) => set({ intro: e.target.value })} /></Field>
          <h3 style={{ margin: "18px 0 10px" }}>Questions</h3>
          {s.questions.map((q, i) => (
            <div key={i} className="panel survey-q">
              <div className="survey-q-head">
                <span className="badge info">{TYPE_LABEL[q.type]}</span>
                <span className="xs faint">id <code>{q.id}</code> · answers are <code>q_{q.id}</code> on survey_answered</span>
                <span className="survey-q-actions">
                  <button type="button" className="btn ghost sm" disabled={i === 0} aria-label="Move up" onClick={() => move(i, -1)}>↑</button>
                  <button type="button" className="btn ghost sm" disabled={i === s.questions.length - 1} aria-label="Move down" onClick={() => move(i, 1)}>↓</button>
                  <button type="button" className="btn ghost sm" aria-label={`Remove question ${i + 1}`}
                    onClick={() => void askDelete(`question ${i + 1}`, "Answers already given to it stay in the results.").then((ok) => ok && set({ questions: s.questions.filter((_, j) => j !== i) }))}>Remove</button>
                </span>
              </div>
              <input className="input" aria-label={`Question ${i + 1}`} value={q.text} onChange={(e) => setQ(i, { text: e.target.value })} />
              {q.type === "choice" && (
                <Field label="Options (one per line)">
                  <textarea className="textarea" rows={3} value={(q.options ?? []).join("\n")} onChange={(e) => setQ(i, { options: e.target.value.split("\n") })} />
                </Field>
              )}
              <label className="check"><input type="checkbox" checked={!!q.required} onChange={(e) => setQ(i, { required: e.target.checked })} />Must be answered</label>
            </div>
          ))}
          {s.questions.length < 10 && (
            <div className="row" style={{ gap: 8 }}>
              <span className="small muted">Add:</span>
              {(Object.keys(TYPE_LABEL) as QuestionType[]).filter((t) => t !== "nps" || !s.questions.some((q) => q.type === "nps")).map((t) => (
                <button key={t} type="button" className="btn sm" onClick={() => set({ questions: [...s.questions, newQuestion(t, s.questions.map((q) => q.id))] })}>{TYPE_LABEL[t]}</button>
              ))}
            </div>
          )}
          <Field label="Thank-you message"><input className="input" value={s.thank_you ?? ""} placeholder="Thanks! Your answer helps us get better." onChange={(e) => set({ thank_you: e.target.value })} /></Field>
          {problem && draft && <div className="alert info">{problem}</div>}
          {links && (
            <section className="panel survey-links">
              <div className="panel-head"><h3>Use it</h3></div>
              <div className="panel-body">
                {s.status !== "live" && <div className="alert info">It's {s.status === "draft" ? "a draft" : "closed"}: its links say it isn't taking answers. Set the status to “Taking answers” and save when it's ready.</div>}
                <p className="small"><strong>In any message</strong> (each contact gets their own link): <code>{links.variable}</code>{" "}
                  <button type="button" className="btn ghost sm" onClick={() => copy(links.variable, "Copied: paste it as a link in a template")}>Copy</button></p>
                {s.questions.some((q) => q.type === "nps") && (
                  <p className="small"><strong>NPS row for email</strong>: 0–10 buttons; a click opens the survey with that score picked (it's saved when they press Send, so link scanners never answer).{" "}
                    <button type="button" className="btn ghost sm" onClick={() => copy(npsSnippet(s.id), "Copied: paste it into the email's HTML")}>Copy HTML</button></p>
                )}
                <p className="small"><strong>Public link</strong> (anyone, answers aren't tied to a contact): <a href={links.public} target="_blank" rel="noreferrer">{links.public}</a>{" "}
                  <button type="button" className="btn ghost sm" onClick={() => copy(links.public, "Link copied")}>Copy</button></p>
                <p className="xs muted">Start a triggered campaign from <code>survey_answered</code> with <code>nps_group</code> = detractor to follow up the same day.</p>
              </div>
            </section>
          )}
        </fieldset>
      )}
    </div>
  );
}

interface Report {
  responses: number; days: number; capped: boolean;
  nps: { score: number; answers: number; distribution: number[]; promoters: number; passives: number; detractors: number; trend: { week: string; nps: number; answers: number }[] | null };
  questions: { id: string; type: string; text: string; answers: number; average?: number; counts?: Record<string, number>; latest?: { at: string; external_id?: string; text: string; nps?: number }[] }[];
}

function SurveyResults({ id }: { id: number }) {
  const [days, setDays] = useState(90);
  const q = useQuery({ queryKey: ["survey-report", id, days], queryFn: () => api.get<Report>(`/v1/surveys/${id}/report?days=${days}`), refetchInterval: 30_000 });
  const r = q.data;
  const max = Math.max(1, ...(r?.nps.distribution ?? [0]));
  return (
    <div className="survey-results">
      <div className="row" style={{ marginBottom: 12 }}>
        <div className="seg-toggle" role="group" aria-label="Period">
          {[30, 90, 365].map((d) => <button key={d} type="button" aria-pressed={days === d} onClick={() => setDays(d)}>{d} days</button>)}
        </div>
        {r && <span className="small muted">{r.responses.toLocaleString("en-IN")} response{r.responses === 1 ? "" : "s"}{r.capped ? " (the latest 20,000)" : ""}</span>}
      </div>
      <ErrorNote error={q.error} />
      {r && r.responses === 0 && <div className="panel"><Empty title="No answers yet">Set the survey to “Taking answers” and put its link in a message.</Empty></div>}
      {r && r.nps.answers > 0 && (
        <section className="panel"><div className="panel-body survey-nps">
          <div className={`survey-score ${npsTone(r.nps.score)}`}><strong>{Math.round(r.nps.score)}</strong><span>NPS</span></div>
          <div className="survey-split">
            {(["promoters", "passives", "detractors"] as const).map((k) => (
              <div key={k}><strong>{Math.round((r.nps[k] / r.nps.answers) * 100)}%</strong><span>{k} ({r.nps[k].toLocaleString("en-IN")})</span></div>
            ))}
          </div>
          <div className="survey-dist" aria-label="Answers by score">
            {r.nps.distribution.map((n, sc) => (
              <div key={sc} className="survey-bar" title={`${sc}: ${n}`}>
                <span className={sc >= 9 ? "pro" : sc >= 7 ? "pas" : "det"} style={{ height: `${(n / max) * 100}%` }} />
                <small>{sc}</small>
              </div>
            ))}
          </div>
          {(r.nps.trend?.length ?? 0) > 1 && (
            <p className="small muted" style={{ gridColumn: "1 / -1", margin: 0 }}>By week: {r.nps.trend!.map((t) => `${t.week.slice(5)} ${Math.round(t.nps)} (${t.answers})`).join(" · ")}</p>
          )}
        </div></section>
      )}
      {r?.questions.filter((x) => x.type !== "nps" && x.answers > 0).map((x) => (
        <section key={x.id} className="panel" style={{ marginTop: 12 }}>
          <div className="panel-head"><h3>{x.text}</h3><span className="small muted">{x.answers} answer{x.answers === 1 ? "" : "s"}{x.type === "rating" && x.average ? ` · average ${x.average.toFixed(1)} ★` : ""}</span></div>
          <div className="panel-body">
            {x.type === "text" ? (
              <ul className="survey-comments">{(x.latest ?? []).map((a, i) => (
                <li key={i}><p>{a.text}</p><span className="xs muted">{a.nps != null ? `NPS ${a.nps} · ` : ""}{a.external_id ? <Link to={`/contacts/${encodeURIComponent(a.external_id)}`}>{a.external_id}</Link> : "public link"} · {timeAgo(a.at)}</span></li>
              ))}</ul>
            ) : (
              <div className="survey-counts">{Object.entries(x.counts ?? {}).sort((a, b) => b[1] - a[1]).map(([k, n]) => (
                <div key={k}><span>{x.type === "rating" ? `${k} ★` : k}</span><span className="survey-count-bar"><span style={{ width: `${(n / x.answers) * 100}%` }} /></span><strong>{n}</strong></div>
              ))}</div>
            )}
          </div>
        </section>
      ))}
    </div>
  );
}
