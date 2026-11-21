// Customer value (O6): buyers grouped by recency, frequency and spend (RFM) with each group's churn
// risk, revenue and habits; every number opens those contacts, and any group becomes a segment.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { api, can } from "../api";
import { useMe } from "../auth";
import { ContactCount } from "../ContactCount";
import { fold, RISK_LABEL, segmentRule, type CVResponse } from "../customerValue";
import { money } from "../insights";
import { ErrorNote, PageHead, useToast } from "../ui";
import "./customer-value.css";

export function CustomerValue() {
  const q = useQuery({ queryKey: ["customer-value"], queryFn: () => api.get<CVResponse>("/v1/revenue/customer-value"), refetchInterval: 300_000 });
  const perms = useMe().data?.permissions;
  const canSegment = can(perms, "segments", "edit");
  const nav = useNavigate();
  const toast = useToast();
  const make = useMutation({
    mutationFn: (g: { group: string; risk?: string }) => api.post<{ id: number }>("/v1/segments", {
      name: g.risk ? `${g.group}: ${RISK_LABEL[g.risk] ?? g.risk} churn risk` : `Customer value: ${g.group}`, rule: segmentRule(g.group, g.risk),
    }),
    onSuccess: (r) => { toast({ text: "Segment created" }); nav(`/segments/${r.id}`); },
  });
  const qc = useQueryClient();
  const score = useMutation({
    mutationFn: () => api.post<{ started: boolean }>("/v1/revenue/customer-value/refresh", {}),
    onSuccess: () => {
      toast({ text: "Scoring started: groups update here as buyers are scored" });
      // Changes reach the contacts through the event pipeline, at a steady rate for big workspaces.
      for (const ms of [4000, 15000, 60000]) window.setTimeout(() => qc.invalidateQueries({ queryKey: ["customer-value"] }), ms);
    },
  });
  const d = q.data;
  const f = d ? fold(d) : null;
  const cur = d?.settings.currency;
  return (
    <div className="page">
      <PageHead title="Customer value"
        actions={<button type="button" className="btn" disabled={score.isPending} title="Scored every day on its own; this scores now (once every 5 minutes)" onClick={() => score.mutate()}>{score.isPending ? "Scoring…" : "Score now"}</button>}
        intro="Your buyers grouped by how recently and how often they buy and how much they spend (RFM), with how likely each is to stop buying. Scored every day from the last two years of purchases." />
      <ErrorNote error={q.error ?? make.error ?? score.error} />
      {d && f && f.total === 0 && (
        <section className="panel"><div className="empty">
          <h3>No buyers scored yet</h3>
          <p>
            Customer value is worked out once a day from your <code>{d.settings.event}</code> events (the amount from <code>{d.settings.amount_prop}</code>).
            {" "}If your purchases use another event or property, change it in the <Link to="/revenue">Revenue</Link> settings; buyers show here after the next daily pass.
          </p>
        </div></section>
      )}
      {d && f && f.total > 0 && (
        <>
          <div className="figures cv-figures">
            <div><strong><ContactCount value={f.total} filters={null} /></strong><span>buyers scored</span></div>
            <div><strong>{money(f.revenue, cur)}</strong><span>their revenue (2 years)</span></div>
            <div><strong><ContactCount value={f.atRisk} filters={[["churn", "high"]]} /></strong><span>at high churn risk</span></div>
            <div><strong>{money(f.atRiskRevenue, cur)}</strong><span>revenue from them</span></div>
          </div>
          <section className="panel">
            <div className="table-scroll">
              <table className="table cv-table">
                <thead><tr>
                  <th>Group</th><th className="num">Buyers</th><th className="num">Revenue</th><th className="num">Avg orders</th>
                  <th className="num">Days since last</th><th>Churn risk</th>{canSegment && <th aria-label="Actions" />}
                </tr></thead>
                <tbody>{f.groups.map((g) => (
                  <tr key={g.name} className={g.contacts === 0 ? "cv-empty" : undefined}>
                    <td><strong>{g.name}</strong>{g.note && <div className="xs muted cv-note">{g.note}</div>}</td>
                    <td className="num">
                      <ContactCount value={g.contacts} filters={[["value", g.name]]} />
                      {g.contacts > 0 && <div className="xs faint">{Math.round((g.contacts / f.total) * 100)}%</div>}
                    </td>
                    <td className="num">{g.contacts ? money(g.revenue, cur) : <span className="faint">–</span>}</td>
                    <td className="num">{g.contacts ? g.avgOrders.toFixed(1) : <span className="faint">–</span>}</td>
                    <td className="num">{g.contacts ? Math.round(g.avgDays) : <span className="faint">–</span>}</td>
                    <td>
                      <div className="cv-risks">
                        {d.risks.filter((r) => g.risks[r]).map((r) => (
                          <span key={r} className={`cv-risk cv-risk-${r}`} title={`${RISK_LABEL[r]} churn risk`}>
                            {RISK_LABEL[r]} <ContactCount value={g.risks[r]} filters={[["value", g.name], ["churn", r]]} />
                          </span>
                        ))}
                      </div>
                    </td>
                    {canSegment && (
                      <td className="num">
                        <button type="button" className="btn small" disabled={g.contacts === 0 || make.isPending} onClick={() => make.mutate({ group: g.name })}>
                          Make a segment
                        </button>
                      </td>
                    )}
                  </tr>
                ))}</tbody>
              </table>
            </div>
          </section>
          <p className="xs muted" style={{ maxWidth: "80ch" }}>
            Recency, frequency and spend are each scored 1–5 against your own buyers (fifths), so the groups adjust as your business grows.
            Churn risk compares the time since each buyer's last order with their usual gap between orders (1.5× overdue: medium, 3×: high);
            one-time buyers go medium after 90 days and high after 180; a year without an order is lost. When a buyer moves group, they get an
            {" "}<code>rfm_changed</code> event (with <code>group</code> and <code>previous_group</code>): start a triggered campaign from it to win back
            people the day they slip to <em>At risk</em>. Use <code>{"{{contact.rfm_group}}"}</code> and <code>{"{{contact.churn_risk}}"}</code> in messages.
          </p>
        </>
      )}
    </div>
  );
}
