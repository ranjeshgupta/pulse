// Shared campaign controls (M3): tags, send deadlines, the exact audience estimate, reach by
// channel, duplicate buttons and the saved test contact picker.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, CHANNEL_NAMES, type Channel, type Deadline, type Estimate, type SegmentReach, type TestContact } from "./api";
import { deadlineProblem, describeDeadline, estimateSteps, normalizeTags, suggestTags, tagProblem } from "./campaigns";
import { ErrorNote, Field, useToast } from "./ui";
import { ContactCount } from "./ContactCount";
import { estimateFilters } from "./contactLinks";

const n = (v: number) => v.toLocaleString("en-IN");

/** TagEditor edits free tags, suggesting the workspace's campaign tags. */
export function TagEditor({ value, onChange, disabled, label = "Tags" }: { value: string[]; onChange: (t: string[]) => void; disabled?: boolean; label?: string }) {
  const [typed, setTyped] = useState("");
  const known = useQuery({ queryKey: ["campaign-tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/campaigns/tags"), staleTime: 30_000 });
  const listId = useId();
  const problem = tagProblem(typed, value);
  const add = (t: string) => {
    if (!t.trim() || tagProblem(t, value)) return;
    onChange(normalizeTags([...value, t]));
    setTyped("");
  };
  return (
    <div className="field">
      <span>{label}</span>
      <div className="row" style={{ gap: 6 }}>
        {value.map((t) => (
          <span key={t} className="tag-chip">{t}
            {!disabled && <button type="button" aria-label={`Remove tag ${t}`} onClick={() => onChange(value.filter((x) => x !== t))}>×</button>}
          </span>
        ))}
        {!disabled && (
          <input className="input inline" style={{ width: 170 }} list={listId} value={typed} placeholder="Add a tag" aria-label="Add a tag"
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" || e.key === ",") { e.preventDefault(); add(typed); } }}
            onBlur={() => typed.trim() && !problem && add(typed)} />
        )}
        <datalist id={listId}>{suggestTags(known.data?.tags ?? [], typed, value).map((t) => <option key={t} value={t} />)}</datalist>
      </div>
      <small>{problem ?? "Free labels, e.g. Diwali 2026 or Retention: filter the Campaigns hub by them."}</small>
    </div>
  );
}

/** DeadlineFields edits a send deadline: a time of day and/or hours late. */
export function DeadlineFields({ value, onChange, localTime, zone, disabled }: { value?: Deadline | null; onChange: (d: Deadline | null) => void; localTime?: boolean; zone?: string; disabled?: boolean }) {
  const on = !!value;
  const set = (p: Partial<Deadline>) => onChange({ ...(value ?? {}), ...p });
  return (
    <div className="deadline-box">
      <label className="check">
        <input type="checkbox" checked={on} disabled={disabled}
          onChange={(e) => onChange(e.target.checked ? { at: "21:00", timezone: localTime ? undefined : zone } : null)} />
        Send deadline: drop messages that would go out late
      </label>
      {on && (
        <div className="grid-2">
          <Field label="Don't send after (time of day)" hint={localTime ? "In each contact's own time zone." : `In ${value?.timezone || "UTC"}.`}>
            <input className="input" type="time" disabled={disabled} value={value?.at ?? ""} onChange={(e) => set({ at: e.target.value || undefined })} aria-label="Don't send after" />
          </Field>
          <Field label="…or more than (hours late)" hint="Hours after the send starts. Leave empty for none.">
            <input className="input" type="number" min={1} max={168} disabled={disabled} value={value?.hours ?? ""} onChange={(e) => set({ hours: e.target.value ? Number(e.target.value) : undefined })} aria-label="Hours late" />
          </Field>
        </div>
      )}
      {deadlineProblem(value) && <p className="xs" style={{ color: "var(--danger)" }}>{deadlineProblem(value)}</p>}
      <p className="xs muted" style={{ marginTop: on ? -4 : 0 }}>{describeDeadline(value, localTime)}{on ? " Quotas, quiet hours and retries can hold messages back; this stops them arriving at night." : ""}</p>
    </div>
  );
}

/** useEstimate fetches the exact audience estimate (cached 30 s by the server). */
export function useEstimate(include: number[], exclude: number[], channel: Channel, transactional = false) {
  const [key, setKey] = useState("");
  const body = { include, exclude, channel, transactional };
  const k = JSON.stringify(body);
  useEffect(() => { const t = window.setTimeout(() => setKey(k), 300); return () => window.clearTimeout(t); }, [k]);
  return useQuery({
    queryKey: ["estimate", key], enabled: !!key && include.length > 0,
    queryFn: () => api.post<Estimate>("/v1/segments/estimate", JSON.parse(key)),
    staleTime: 25_000, retry: false,
  });
}

