// Settings → Bounces & unsubscribes: what bounces and spam complaints do on each channel (the
// workspace's bounce rules, the "bounces" section of the automation settings), where email
// services and mailboxes report them, and how people unsubscribe.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import { api, CHANNEL_NAMES, type AutomationResponse, type BounceAction, type ChannelBounces, type Channel, type UnsubscribeSettings } from "../api";
import { useIsAdmin } from "../auth";
import { ErrorNote, PageHead, useToast } from "../ui";
import { useEditGuard } from "../guard";
import { BounceMailboxPanel } from "./BounceMailbox";
import { BouncesPanel } from "./Channels";
import { BOUNCE_ACTIONS, BOUNCE_CHANNELS, BOUNCE_TYPES, ruleProblem, ruleSentence } from "../bounceRules";
import { topicKeyFrom, unsubscribeProblem } from "../unsubscribeRules";

type ReportDirty = (dirty: boolean) => void;

/** useDirtyParts guards a page made of panels that each keep their own draft: leaving the page
 * asks first while any of them has unsaved changes. part(key) is the callback a panel reports to. */
function useDirtyParts() {
  const [parts, setParts] = useState<Record<string, boolean>>({});
  const { markDirty, saved } = useEditGuard();
  const any = Object.values(parts).some(Boolean);
  useEffect(() => { if (any) markDirty(); else saved(); }, [any, markDirty, saved]);
  const report = useCallback((key: string, dirty: boolean) => setParts((p) => (!!p[key] === dirty ? p : { ...p, [key]: dirty })), []);
  const [cbs] = useState(() => new Map<string, ReportDirty>());
  return (key: string): ReportDirty => {
    let cb = cbs.get(key);
    if (!cb) { cb = (d) => report(key, d); cbs.set(key, cb); }
    return cb;
  };
}

export function BounceSettings() {
  const admin = useIsAdmin();
  const part = useDirtyParts();
  return (
    <div className="page">
      <PageHead title="Bounces & unsubscribes"
        intro="What Pulse does when a message bounces or someone marks it as spam, on every channel; where your email service and bounce mailboxes report them; and how people unsubscribe." />
      <BounceRulesPanel canEdit={admin} onDirty={part("rules")} />
      <UnsubscribePanel canEdit={admin} onDirty={part("unsubscribe")} />
      {admin && <BouncesPanel onDirty={part("signing")} />}
      {admin && <BounceMailboxPanel onDirty={part("mailbox")} />}
    </div>
  );
}

/** BounceRulesPanel: per channel, after how many hard bounces, soft bounces and complaints which action runs. */
function BounceRulesPanel({ canEdit, onDirty }: { canEdit: boolean; onDirty: ReportDirty }) {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["automation"], queryFn: () => api.get<AutomationResponse>("/v1/settings/automation") });
  const [ch, setCh] = useState<Channel>("email");
  const [draft, setDraft] = useState<Record<string, ChannelBounces> | null>(null);
  const saved = q.data?.settings.bounces ?? q.data?.defaults.bounces;
  useEffect(() => { if (saved && !draft) setDraft(structuredClone(saved)); }, [saved, draft]);
  const rules = draft?.[ch] ?? saved?.[ch];
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  useEffect(() => { onDirty(!!draft && !!saved && dirty); }, [draft, saved, dirty, onDirty]);
  const problem = draft ? BOUNCE_CHANNELS.map((c) => draft[c] && ruleProblem(draft[c])).find(Boolean) : "";
  const save = useMutation({
    mutationFn: () => api.put<{ settings: unknown }>("/v1/settings/automation", { bounces: draft }),
    // Wait for the saved settings before dropping the draft, or the draft would refill from the old ones.
    onSuccess: async () => { await qc.invalidateQueries({ queryKey: ["automation"] }); setDraft(null); toast({ text: "Bounce rules saved. They apply to the next bounce within 30 seconds." }); },
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
            <button className="btn primary" disabled={!dirty || !!problem || save.isPending} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : "Save bounce rules"}</button>
            {dirty && <button className="btn ghost" onClick={() => setDraft(structuredClone(saved!))}>Discard changes</button>}
            <button className="btn ghost" onClick={() => setDraft(structuredClone(q.data!.defaults.bounces ?? null))}>Back to the defaults</button>
          </div>
        )}
        {!canEdit && <p className="xs muted">Only admins can change the rules.</p>}
      </div>
    </section>
  );
}

/** UnsubscribePanel: what the unsubscribe link does, whether email unsubscribes stop email by
 * themselves, and the workspace's topics (lists people can leave on their own). */
