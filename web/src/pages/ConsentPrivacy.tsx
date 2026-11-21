// The contact's Consent & privacy tab (O6): consent history, and the data-protection rights in one
// place (access, correction, withdrawal, erasure), for DPDP Act / GDPR requests.
import { useQuery } from "@tanstack/react-query";
import { api, type EventRow } from "../api";
import { CONSENT_EVENTS, consentLine } from "../consentHistory";
import { Empty, ErrorNote } from "../ui";

export function ConsentPrivacyPanel({ externalId, canEdit, onDelete }: { externalId: string; canEdit: boolean; onDelete: () => void }) {
  const id = encodeURIComponent(externalId);
  const q = useQuery({ queryKey: ["consent-history", externalId], queryFn: () => api.get<{ events: EventRow[] }>(`/v1/contacts/${id}/events?name=${CONSENT_EVENTS.join(",")}&limit=500`) });
  const rows = (q.data?.events ?? []).map((e) => ({ e, line: consentLine(e) })).filter((r) => r.line);
  return (
    <div className="grid-2" style={{ alignItems: "start", gap: 16 }}>
      <section className="panel">
        <div className="panel-head"><h3>Consent history</h3><span className="xs muted">Newest first</span></div>
        <ErrorNote error={q.error} />
        {rows.length === 0 && !q.isLoading ? <Empty title="No consent changes recorded">Opt-ins, opt-outs, confirmations and form consents show here.</Empty> : (
          <ul className="consent-list">
            {rows.map(({ e, line }) => (
              <li key={e.id}>
                <span aria-hidden="true">{line!.icon}</span>
                <div><div>{line!.text}</div>{line!.detail && <div className="xs muted">{line!.detail}</div>}
                  <div className="xs muted">{new Date(e.ts).toLocaleString("en-IN", { dateStyle: "medium", timeStyle: "short" })}</div></div>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="panel">
        <div className="panel-head"><h3>Their data rights</h3></div>
        <div className="panel-body small">
          <p style={{ marginTop: 0 }}>When this person asks under the DPDP Act (India) or GDPR:</p>
          <ul className="rights-list">
            <li><strong>See their data</strong> — {canEdit ? <a href={`/v1/contacts/${id}/data`} download>Download everything Pulse keeps</a> : "someone who may edit contacts can download it"} (profile, consent, segments, events) as JSON.</li>
            <li><strong>Correct it</strong> — edit the fields on the Overview tab, or send an identify from your systems.</li>
            <li><strong>Withdraw consent</strong> — unsubscribe them from a channel or from everything on the Overview tab; every send respects it at once.</li>
            <li><strong>Erase it</strong> — {canEdit ? <button className="btn danger sm" onClick={onDelete}>Delete this contact</button> : "someone who may edit contacts can delete them"}: profile, events, messages and devices go; it can't be undone.</li>
          </ul>
          <p className="xs muted">Keep a note of each request and when you answered it (the audit log records deletions and exports by your team).</p>
        </div>
      </section>
    </div>
  );
}