/** AudienceEstimate shows the audience after each step: exclusions, consent, reachability, caps. */
export function AudienceEstimate({ include, exclude, channel, transactional }: { include: number[]; exclude: number[]; channel: Channel; transactional?: boolean }) {
  const q = useEstimate(include, exclude, channel, transactional);
  if (include.length === 0) return <div className="estimate muted small">Choose a segment to see who it reaches.</div>;
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return <div className="estimate muted small" aria-busy="true">Counting…</div>;
  const e = q.data;
  const steps = estimateSteps(e, CHANNEL_NAMES[channel], exclude.length > 0);
  return (
    <div className="estimate" aria-live="polite">
      <ol className="estimate-steps">
        {steps.map((s) => (
          <li key={s.key}>
            <span className="estimate-label">{s.label}{s.note && <small className="muted"> ({s.note})</small>}</span>
            <strong className="num"><ContactCount value={s.count} filters={estimateFilters(s.key, include, exclude, channel)} /></strong>
            {s.dropped > 0 && <span className="xs muted">−{n(s.dropped)}</span>}
          </li>
        ))}
      </ol>
      <p className="estimate-total"><strong className="num"><ContactCount value={e.will_send} filters={estimateFilters(e.capped && e.cap ? "cap" : "reach", include, exclude, channel)} /></strong> contacts will get it{e.capped?.approximate ? " (about)" : ""}, if nothing changes before it sends.</p>
      {!e.ready && <p className="xs muted">Reachability is still being computed for this workspace; the last steps may read low for a few minutes.</p>}
      <p className="xs muted">Counted {new Date(e.computed_at).toLocaleTimeString()} · refreshes every 30 seconds.</p>
    </div>
  );
}

/** ReachByChannel shows how many members of a segment each channel reaches. */
export function ReachByChannel({ segmentId, channels, title = "Reachable by channel" }: { segmentId: number; channels?: Channel[]; title?: string }) {
  const q = useQuery({ queryKey: ["segment-reach", segmentId], queryFn: () => api.get<SegmentReach>(`/v1/segments/${segmentId}/reach`), enabled: segmentId > 0, staleTime: 25_000, retry: false });
  if (!segmentId) return null;
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return <p className="xs muted" aria-busy="true">Counting who each channel reaches…</p>;
  const rows = q.data.channels.filter((c) => !channels || channels.includes(c.channel));
  return (
    <div className="reach-box">
      <h4 className="small">{title}</h4>
      <table className="table compact">
        <thead><tr><th>Channel</th><th className="num">Not unsubscribed</th><th className="num">Reachable</th></tr></thead>
        <tbody>{rows.map((c) => (
          <tr key={c.channel}><td>{CHANNEL_NAMES[c.channel]}</td>
            <td className="num"><ContactCount value={c.after_consent} filters={[["in", segmentId], ["subscribed", c.channel]]} /></td>
            <td className="num"><strong><ContactCount value={c.reachable} filters={[["in", segmentId], ["reachable", c.channel]]} /></strong></td></tr>
        ))}</tbody>
      </table>
      <p className="xs muted">Of <ContactCount value={q.data.size} filters={[["in", segmentId]]} /> members now. Click a number to see those contacts. Reachable: has an address on the channel (a push device for push), not bounced, not unsubscribed or Do Not Contact.{q.data.ready ? "" : " Still being computed for this workspace."}</p>
    </div>
  );
}

/** DuplicateButton copies a broadcast, journey, template or segment and opens the copy. */
export function DuplicateButton({ path, open, label = "Duplicate", invalidate }: { path: string; open: (id: number) => string; label?: string; invalidate?: string[] }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const dup = useMutation({
    mutationFn: () => api.post<{ id: number }>(path),
    onSuccess: (r) => { invalidate?.forEach((k) => qc.invalidateQueries({ queryKey: [k] })); toast({ text: "Copy created as a draft" }); nav(open(r.id)); },
    onError: (e) => toast({ text: (e as Error).message, error: true }),
  });
  return <button type="button" className="btn" disabled={dup.isPending} onClick={() => dup.mutate()}>{label}</button>;
}

/** useTestContacts lists the workspace's saved test contacts. */
export function useTestContacts() {
  return useQuery({ queryKey: ["test-contacts"], queryFn: () => api.get<{ test_contacts: TestContact[] }>("/v1/test-contacts") });
}

/** testContactLabel describes a test contact for a channel, or why it can't take it. */
export function testContactAddress(tc: TestContact, channel: Channel): string | null {
  if (tc.external_id) return `contact ${tc.external_id}`;
  const a = channel === "email" ? tc.email : channel === "push" ? tc.push_token : channel === "inbox" ? "" : tc.phone;
  return a ? (channel === "push" ? `device ${a.slice(0, 12)}…` : a) : null;
}