function UnsubscribePanel({ canEdit, onDirty }: { canEdit: boolean; onDirty: ReportDirty }) {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["automation"], queryFn: () => api.get<AutomationResponse>("/v1/settings/automation") });
  const saved = q.data?.settings.unsubscribe ?? q.data?.defaults.unsubscribe;
  const [draft, setDraft] = useState<UnsubscribeSettings | null>(null);
  useEffect(() => { if (saved && !draft) setDraft(structuredClone(saved)); }, [saved, draft]);
  const u = draft ?? saved;
  const dirty = !!draft && JSON.stringify(draft) !== JSON.stringify(saved);
  useEffect(() => { onDirty(!!saved && dirty); }, [saved, dirty, onDirty]);
  const problem = u ? unsubscribeProblem(u) : "";
  const save = useMutation({
    mutationFn: () => api.put<{ settings: unknown }>("/v1/settings/automation", { unsubscribe: draft }),
    onSuccess: async () => {
      qc.invalidateQueries({ queryKey: ["unsubscribe-topics"] });
      await qc.invalidateQueries({ queryKey: ["automation"] });
      setDraft(null); toast({ text: "Unsubscribe settings saved. Emails sent from now on use them." });
    },
  });
  if (!u) return null;
  const set = (patch: Partial<UnsubscribeSettings>) => setDraft({ ...u, ...patch });
  const setTopic = (i: number, patch: Partial<UnsubscribeSettings["topics"][number]>) =>
    set({ topics: u.topics.map((t, j) => (j === i ? { ...t, ...patch } : t)) });
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Unsubscribes</h3><span className="xs muted">Every email has an unsubscribe link and the one-click unsubscribe header</span></div>
      <div className="panel-body">
        <ErrorNote error={q.error ?? save.error} />
        <fieldset className="radio-cards" disabled={!canEdit}>
          <legend className="small"><strong>When someone clicks the unsubscribe link</strong></legend>
          <label className="check">
            <input type="radio" name="link_mode" checked={u.link_mode === "confirm"} onChange={() => set({ link_mode: "confirm" })} />
            <span><strong>Show a page to confirm</strong><span className="xs muted"> — they press "Unsubscribe" (and can pick topics or other channels instead).</span></span>
          </label>
          <label className="check">
            <input type="radio" name="link_mode" checked={u.link_mode === "one_click"} onChange={() => set({ link_mode: "one_click" })} />
            <span><strong>Unsubscribe in one click</strong><span className="xs muted"> — the page unsubscribes them right away and says so. Email security scanners that open links don't unsubscribe anyone.</span></span>
          </label>
        </fieldset>
        <p className="xs muted">Either way, every email carries the <code>List-Unsubscribe</code> and <code>List-Unsubscribe-Post: List-Unsubscribe=One-Click</code> headers, so Gmail, Yahoo and Outlook show their own Unsubscribe button, which works in one click.</p>

        <fieldset className="radio-cards" disabled={!canEdit} style={{ marginTop: 14 }}>
          <legend className="small"><strong>People who unsubscribed from email</strong></legend>
          <label className="check">
            <input type="radio" name="email_policy" checked={u.email_policy === "enforce"} onChange={() => set({ email_policy: "enforce" })} />
            <span><strong>Never email them</strong><span className="xs muted"> — Pulse skips them in every campaign.</span></span>
          </label>
          <label className="check">
            <input type="radio" name="email_policy" checked={u.email_policy === "segment"} onChange={() => set({ email_policy: "segment" })} />
            <span><strong>Leave them out with segment filters</strong><span className="xs muted"> — they stay in segments and campaigns; exclude a segment of unsubscribed contacts (filter "Unsubscribed from email"). Campaigns show how many are in the audience.</span></span>
          </label>
        </fieldset>
        {u.email_policy === "segment" && (
          <div className="alert warn" style={{ marginTop: 8 }}>
            Laws such as CAN-SPAM and GDPR require you to stop emailing people who unsubscribed. With this choice, that's up to your segments.
            People who marked an email as spam are still never emailed, and SMS STOP replies and topic unsubscribes are always honoured.
          </div>
        )}

        <h4 className="small" style={{ marginTop: 18 }}>Topics</h4>
        <p className="xs muted" style={{ marginTop: 0 }}>
          Products, newsletters or lists people can leave on their own. Choose a topic on a campaign or journey step: people who left it are skipped,
          and its unsubscribe link (and the one-click header) leaves that topic only. They stay in every segment.
        </p>
        {u.topics.length > 0 && (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Key</th><th>Shown to people</th><th /></tr></thead>
            <tbody>{u.topics.map((t, i) => (
              <tr key={i}>
                <td><input className="input" aria-label={`Topic ${i + 1} name`} value={t.name} disabled={!canEdit}
                  onChange={(e) => setTopic(i, { name: e.target.value, ...(saved?.topics.some((x) => x.key === t.key) ? {} : { key: topicKeyFrom(e.target.value) }) })} /></td>
                <td><code className="xs">{t.key || "—"}</code></td>
                <td><input className="input" aria-label={`Topic ${i + 1} description`} placeholder="e.g. Weekly deals and coupons" value={t.description ?? ""} disabled={!canEdit}
                  onChange={(e) => setTopic(i, { description: e.target.value })} /></td>
                <td>{canEdit && <button className="btn ghost sm" aria-label={`Remove topic ${t.name || i + 1}`} onClick={() => set({ topics: u.topics.filter((_, j) => j !== i) })}>×</button>}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
        {canEdit && <button className="btn sm" style={{ marginTop: 8 }} onClick={() => set({ topics: [...u.topics, { key: "", name: "" }] })}>+ Add a topic</button>}
        {problem && <div className="alert error" style={{ marginTop: 12 }}>{problem}</div>}
        {canEdit && (
          <div className="row" style={{ marginTop: 12 }}>
            <button className="btn primary" disabled={!dirty || !!problem || save.isPending} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : "Save unsubscribe settings"}</button>
            {dirty && <button className="btn ghost" onClick={() => setDraft(structuredClone(saved!))}>Discard changes</button>}
          </div>
        )}
        {!canEdit && <p className="xs muted">Only admins can change these.</p>}
      </div>
    </section>
  );
}
