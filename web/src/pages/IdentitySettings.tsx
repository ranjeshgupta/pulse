// Settings → Identity: the fields that identify a person (external id, email, phone, user name…),
// in priority order, and whether a shared value merges contacts or only lists them as duplicates.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, type AutomationResponse, type IdentityKey, type IdentityMode } from "../api";
import { useIsAdmin } from "../auth";
import { addKey, BUILT_IN, DEFAULT_KEYS, EXTERNAL_ID, explain, keyLabel, MAX_KEYS, move, problem, progress, removeKey } from "../identityKeys";
import { useEditGuard } from "../guard";
import { ErrorNote, PageHead, useToast } from "../ui";
import { useAttributes } from "./segmentData";

export function IdentitySettings() {
  const canEdit = useIsAdmin();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({
    queryKey: ["automation"],
    queryFn: () => api.get<AutomationResponse>("/v1/settings/automation"),
    // Poll while a new key is still being filled in for existing contacts.
    refetchInterval: (query) => (query.state.data?.identity_backfills ?? []).some((b) => !b.done_at) ? 3000 : false,
  });
  const attrs = useAttributes().data?.attributes ?? [];
  const labels = useMemo(() => Object.fromEntries(attrs.map((a) => [a.key, a.label || a.key])), [attrs]);
  const saved = q.data?.settings.identity?.keys ?? DEFAULT_KEYS;
  const [draft, setDraft] = useState<IdentityKey[] | null>(null);
  useEffect(() => { if (q.data && !draft) setDraft(structuredClone(saved)); }, [q.data, saved, draft]);
  const keys = draft ?? saved;
  const dirty = JSON.stringify(keys) !== JSON.stringify(saved);
  const why = problem(keys);
  const [adding, setAdding] = useState("");
  const options = [
    ...BUILT_IN.filter((b) => b.field !== EXTERNAL_ID).map((b) => ({ field: b.field, label: b.label })),
    ...attrs.filter((a) => a.type !== "list" && a.key !== "email" && a.key !== "phone").map((a) => ({ field: a.key, label: a.label || a.key })),
  ].filter((o) => !keys.some((k) => k.field === o.field));
  const backfills = q.data?.identity_backfills ?? [];
  const save = useMutation({
    mutationFn: () => api.put("/v1/settings/automation", { identity: { keys } }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["automation"] });
      setDraft(null);
      toast({ text: "Identity keys saved. Events use them within 30 seconds; new keys are filled in for existing contacts in the background." });
    },
  });
  // Leaving with unsaved key changes asks first; Ctrl/⌘+S saves.
  const guard = useEditGuard(() => { if (canEdit && dirty && !why && !save.isPending) save.mutate(); });
  const { markDirty, saved: markSaved } = guard;
  useEffect(() => { if (dirty) markDirty(); else markSaved(); }, [dirty, markDirty, markSaved]);
  const setMode = (i: number, mode: IdentityMode) => setDraft(keys.map((k, j) => (j === i ? { ...k, mode } : k)));

  return (
    <div className="page">
      <PageHead title="Identity"
        intro="Which details identify a person, and in what order. When an event's email, phone or other key is already held by another contact, Pulse treats them as one person." />
      <section className="panel section">
        <div className="panel-head"><h3>Identity keys</h3><span className="xs muted">Highest priority first</span></div>
        <div className="panel-body">
          <ErrorNote error={q.error ?? save.error} />
          <table className="table">
            <thead><tr><th style={{ width: 40 }}>#</th><th>Field</th><th>When two contacts share it</th><th>Status</th><th /></tr></thead>
            <tbody>{keys.map((k, i) => {
              const builtIn = BUILT_IN.find((b) => b.field === k.field);
              const ext = k.field === EXTERNAL_ID;
              return (
                <tr key={k.field}>
                  <td>{i + 1}</td>
                  <td><strong>{keyLabel(k.field, labels)}</strong><div className="xs muted">{builtIn?.hint ?? `Contact field ${k.field}, compared in lower case`}</div></td>
                  <td>
                    {ext ? <span className="small muted">It is the contact itself</span> : (
                      <select className="select" value={k.mode} disabled={!canEdit} aria-label={`${keyLabel(k.field, labels)}: when shared`}
                        onChange={(e) => setMode(i, e.target.value as IdentityMode)}>
                        <option value="merge">Merge them automatically</option>
                        <option value="suggest">Only list them under Duplicates</option>
                      </select>
                    )}
                  </td>
                  <td className="small muted">{ext ? "" : progress(backfills.find((b) => b.field === k.field)) || (dirty ? "Saved with the changes" : "")}</td>
                  <td style={{ whiteSpace: "nowrap" }}>
                    {canEdit && <>
                      <button className="btn ghost sm" aria-label={`Move ${keyLabel(k.field, labels)} up`} disabled={i === 0} onClick={() => setDraft(move(keys, i, -1))}>↑</button>
                      <button className="btn ghost sm" aria-label={`Move ${keyLabel(k.field, labels)} down`} disabled={i === keys.length - 1} onClick={() => setDraft(move(keys, i, 1))}>↓</button>
                      {!ext && <button className="btn ghost sm" onClick={() => setDraft(removeKey(keys, k.field))}>Remove</button>}
                    </>}
                  </td>
                </tr>
              );
            })}</tbody>
          </table>
          {canEdit && keys.length < MAX_KEYS && options.length > 0 && (
            <div className="row" style={{ marginTop: 12 }}>
              <select className="select" value={adding} aria-label="Field to add" onChange={(e) => setAdding(e.target.value)}>
                <option value="">Add a field…</option>
                {options.map((o) => <option key={o.field} value={o.field}>{o.label}</option>)}
              </select>
              <button className="btn" disabled={!adding} onClick={() => { setDraft(addKey(keys, adding)); setAdding(""); }}>Add key</button>
              <span className="xs muted">Any contact field works: user name, customer number, PAN…</span>
            </div>
          )}
          <div className="alert info" style={{ marginTop: 14 }}>
            <strong>What this does</strong>
            <ul style={{ margin: "6px 0 0", paddingLeft: 18 }}>{explain(keys, labels).map((l) => <li key={l}>{l}</li>)}</ul>
          </div>
          {keys.some((k) => k.mode === "merge" && (k.field === "email" || k.field === "phone")) && (
            <p className="xs muted">
              Merging can't be undone. If people in your audience share an email address or phone (a family number, an office inbox),
              choose "Only list them under Duplicates" for that key. Contacts that already share a value when a key is added are not
              merged in bulk: they show under <Link to="/contacts/duplicates">Duplicates</Link>. Only events from your servers, imports and
              integrations merge contacts: website and app events (where anyone can type any email) and anonymous visitors never do.
            </p>
          )}
          {why && <div className="alert error" style={{ marginTop: 12 }}>{why}</div>}
          {canEdit && (
            <div className="row" style={{ marginTop: 12 }}>
              <button className="btn primary" disabled={!dirty || !!why || save.isPending} onClick={() => save.mutate()}>Save identity keys</button>
              {dirty && <button className="btn ghost" onClick={() => setDraft(structuredClone(saved))}>Discard changes</button>}
            </div>
          )}
          {!canEdit && <p className="xs muted">Only admins can change the identity keys.</p>}
        </div>
      </section>
    </div>
  );
}
