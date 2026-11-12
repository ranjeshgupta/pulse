// The delivery funnel and outcome reasons of a message report (broadcasts and journeys).
// The maths lives in funnel.ts.
import type { ReactNode } from "react";
import type { ReasonCount } from "./api";
import {
  barWidth, funnelSteps, REASON_KIND_LABELS, reasonRows, sideFigures, skippedRows, stepReasonGroups, type FunnelData, type ReasonRow,
} from "./funnelMath";
import { ReportPanel } from "./reports";
import { ContactCount } from "./ContactCount";
import type { ContactFilter } from "./contactLinks";

/** Builds the contact list behind a funnel number (outcome: a FUNNEL_OUTCOME value, "enrolled" or skipped_<group>); null: no list. */
export type OutcomeLink = (outcome: string) => ContactFilter[] | null;
/** Builds the contact list behind a reasons row (kind and code, and the journey step). */
export type ReasonLink = (kind: string, code: string, step?: string) => ContactFilter[] | null;
import "./funnel.css";

const REASON_TONE: Record<string, string> = { failed: "warn", bounced: "bad", complained: "bad", suppressed: "", capped: "info" };

/** FunnelPanel: the path from the audience to clicks, with the other outcomes and skips beside it. */
export function FunnelPanel({ funnel, channels, title = "Funnel", link }: { funnel: FunnelData | undefined; channels?: string[]; title?: ReactNode; link?: OutcomeLink }) {
  if (!funnel) return null;
  const steps = funnelSteps(funnel, channels);
  const max = Math.max(1, ...steps.map((s) => s.count ?? 0));
  const side = sideFigures(funnel);
  const skipped = skippedRows(funnel.skipped);
  return (
    <ReportPanel title={title} aside={<span className="xs muted">Unique contacts at each step{link ? "; click a number to see them" : ""}</span>} body>
      <div className="fn-layout">
        <ol className="fn-steps" aria-label="Funnel steps">
          {steps.map((s) => (
            <li key={s.key} className={`fn-step${s.before ? " before" : ""}`}>
              <div className="fn-label">
                <span>{s.label}</span>
                {s.hint && <small className="muted">{s.hint}</small>}
              </div>
              <div className="fn-track" aria-hidden><div className={`fn-fill fn-${s.key}`} style={{ width: `${barWidth(s.count, max)}%` }} /></div>
              <div className="fn-num">
                <strong>{s.count == null ? <span title={s.hint} aria-label="Not reported">—</span> : <ContactCount value={s.count} live filters={link?.(s.key) ?? null} />}</strong>
                <span className="fn-rates">
                  {s.ofPrevious && <span title="Share of the step before">{s.ofPrevious} <span className="faint">of previous</span></span>}
                  {s.ofSent && <span title="Share of messages sent">{s.ofSent} <span className="faint">of sent</span></span>}
                </span>
              </div>
            </li>
          ))}
        </ol>
        <div className="fn-side">
          <dl className="fn-figs">
            {side.map((x) => (
              <div key={x.key} className={x.tone ? `tone-${x.tone}` : undefined}>
                <dt>{x.label}</dt>
                <dd>
                  <strong><ContactCount value={x.count} live filters={link?.(x.key) ?? null} /></strong>
                  {x.count > 0 && <span className="xs muted"> {x.rate} of {x.rateOf}</span>}
                  {x.detail && <small className="muted">{x.detail}</small>}
                </dd>
              </div>
            ))}
          </dl>
          <div className="fn-skips">
            <h4 className="small">Skipped <span className="muted">{skipped.total.toLocaleString("en-IN")}</span></h4>
            {skipped.rows.length === 0 ? <p className="xs muted">No one was skipped.</p> : (
              <ul>
                {skipped.rows.map((r) => (
                  <li key={r.group}>
                    <span><span>{r.label}</span><small className="muted">{r.hint}</small></span>
                    <strong><ContactCount value={r.count} live filters={link?.(`skipped_${r.group}`) ?? null} /></strong>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </div>
    </ReportPanel>
  );
}

function ReasonsTable({ rows, link }: { rows: ReasonRow[]; link?: (kind: string, code: string) => ContactFilter[] | null }) {
  return (
    <div className="table-scroll">
      <table className="table fn-reasons">
        <thead><tr><th scope="col">Reason</th><th scope="col">Kind</th><th scope="col" className="num">Contacts</th><th scope="col" className="fn-share-col">Share</th></tr></thead>
        <tbody>{rows.map((r) => (
          <tr key={`${r.kind}:${r.code}`}>
            <td title={r.code}>{r.label}</td>
            <td><span className={`badge ${REASON_TONE[r.kind] ?? ""}`}>{REASON_KIND_LABELS[r.kind] ?? r.kind}</span></td>
            <td className="num"><ContactCount value={r.contacts} live filters={link?.(r.kind, r.code) ?? null} /></td>
            <td>
              <div className="fn-share">
                <div className="fn-track" aria-hidden><div className={`fn-fill reason-${r.kind}`} style={{ width: `${r.share * 100}%` }} /></div>
                <span className="xs">{r.shareText}</span>
              </div>
            </td>
          </tr>
        ))}</tbody>
      </table>
    </div>
  );
}

/** ReasonsPanel: why messages failed, bounced or were skipped, most contacts first. */
export function ReasonsPanel({ reasons, title = "Why messages didn't go out", link }: { reasons: ReasonCount[] | undefined; title?: ReactNode; link?: ReasonLink }) {
  const { rows, total } = reasonRows(reasons);
  return (
    <ReportPanel title={title} aside={total > 0 ? <span className="xs muted">{total.toLocaleString("en-IN")} contacts</span> : undefined} body={rows.length === 0}>
      {rows.length === 0 ? <p className="muted small">No failures, bounces or skips.</p> : <ReasonsTable rows={rows} link={link && ((k, c) => link(k, c))} />}
    </ReportPanel>
  );
}

/** StepReasons: the reasons per journey step. label names a step; order (step numbers) breaks ties. */
export function StepReasons({ byNode, label, order, onStep, canOpen, link }: {
  byNode: Record<string, ReasonCount[]> | undefined; label: (stepId: string) => ReactNode; order?: Record<string, number>;
  onStep?: (stepId: string) => void; canOpen?: (stepId: string) => boolean; link?: ReasonLink;
}) {
  const groups = stepReasonGroups(byNode, order);
  return (
    <ReportPanel title="Why messages didn't go out, by step" body={groups.length === 0}>
      {groups.length === 0 ? <p className="muted small">No failures, bounces or skips at any step.</p> : groups.map((g) => (
        <div key={g.id} className="fn-step-reasons">
          <div className="fn-step-head">
            <h4 className="small">{label(g.id)} <span className="muted xs">{g.total.toLocaleString("en-IN")} contacts</span></h4>
            {onStep && (canOpen?.(g.id) ?? true) && <button type="button" className="btn ghost sm" onClick={() => onStep(g.id)}>Open step</button>}
          </div>
          <ReasonsTable rows={g.rows} link={link && ((k, c) => link(k, c, g.id))} />
        </div>
      ))}
    </ReportPanel>
  );
}
