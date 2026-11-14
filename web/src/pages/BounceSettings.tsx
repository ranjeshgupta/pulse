// Settings → Bounces & unsubscribes: what bounces and spam complaints do on each channel (the
// workspace's bounce rules, the "bounces" section of the automation settings), where email
// services and mailboxes report them, and how people unsubscribe.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, CHANNEL_NAMES, type AutomationResponse, type BounceAction, type ChannelBounces, type Channel } from "../api";
import { useIsAdmin } from "../auth";
import { ErrorNote, PageHead, useToast } from "../ui";
import { BounceMailboxPanel } from "./BounceMailbox";
import { BouncesPanel } from "./Channels";
import { BOUNCE_ACTIONS, BOUNCE_CHANNELS, BOUNCE_TYPES, ruleProblem, ruleSentence } from "../bounceRules";

export function BounceSettings() {
  const admin = useIsAdmin();
  return (
    <div className="page">
      <PageHead title="Bounces & unsubscribes"
        intro="What Pulse does when a message bounces or someone marks it as spam, on every channel; where your email service and bounce mailboxes report them; and how people unsubscribe." />
      <BounceRulesPanel canEdit={admin} />
      {admin && <BouncesPanel />}
      {admin && <BounceMailboxPanel />}
    </div>
  );
}

/** BounceRulesPanel: per channel, after how many hard bounces, soft bounces and complaints which action runs. */
function BounceRulesPanel({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["automation"], queryFn: () => api.get<AutomationResponse>("/v1/settings/automation") });
  const [ch, setCh] = useState<Channel>("email");
  const [draft, setDraft] = useState<Record<string, ChannelBounces> | null>(null);
  const saved = q.data?.settings.bounces ?? q.data?.defaults.bounces;
  useEffect(() => { if (saved && !draft) setDraft(structuredClone(saved)); }, [saved, draft]);
  const rules = draft?.[ch] ?? saved?.[ch];
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  const problem = draft ? BOUNCE_CHANNELS.map((c) => draft[c] && ruleProblem(draft[c])).find(Boolean) : "";
  const save = useMutation({
    mutationFn: () => api.put<{ settings: unknown }>("/v1/settings/automation", { bounces: draft }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["automation"] }); setDraft(null); toast({ text: "Bounce rules saved. They apply to the next bounce within 30 seconds." }); },
  });
  const set = (type: keyof ChannelBounces, patch: Partial<ChannelBounces[keyof ChannelBounces]>) =>
    setDraft({ ...draft!, [ch]: { ...draft![ch], [type]: { ...draft![ch][type], ...patch } } });
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Bounce rules</h3><span className="xs muted">Counted per contact, per channel</span></div>
      <div className="panel-body">
        <ErrorNote error={q.error ?? save.error} />
        <p className="small muted" style={{ marginTop: 0 }}>
          Every bounce and spam complaint is counted on the contact. When a count reaches the number below, the action runs once.
          A new email address or phone number starts the counts over and undoes "Stop sending" and "Leave all segments".
        </p>
        <div className="seg-toggle" role="tablist" aria-label="Channel" style={{ marginBottom: 14 }}>
          {BOUNCE_CHANNELS.map((c) => (
            <button key={c} role="tab" aria-selected={ch === c} aria-pressed={ch === c} onClick={() => setCh(c)}>{CHANNEL_NAMES[c]}</button>
          ))}
        </div>
        {rules && (
          <table className="table br-table">
            <thead><tr><th>When it's a</th><th>After this many</th><th>Do this</th><th>In words</th></tr></thead>
            <tbody>{BOUNCE_TYPES.map((t) => (
              <tr key={t.key}>
                <td><strong>{t.label}</strong><div className="xs muted">{t.hint[ch] ?? t.hint.other}</div></td>
                <td>
                  <input className="input br-count" type="number" min={1} max={100} value={rules[t.key].count} disabled={!canEdit}
                    aria-label={`${t.label}: count`} onChange={(e) => set(t.key, { count: Math.trunc(Number(e.target.value)) })} />
                </td>
                <td>
                  <select className="select" value={rules[t.key].action} disabled={!canEdit} aria-label={`${t.label}: action`}
                    onChange={(e) => set(t.key, { action: e.target.value as BounceAction })}>
                    {BOUNCE_ACTIONS.map((a) => <option key={a.key} value={a.key}>{a.label}</option>)}
                  </select>
                </td>
                <td className="small muted">{ruleSentence(ch, t.key, rules[t.key])}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
        {problem && <div className="alert error" style={{ marginTop: 12 }}>{problem}</div>}
        {canEdit && (
          <div className="row" style={{ marginTop: 12 }}>
            <button className="btn primary" disabled={!dirty || !!problem || save.isPending} onClick={() => save.mutate()}>Save bounce rules</button>
            {dirty && <button className="btn ghost" onClick={() => setDraft(structuredClone(saved!))}>Discard changes</button>}
            <button className="btn ghost" onClick={() => setDraft(structuredClone(q.data!.defaults.bounces ?? null))}>Back to the defaults</button>
          </div>
        )}
        {!canEdit && <p className="xs muted">Only admins can change the rules.</p>}
      </div>
    </section>
  );
}
