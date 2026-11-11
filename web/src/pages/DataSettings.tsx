import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api";
import { useIsAdmin } from "../auth";
import { Link } from "react-router-dom";
import { ErrorNote, PageHead, useToast } from "../ui";

const keep = (d: number) => (d ? `${d} days` : "forever");

interface DataSettings {
  tracking: { anonymous: boolean; geoip: boolean };
  retention: { anonymous_days: number; event_days: number; audit_days: number };
}

// DataPrivacy: whether anonymous visitors are tracked and located. Retention moved to Settings →
// Automation & limits; a save here sends the retention it read, which the server merges unchanged.
export function DataPrivacy() {
  const admin = useIsAdmin();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["data-settings"], queryFn: () => api.get<{ settings: DataSettings; geoip_available: boolean }>("/v1/settings/data") });
  const [d, setD] = useState<DataSettings | null>(null);
  const cur = d ?? q.data?.settings;
  const save = useMutation({
    mutationFn: () => api.put("/v1/settings/data", cur),
    onSuccess: () => { setD(null); qc.invalidateQueries({ queryKey: ["data-settings"] }); toast({ text: "Saved." }); },
  });
  if (!cur) return <div className="page"><ErrorNote error={q.error} /></div>;
  return (
    <div className="page">
      <PageHead title="Data & privacy" intro="Choose what Pulse records about visitors and how long it keeps it." />
      <ErrorNote error={q.error ?? save.error} />
      <fieldset disabled={!admin} style={{ border: 0, padding: 0, margin: 0 }}>
        <section className="panel section"><div className="panel-head"><h3>Tracking</h3></div><div className="panel-body">
          <label className="check">
            <input type="checkbox" checked={cur.tracking.anonymous} onChange={(e) => setD({ ...cur, tracking: { ...cur.tracking, anonymous: e.target.checked } })} />
            <span>Track anonymous visitors. Their activity joins their profile when they sign in or fill a form. Off: only signed-in visitors are recorded.</span>
          </label>
          <label className="check">
            <input type="checkbox" checked={cur.tracking.geoip} onChange={(e) => setD({ ...cur, tracking: { ...cur.tracking, geoip: e.target.checked } })} />
            <span>Locate visitors by IP address: country, state, city and time zone (geo_country, geo_region, geo_city, geo_timezone). Quiet hours use the time zone when a contact has none.</span>
          </label>
          {!q.data?.geoip_available && <p className="xs muted">The server has no MaxMind database yet (PULSE_GEOIP_DB), so nothing is located until one is added.</p>}
        </div></section>
        <section className="panel section"><div className="panel-head"><h3>Keep data for</h3></div><div className="panel-body small">
          How long events, anonymous visitors, the audit log and other logs are kept is set on <Link to="/settings/automation">Automation &amp; limits</Link>,
          with a preview of what a shorter setting would delete.
          <span className="muted"> Now: events {keep(cur.retention.event_days)}, anonymous visitors {cur.retention.anonymous_days ? `deleted after ${cur.retention.anonymous_days} idle days` : "kept"}, audit log {keep(cur.retention.audit_days)}.</span>
        </div></section>
        {admin && <button className="btn primary" disabled={!d || save.isPending} onClick={() => save.mutate()}>Save</button>}
      </fieldset>
    </div>
  );
}
