import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api";
import { useIsAdmin } from "../auth";
import { ErrorNote, Field, PageHead, useToast } from "../ui";

interface DataSettings {
  tracking: { anonymous: boolean; geoip: boolean };
  retention: { anonymous_days: number; event_days: number; audit_days: number };
}

// DataPrivacy: whether anonymous visitors are tracked and located, and how long data is kept.
export function DataPrivacy() {
  const admin = useIsAdmin();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["data-settings"], queryFn: () => api.get<{ settings: DataSettings; geoip_available: boolean }>("/v1/settings/data") });
  const [d, setD] = useState<DataSettings | null>(null);
  const cur = d ?? q.data?.settings;
  const save = useMutation({
    mutationFn: () => api.put("/v1/settings/data", cur),
    onSuccess: () => { setD(null); qc.invalidateQueries({ queryKey: ["data-settings"] }); toast({ text: "Saved. Cleanup runs every hour." }); },
  });
  if (!cur) return <div className="page"><ErrorNote error={q.error} /></div>;
  const days = (k: keyof DataSettings["retention"], label: string, hint: string, min: number) => (
    <Field label={label} hint={hint}>
      <input className="input" type="number" min={0} max={3650} value={cur.retention[k]}
        onChange={(e) => setD({ ...cur, retention: { ...cur.retention, [k]: Number(e.target.value) || 0 } })} aria-describedby={`min-${k}`} />
      <small id={`min-${k}`} className="xs muted">0 keeps it forever; otherwise at least {min} days.</small>
    </Field>
  );
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
        <section className="panel section"><div className="panel-head"><h3>Keep data for</h3></div><div className="panel-body">
          <div className="grid-2">
            {days("anonymous_days", "Anonymous visitors (days idle)", "Visitors who never signed in, with their events, are deleted after this long without activity.", 7)}
            {days("event_days", "Events (days)", "Older events are deleted. Profiles, segments and reports of recent activity stay.", 30)}
            {days("audit_days", "Audit log (days)", "Older audit entries are deleted.", 30)}
          </div>
        </div></section>
        {admin && <button className="btn primary" disabled={!d || save.isPending} onClick={() => save.mutate()}>Save</button>}
      </fieldset>
    </div>
  );
}
