// Settings → Integrations: connectors to CRMs (HubSpot, Salesforce), Shopify, Zapier / Make
// REST hooks and ad platforms (Facebook Custom Audiences, Google Ads Customer Match). Each has its
// settings (secrets shown masked), a connection test, a preview and a sync log; CRMs add a field
// mapping and pushes, Shopify its webhook URL, REST hooks their API key and subscriptions, and ad
// platforms the segments linked to their audiences.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  api, type AttributeDef, type AudienceSync, type FieldMap, type Integration, type IntegrationKind, type IntegrationLogEntry,
  type RevenueSettings, type Segment, type Webhook, type WebhookDelivery,
} from "../api";
import { useCan, useIsAdmin } from "../auth";
import { CATEGORY_LABELS, CATEGORY_ORDER, mappingProblems, PULL_EVERY } from "../integrations";
import { Empty, ErrorNote, Field, PageHead, timeAgo, useCopy, useToast } from "../ui";
import "./integrations.css";
import "./webhooks.css";

type Catalog = { kinds: IntegrationKind[]; shopify_topics: string[]; hook_events: string[] };
type Detail = {
  integration: Integration; kind: IntegrationKind; log: IntegrationLogEntry[];
  webhook_url?: string; hooks_url?: string; subscriptions?: Webhook[]; deliveries?: WebhookDelivery[]; audiences?: AudienceSync[];
};
type TestResult = { ok: boolean; error?: string; duration_ms: number };

const fmt = (s: string) => new Date(s).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" });
const useCatalog = () => useQuery({ queryKey: ["integration-catalog"], queryFn: () => api.get<Catalog>("/v1/integrations/catalog"), staleTime: 300_000 });

function StatusBadge({ i }: { i: Pick<Integration, "status" | "active"> }) {
  if (!i.active) return <span className="badge">Paused</span>;
  if (i.status === "ok") return <span className="badge ok">Connected</span>;
  if (i.status === "error") return <span className="badge bad">Error</span>;
  if (i.status === "syncing") return <span className="badge info">Syncing</span>;
  return <span className="badge">Not synced yet</span>;
}

// ---------- list ----------

export function IntegrationList() {
  const admin = useIsAdmin();
  const cat = useCatalog();
  const q = useQuery({ queryKey: ["integrations"], queryFn: () => api.get<{ integrations: Integration[] }>("/v1/integrations"), refetchInterval: 15000 });
  const items = q.data?.integrations ?? [];
  const kinds = cat.data?.kinds ?? [];
  const kindName = (id: string) => kinds.find((k) => k.id === id)?.name ?? id;
  return (
    <div className="page">
      <PageHead
        title="Integrations"
        intro="Connect Pulse to your CRM, your store, Zapier or Make, and your ad accounts. Contacts and events flow in, contacts and audiences flow out."
      />
      <ErrorNote error={q.error ?? cat.error} />
      {items.length > 0 && (
        <div className="panel">
          <table className="table">
            <thead><tr><th>Name</th><th>Type</th><th>Status</th><th>Last sync</th></tr></thead>
            <tbody>{items.map((i) => (
              <tr key={i.id}>
                <td><Link to={`/settings/integrations/${i.id}`}>{i.name}</Link></td>
                <td className="small">{kindName(i.kind)}</td>
                <td><StatusBadge i={i} />{i.status === "error" && i.last_error && <div className="xs muted int-err" title={i.last_error}>{i.last_error}</div>}</td>
                <td className="small muted">{i.last_sync_at ? timeAgo(i.last_sync_at) : "Never"}</td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
      {!admin && <p className="small muted" style={{ marginTop: 12 }}>Only admins (settings edit access) can connect or change integrations.</p>}
      {CATEGORY_ORDER.map((c) => {
        const ks = kinds.filter((k) => k.category === c);
        if (ks.length === 0) return null;
        return (
          <section className="section" key={c}>
            <h3>{CATEGORY_LABELS[c]}</h3>
            <div className="int-cards">
              {ks.map((k) => (
                <div className="panel int-card" key={k.id}>
                  <strong>{k.name}</strong>
                  <p className="small muted">{k.description}</p>
                  {admin && <Link className="btn sm" to={`/settings/integrations/new?kind=${k.id}`}>Connect</Link>}
                </div>
              ))}
            </div>
          </section>
        );
      })}
    </div>
  );
}

// ---------- editor ----------

type Draft = { name: string; config: Record<string, string>; secrets: Record<string, string>; mapping: FieldMap[]; active: boolean; sync_minutes: number };

export function IntegrationEdit() {
  const { id } = useParams();
  const [params] = useSearchParams();
  const isNew = id === "new";
  const admin = useIsAdmin();
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const cat = useCatalog();
  const existing = useQuery({ queryKey: ["integration", id], queryFn: () => api.get<Detail>(`/v1/integrations/${id}`), enabled: !isNew, refetchInterval: 8000 });
  const kind: IntegrationKind | undefined = isNew ? cat.data?.kinds.find((k) => k.id === params.get("kind")) : existing.data?.kind;
  const [d, setD] = useState<Draft | null>(null);
  const [newKey, setNewKey] = useState<string | null>(null);
  useEffect(() => {
    if (d) return;
    if (isNew && kind) setD({ name: kind.name, config: {}, secrets: {}, mapping: kind.default_mapping ?? [], active: true, sync_minutes: kind.pull ? 60 : 0 });
    if (!isNew && existing.data) {
      const i = existing.data.integration;
      setD({ name: i.name, config: { ...i.config }, secrets: {}, mapping: i.mapping ?? [], active: i.active, sync_minutes: i.sync_minutes });
    }
  }, [isNew, kind, existing.data, d]);

  const save = useMutation({
    mutationFn: () => {
      const body = { kind: kind!.id, ...d!, name: d!.name.trim(), mapping: d!.mapping.map((m) => ({ ...m, remote: m.remote.trim(), attr: m.attr.trim() })) };
      return isNew ? api.post<{ id: number; api_key?: string }>("/v1/integrations", body) : api.put<{ id: number }>(`/v1/integrations/${id}`, body);
    },
    onSuccess: (r: { id: number; api_key?: string }) => {
      qc.invalidateQueries({ queryKey: ["integrations"] });
      if (r.api_key) setNewKey(r.api_key);
      toast({ text: isNew ? "Integration connected" : "Integration saved" });
      if (isNew) nav(`/settings/integrations/${r.id}`, { replace: true });
      else qc.invalidateQueries({ queryKey: ["integration", id] });
      setD(null);
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/integrations/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["integrations"] }); toast({ text: "Integration deleted" }); nav("/settings/integrations"); },
  });

  const back = <Link className="back" to="/settings/integrations">Integrations</Link>;
  if (!admin && isNew) {
    return <div className="page"><PageHead back={back} title="Integration" /><div className="panel"><Empty title="Only admins can connect integrations" /></div></div>;
  }
  if (!kind || !d) {
    return <div className="page"><PageHead back={back} title="Integration" /><ErrorNote error={existing.error ?? cat.error} />
      {isNew && cat.data && !kind && <div className="panel"><Empty title="Unknown integration type">Pick one from the Integrations page.</Empty></div>}</div>;
  }
  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const saved = existing.data?.integration;
  const crm = kind.pull || kind.push;
  const mapIssues = crm ? mappingProblems(d.mapping) : [];
  const missing = [...kind.config.filter((f) => f.required && !d.config[f.key]?.trim()),
    ...kind.secrets.filter((f) => f.required && !d.secrets[f.key]?.trim() && !saved?.secrets?.[f.key])];
  const problem = !d.name.trim() ? "Name the integration."
    : missing.length ? `Fill in ${missing[0].label}.`
    : mapIssues.some(Boolean) ? "Fix the field mapping." : null;

  return (
    <div className="page">
      <PageHead
        back={back}
        title={isNew ? `Connect ${kind.name}` : saved?.name ?? kind.name}
        intro={kind.description}
        actions={admin && <>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => confirm("Delete this integration? Its settings, log and audience links go too. Contacts already synced stay.") && remove.mutate()}>Delete</button>}
          <button className="btn primary" disabled={!!problem || save.isPending} title={problem ?? undefined} onClick={() => save.mutate()}>{isNew ? "Connect" : "Save"}</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error ?? existing.error} />
      {newKey && <NewKeyPanel apiKey={newKey} onClose={() => setNewKey(null)} />}
      {saved && <p className="small" style={{ marginBottom: 12 }}><StatusBadge i={saved} /> {saved.last_sync_at && <span className="muted">Last sync {timeAgo(saved.last_sync_at)}</span>}
        {saved.status === "error" && saved.last_error && <span className="int-err-full"> {saved.last_error}</span>}</p>}

      <section className="panel section">
        <div className="panel-head"><h3>Settings</h3></div>
        <div className="panel-body">
          <div className="grid-2">
            <Field label="Name"><input className="input" value={d.name} disabled={!admin} onChange={(e) => set({ name: e.target.value })} /></Field>
            {kind.config.map((f) => (
              <Field key={f.key} label={f.label + (f.required ? "" : " (optional)")} hint={f.hint}>
                <input className="input" value={d.config[f.key] ?? ""} disabled={!admin} placeholder={f.default || f.placeholder}
                  onChange={(e) => set({ config: { ...d.config, [f.key]: e.target.value } })} />
              </Field>
            ))}
            {kind.secrets.map((f) => (
              <Field key={f.key} label={f.label + (f.required ? "" : " (optional)")}
                hint={saved?.secrets?.[f.key] ? "Stored encrypted. Leave empty to keep the saved value." : f.hint ?? "Stored encrypted; never shown again."}>
                <input className="input" type="password" autoComplete="off" value={d.secrets[f.key] ?? ""} disabled={!admin}
                  placeholder={saved?.secrets?.[f.key] || f.placeholder} onChange={(e) => set({ secrets: { ...d.secrets, [f.key]: e.target.value } })} />
              </Field>
            ))}
            {kind.pull && (
              <Field label="Pull contacts" hint="Pulse asks the CRM for records changed since the last pull.">
                <select className="select" value={d.sync_minutes} disabled={!admin} onChange={(e) => set({ sync_minutes: Number(e.target.value) })}>
                  {PULL_EVERY.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                </select>
              </Field>
            )}
          </div>
          <label className="check"><input type="checkbox" checked={d.active} disabled={!admin} onChange={(e) => set({ active: e.target.checked })} />Active. Turn off to pause every sync without deleting the integration.</label>
        </div>
      </section>

      {crm && <MappingEditor id={isNew ? null : id!} kind={kind} rows={d.mapping} issues={mapIssues} readOnly={!admin} onChange={(mapping) => set({ mapping })} />}
      {isNew && <p className="small muted">Connect first; then you can test the connection{crm ? ", pull and push contacts" : ""} and see the sync log here.</p>}

      {!isNew && saved && existing.data && (
        <>
          {kind.id !== "rest_hooks" && admin && <TestPanel id={id!} />}
          {kind.pull && admin && <PullPanel id={id!} />}
          {kind.push && admin && <PushPanel id={id!} kind={kind} />}
          {kind.id === "shopify" && <ShopifyPanel id={id!} url={existing.data.webhook_url ?? ""} topics={cat.data?.shopify_topics ?? []} admin={admin} />}
          {kind.hooks && <HooksPanel id={id!} detail={existing.data} admin={admin} onKey={setNewKey} />}
          {kind.audiences && <AudiencesPanel id={id!} audiences={existing.data.audiences ?? []} admin={admin} />}
          <LogPanel log={existing.data.log} />
        </>
      )}
    </div>
  );
}

function NewKeyPanel({ apiKey, onClose }: { apiKey: string; onClose: () => void }) {
  const copy = useCopy();
  return (
    <div className="alert ok int-key">
      <strong>Your API key. Copy it now: Pulse won't show it again.</strong>
      <div className="wh-secret" style={{ marginTop: 8 }}>
        <code>{apiKey}</code>
        <button className="btn sm" onClick={() => copy(apiKey, "API key copied")}>Copy</button>
        <button className="btn sm" onClick={onClose}>Done</button>
      </div>
    </div>
  );
}

// ---------- CRM ----------

function MappingEditor({ id, kind, rows, issues, readOnly, onChange }: {
  id: string | null; kind: IntegrationKind; rows: FieldMap[]; issues: string[]; readOnly: boolean; onChange: (r: FieldMap[]) => void;
}) {
  const attrs = useQuery({ queryKey: ["attributes"], queryFn: () => api.get<{ attributes: AttributeDef[]; undefined: string[] }>("/v1/attributes") });
  const fields = useMutation({ mutationFn: () => api.get<{ fields: { name: string; label: string; type?: string; read_only?: boolean }[] }>(`/v1/integrations/${id}/fields`) });
  const attrNames = useMemo(() => Array.from(new Set(["email", "phone", "first_name", "last_name", "city", ...(attrs.data?.attributes ?? []).map((a) => a.key), ...(attrs.data?.undefined ?? [])])), [attrs.data]);
  const setRow = (i: number, p: Partial<FieldMap>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...p } : r)));
  const listId = `int-remote-${kind.id}`;
  return (
    <section className="panel section">
      <div className="panel-head">
        <h3>Field mapping</h3>
        {id && !readOnly && <button className="btn sm" disabled={fields.isPending} onClick={() => fields.mutate()}>{fields.isPending ? "Loading…" : `Load fields from ${kind.name}`}</button>}
      </div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 10 }}>
          Pulls fill Pulse attributes from {kind.name}; pushes write the attributes back. The record's {kind.name} id is kept as <code>{kind.remote_id_attr}</code>.
          Attributes Pulse keeps itself (tags, points, stage, consent…) can only go out to {kind.name}.
        </p>
        <ErrorNote error={fields.error} />
        {fields.data && <p className="xs muted">{fields.data.fields.length} fields loaded: type in the {kind.name} column to pick one.</p>}
        <table className="table int-map">
          <thead><tr><th>{kind.name} field</th><th>Direction</th><th>Pulse attribute</th><th /></tr></thead>
          <tbody>{rows.map((r, i) => (
            <tr key={i}>
              <td><input className="input" list={listId} value={r.remote} disabled={readOnly} aria-label={`${kind.name} field`} onChange={(e) => setRow(i, { remote: e.target.value })} /></td>
              <td>
                <select className="select" value={r.dir ?? "both"} disabled={readOnly} aria-label="Direction" onChange={(e) => setRow(i, { dir: e.target.value as FieldMap["dir"] })}>
                  <option value="both">Both ways</option><option value="pull">Into Pulse</option><option value="push">Out to {kind.name}</option>
                </select>
              </td>
              <td>
                <input className="input" list="int-attrs" value={r.attr} disabled={readOnly} aria-label="Pulse attribute" onChange={(e) => setRow(i, { attr: e.target.value })} />
                {issues[i] && <div className="xs int-err-full">{issues[i]}</div>}
              </td>
              <td>{!readOnly && <button className="btn sm" aria-label="Remove row" onClick={() => onChange(rows.filter((_, j) => j !== i))}>×</button>}</td>
            </tr>
          ))}</tbody>
        </table>
        {!readOnly && <button className="btn sm" onClick={() => onChange([...rows, { remote: "", attr: "", dir: "both" }])}>Add field</button>}
        <datalist id={listId}>{(fields.data?.fields ?? []).map((f) => <option key={f.name} value={f.name}>{f.label}{f.read_only ? " (read only)" : ""}</option>)}</datalist>
        <datalist id="int-attrs">{attrNames.map((a) => <option key={a} value={a} />)}</datalist>
      </div>
    </section>
  );
}

function TestPanel({ id }: { id: string }) {
  const qc = useQueryClient();
  const test = useMutation({ mutationFn: () => api.post<TestResult>(`/v1/integrations/${id}/test`), onSettled: () => qc.invalidateQueries({ queryKey: ["integration", id] }) });
  const r = test.data;
  return (
    <section className="panel section">
      <div className="panel-head">
        <h3>Test connection</h3>
        <button className="btn sm" disabled={test.isPending} onClick={() => test.mutate()}>{test.isPending ? "Testing…" : "Test connection"}</button>
      </div>
      <div className="panel-body">
        <p className="small muted">Checks the saved settings with a harmless read. Save your changes first.</p>
        {r && (r.ok
          ? <div className="alert ok" style={{ marginTop: 12, marginBottom: 0 }}>It works ({r.duration_ms} ms).</div>
          : <div className="alert error" style={{ marginTop: 12, marginBottom: 0 }}><strong>It didn't work</strong> after {r.duration_ms} ms.<br />{r.error}</div>)}
        <ErrorNote error={test.error} />
      </div>
    </section>
  );
}

function PullPanel({ id }: { id: string }) {
  const qc = useQueryClient();
  const toast = useToast();
  const pull = useMutation({
    mutationFn: (restart: boolean) => api.post(`/v1/integrations/${id}/pull`, { restart }),
    onSuccess: () => { toast({ text: "Pull starts within a few seconds" }); qc.invalidateQueries({ queryKey: ["integration", id] }); },
  });
  return (
    <section className="panel section">
      <div className="panel-head">
        <h3>Pull contacts</h3>
        <div className="row" style={{ gap: 6 }}>
          <button className="btn sm" disabled={pull.isPending} onClick={() => pull.mutate(false)}>Pull now</button>
          <button className="btn sm" disabled={pull.isPending} onClick={() => confirm("Pull every record again, from the start? Unchanged contacts stay as they are.") && pull.mutate(true)}>Pull everything again</button>
        </div>
      </div>
      <div className="panel-body">
        <p className="small muted">Records become <code>crm_synced</code> events on the contact with that email (or a new contact), so segments and journeys react at once. The log below shows each pull.</p>
        <ErrorNote error={pull.error} />
      </div>
    </section>
  );
}

function PushPanel({ id, kind }: { id: string; kind: IntegrationKind }) {
  const [ext, setExt] = useState("");
  const preview = useMutation({ mutationFn: () => api.post<{ fields: Record<string, unknown>; remote_id?: string }>(`/v1/integrations/${id}/preview`, { external_id: ext.trim() }) });
  const push = useMutation({ mutationFn: () => api.post<{ ok: boolean; remote_id?: string; error?: string }>(`/v1/integrations/${id}/push`, { external_id: ext.trim() }) });
  const r = push.data;
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Push a contact</h3></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 10 }}>Sends one contact to {kind.name} through the mapping: the record Pulse knows, else the one with the contact's email, else a new one. Journeys do the same with “Push contact to integration”.</p>
        <div className="row">
          <input className="input" style={{ maxWidth: 280 }} value={ext} placeholder="Contact's external ID" aria-label="Contact's external ID" onChange={(e) => setExt(e.target.value)} />
          <button className="btn sm" disabled={!ext.trim() || preview.isPending} onClick={() => preview.mutate()}>Preview</button>
          <button className="btn sm primary" disabled={!ext.trim() || push.isPending} onClick={() => push.mutate()}>{push.isPending ? "Pushing…" : "Push"}</button>
        </div>
        {preview.data && <pre className="wh-snippet int-pre">{JSON.stringify({ [`${kind.name} record`]: preview.data.remote_id ?? "new or matched by email", fields: preview.data.fields }, null, 2)}</pre>}
        {r && (r.ok
          ? <div className="alert ok" style={{ marginTop: 12, marginBottom: 0 }}>Pushed: {kind.name} record {r.remote_id}.</div>
          : <div className="alert error" style={{ marginTop: 12, marginBottom: 0 }}>{r.error}</div>)}
        <ErrorNote error={preview.error ?? push.error} />
      </div>
    </section>
  );
}

// ---------- Shopify ----------

function ShopifyPanel({ id, url, topics, admin }: { id: string; url: string; topics: string[]; admin: boolean }) {
  const copy = useCopy();
  const qc = useQueryClient();
  const toast = useToast();
  const preview = useMutation({ mutationFn: () => api.post<Record<string, unknown>>(`/v1/integrations/${id}/preview`, {}) });
  const rev = useQuery({ queryKey: ["revenue-settings"], queryFn: () => api.get<{ settings: RevenueSettings }>("/v1/revenue/settings"), retry: false });
  const cur = rev.data?.settings;
  const useOrders = useMutation({
    mutationFn: () => api.put("/v1/revenue/settings", { event: "order_completed", amount_prop: "amount", currency: cur?.currency || "" }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["revenue-settings"] }); toast({ text: "Revenue reports now count Shopify orders" }); },
  });
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Shopify webhooks</h3></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 10 }}>
          In Shopify admin → Settings → Notifications → Webhooks, create a JSON webhook to this URL for each topic below.
          Pulse checks Shopify's signature with the signing secret above.
        </p>
        <div className="wh-secret"><code>{url}</code><button className="btn sm" onClick={() => copy(url, "URL copied")}>Copy</button></div>
        <table className="table" style={{ marginTop: 12 }}>
          <thead><tr><th>Topic</th><th>Becomes</th></tr></thead>
          <tbody>
            {topics.map((t) => (
              <tr key={t}><td><code>{t}</code></td><td className="small">{t === "orders/create" ? <><code>order_completed</code> with amount, currency and items</>
                : t === "customers/create" ? <><code>customer_created</code>, and the contact's name and phone</>
                : <><code>cart_updated</code> with the checkout link (completed checkouts are skipped)</>}</td></tr>
            ))}
          </tbody>
        </table>
        {cur && cur.event !== "order_completed" && (
          <div className="alert" style={{ marginTop: 12, marginBottom: 0 }}>
            Revenue reports count <code>{cur.event}</code> events. Count Shopify orders instead?{" "}
            {admin && <button className="btn sm" disabled={useOrders.isPending} onClick={() => useOrders.mutate()}>Use order_completed</button>}
          </div>
        )}
        <div className="row" style={{ marginTop: 12 }}>
          <button className="btn sm" disabled={preview.isPending} onClick={() => preview.mutate()}>Preview a sample order</button>
        </div>
        {preview.data && <pre className="wh-snippet int-pre">{JSON.stringify(preview.data, null, 2)}</pre>}
        <ErrorNote error={preview.error ?? useOrders.error} />
      </div>
    </section>
  );
}

// ---------- Zapier / Make ----------

const ENDPOINTS: [string, string][] = [
  ["GET /me", "connection test: the workspace and integration"],
  ["GET /segments", "segments, for a trigger's dropdown"],
  ["GET /samples?event=", "a sample of what a trigger sends"],
  ["POST /subscriptions", "{target_url, event, segment_id?, form_id?}: subscribe a trigger"],
  ["DELETE /subscriptions/{id}", "unsubscribe (a 410 answer also unsubscribes)"],
  ["POST /contacts", "{external_id?, email?, phone?, attrs}: create or update a contact"],
  ["POST /events", "{external_id? | email?, name, props}: track an event"],
  ["POST /tags", "{external_id? | email?, tags, remove?}: add or remove tags"],
];

function HooksPanel({ id, detail, admin, onKey }: { id: string; detail: Detail; admin: boolean; onKey: (k: string) => void }) {
  const qc = useQueryClient();
  const copy = useCopy();
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const segName = (sid?: number) => segs.data?.segments.find((s) => s.id === sid)?.name ?? `segment ${sid}`;
  const rotate = useMutation({ mutationFn: () => api.post<{ api_key: string }>(`/v1/integrations/${id}/key`), onSuccess: (r) => { onKey(r.api_key); qc.invalidateQueries({ queryKey: ["integration", id] }); } });
  const unsub = useMutation({ mutationFn: (sub: number) => api.del(`/v1/integrations/${id}/subscriptions/${sub}`), onSuccess: () => qc.invalidateQueries({ queryKey: ["integration", id] }) });
  const base = detail.hooks_url ?? "";
  const subs = detail.subscriptions ?? [];
  const ds = detail.deliveries ?? [];
  return (
    <>
      <section className="panel section">
        <div className="panel-head"><h3>API key and endpoints</h3></div>
        <div className="panel-body">
          <div className="wh-secret">
            <code>{detail.integration.key_hint ? "pzk_••••••••" + detail.integration.key_hint : "No key"}</code>
            {admin && <button className="btn sm danger" disabled={rotate.isPending} onClick={() => confirm("Create a new API key? The current one stops working right away, so update Zapier or Make too.") && rotate.mutate()}>Rotate key</button>}
          </div>
          <p className="small muted" style={{ margin: "12px 0 6px" }}>Send <code>Authorization: Bearer pzk_…</code> to these endpoints under</p>
          <div className="wh-secret"><code>{base}</code><button className="btn sm" onClick={() => copy(base, "URL copied")}>Copy</button></div>
          <table className="table" style={{ marginTop: 12 }}>
            <tbody>{ENDPOINTS.map(([e, what]) => <tr key={e}><td><code>{e}</code></td><td className="small muted">{what}</td></tr>)}</tbody>
          </table>
          <p className="xs muted">Triggers: <code>contact_created</code>, <code>segment_entered</code> / <code>segment_exited</code> (optionally one segment), <code>form_submitted</code> (optionally one form), or any event name. Deliveries are signed and retried like webhooks.</p>
          <ErrorNote error={rotate.error} />
        </div>
      </section>
      <section className="section">
        <h3>Subscriptions</h3>
        <div className="panel">
          {subs.length === 0 ? <Empty title="No subscriptions yet">Turn on a Zap or scenario that uses a Pulse trigger and it shows up here.</Empty> : (
            <table className="table">
              <thead><tr><th>Event</th><th>Only</th><th>Target URL</th><th>Since</th><th /></tr></thead>
              <tbody>{subs.map((s) => (
                <tr key={s.id}>
                  <td><code>{s.events.join(", ")}</code></td>
                  <td className="small">{s.segment_id ? segName(s.segment_id) : s.form_id ? `form ${s.form_id}` : <span className="muted">All</span>}</td>
                  <td className="xs muted int-url">{s.url}</td>
                  <td className="small muted">{timeAgo(s.created_at)}</td>
                  <td>{admin && <button className="btn sm" disabled={unsub.isPending} onClick={() => confirm("Remove this subscription? The Zap stops getting this trigger.") && unsub.mutate(s.id)}>Remove</button>}</td>
                </tr>
              ))}</tbody>
            </table>
          )}
        </div>
        <ErrorNote error={unsub.error} />
      </section>
      {ds.length > 0 && (
        <section className="section">
          <h3>Recent deliveries</h3>
          <div className="panel table-scroll">
            <table className="table">
              <thead><tr><th>When</th><th>Event</th><th className="num">Attempt</th><th>Answer</th><th>Error</th></tr></thead>
              <tbody>{ds.map((x) => (
                <tr key={x.id}>
                  <td className="muted" style={{ whiteSpace: "nowrap" }}>{fmt(x.at)}</td>
                  <td><code>{x.event_name}</code></td>
                  <td className="num">{x.attempt}</td>
                  <td>{x.status >= 200 && x.status < 300 ? <span className="badge ok">{x.status}</span> : x.status ? <span className="badge bad">{x.status}</span> : <span className="muted">None</span>}</td>
                  <td className="xs muted int-err" title={x.error}>{x.error}</td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        </section>
      )}
    </>
  );
}

// ---------- ad audiences ----------

function AudiencesPanel({ id, audiences, admin }: { id: string; audiences: AudienceSync[]; admin: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const canSegments = useCan("segments", "edit");
  const segs = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments") });
  const all = segs.data?.segments ?? [];
  const segName = (sid: number) => all.find((s) => s.id === sid)?.name ?? `Segment ${sid}`;
  const refresh = () => qc.invalidateQueries({ queryKey: ["integration", id] });
  const [seg, setSeg] = useState(0);
  const [mode, setMode] = useState<"new" | "existing">("new");
  const [name, setName] = useState("");
  const [aud, setAud] = useState("");
  const link = useMutation({
    mutationFn: () => api.post(`/v1/integrations/${id}/audiences`, mode === "new"
      ? { segment_id: seg, audience_name: name.trim() || `${segName(seg)} (Pulse)` }
      : { segment_id: seg, audience_id: aud.trim() }),
    onSuccess: () => { toast({ text: "Linked. The first upload starts within seconds." }); setSeg(0); setName(""); setAud(""); refresh(); },
  });
  const toggle = useMutation({ mutationFn: (a: AudienceSync) => api.put(`/v1/integrations/${id}/audiences/${a.id}`, { active: !a.active }), onSuccess: refresh });
  const sync = useMutation({ mutationFn: (a: AudienceSync) => api.post(`/v1/integrations/${id}/audiences/${a.id}/reconcile`), onSuccess: () => { toast({ text: "A full sync starts within seconds" }); refresh(); } });
  const unlink = useMutation({ mutationFn: (a: AudienceSync) => api.del(`/v1/integrations/${id}/audiences/${a.id}`), onSuccess: refresh });
  const mayLink = admin && canSegments;
  return (
    <>
      <section className="section">
        <h3>Audiences</h3>
        <p className="xs muted" style={{ marginBottom: 10 }}>
          Pulse uploads SHA-256 hashes of each member's email (trimmed, lower case) and phone (E.164), then adds and removes contacts as they enter and leave,
          with a full check every day. Contacts unsubscribed from everything are never uploaded.
        </p>
        <div className="panel">
          {audiences.length === 0 ? <Empty title="No segments linked yet">Link a segment below to keep an audience in step with it.</Empty> : (
            <div className="table-scroll">
              <table className="table">
                <thead><tr><th>Segment</th><th>Audience</th><th>Status</th><th className="num">In audience</th><th className="num">Added</th><th className="num">Removed</th>
                  <th className="num" title="Members left out at the last full sync: no consent, or no email or phone">Left out</th><th className="num">Waiting</th><th>Last sync</th><th /></tr></thead>
                <tbody>{audiences.map((a) => (
                  <tr key={a.id}>
                    <td>{segName(a.segment_id)}</td>
                    <td className="small">{a.audience_name || <span className="muted">—</span>}<div className="xs muted">{a.audience_id}</div></td>
                    <td>{!a.active ? <span className="badge">Paused</span> : a.status === "error" ? <span className="badge bad">Error</span> : a.status === "ok" ? <span className="badge ok">In sync</span> : <span className="badge info">Syncing</span>}
                      {a.last_error && <div className="xs int-err" title={a.last_error}>{a.last_error}</div>}</td>
                    <td className="num">{a.uploaded.toLocaleString()}</td>
                    <td className="num">{a.added.toLocaleString()}</td>
                    <td className="num">{a.removed.toLocaleString()}</td>
                    <td className="num">{a.skipped.toLocaleString()}</td>
                    <td className="num">{a.queued.toLocaleString()}</td>
                    <td className="small muted">{a.last_sync_at ? timeAgo(a.last_sync_at) : "Not yet"}</td>
                    <td className="int-actions">{mayLink && <>
                      <button className="btn sm" onClick={() => sync.mutate(a)}>Sync now</button>
                      <button className="btn sm" onClick={() => toggle.mutate(a)}>{a.active ? "Pause" : "Resume"}</button>
                      <button className="btn sm danger" onClick={() => confirm("Unlink this segment? Pulse stops updating the audience; the audience keeps contacts already uploaded.") && unlink.mutate(a)}>Unlink</button>
                    </>}</td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
          )}
        </div>
        <ErrorNote error={toggle.error ?? sync.error ?? unlink.error} />
      </section>
      {admin && (
        <section className="panel section">
          <div className="panel-head"><h3>Link a segment</h3></div>
          <div className="panel-body">
            {!canSegments && <p className="small muted">Linking a segment also needs edit access to segments.</p>}
            <div className="grid-2">
              <Field label="Segment">
                <select className="select" value={seg} onChange={(e) => setSeg(Number(e.target.value))}>
                  <option value={0}>Choose a segment</option>
                  {all.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
                </select>
              </Field>
              <Field label="Audience">
                <label className="check"><input type="radio" checked={mode === "new"} onChange={() => setMode("new")} />Create a new audience</label>
                <label className="check"><input type="radio" checked={mode === "existing"} onChange={() => setMode("existing")} />Use an existing audience</label>
              </Field>
              {mode === "new"
                ? <Field label="New audience name"><input className="input" value={name} placeholder={seg ? `${segName(seg)} (Pulse)` : "Segment name (Pulse)"} onChange={(e) => setName(e.target.value)} /></Field>
                : <Field label="Audience ID" hint="Facebook: the custom audience id. Google: the user list id or customers/…/userLists/… resource name."><input className="input" value={aud} onChange={(e) => setAud(e.target.value)} /></Field>}
            </div>
            <button className="btn primary" disabled={!mayLink || !seg || (mode === "existing" && !aud.trim()) || link.isPending} onClick={() => link.mutate()}>{link.isPending ? "Linking…" : "Link segment"}</button>
            <ErrorNote error={link.error} />
          </div>
        </section>
      )}
      {admin && <CheckContact id={id} segments={all} />}
    </>
  );
}

function CheckContact({ id, segments }: { id: string; segments: Segment[] }) {
  const [ext, setExt] = useState("");
  const [seg, setSeg] = useState(0);
  const check = useMutation({ mutationFn: () => api.post<{ included: boolean; reason?: string; email_sha256?: string; phone_sha256?: string }>(`/v1/integrations/${id}/preview`, { external_id: ext.trim(), segment_id: seg || undefined }) });
  const r = check.data;
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Check a contact</h3></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 10 }}>See whether a contact would be uploaded and the hashes Pulse would send. Nothing is sent.</p>
        <div className="row">
          <input className="input" style={{ maxWidth: 240 }} value={ext} placeholder="Contact's external ID" aria-label="Contact's external ID" onChange={(e) => setExt(e.target.value)} />
          <select className="select inline" value={seg} aria-label="Segment" onChange={(e) => setSeg(Number(e.target.value))}>
            <option value={0}>Any segment</option>
            {segments.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          <button className="btn sm" disabled={!ext.trim() || check.isPending} onClick={() => check.mutate()}>Check</button>
        </div>
        {r && (
          <div className={`alert ${r.included ? "ok" : "error"}`} style={{ marginTop: 12, marginBottom: 0 }}>
            {r.included ? "Included." : `Left out: ${r.reason}.`}
            <div className="xs int-hash">Email SHA-256: {r.email_sha256 || "none"}<br />Phone SHA-256: {r.phone_sha256 || "none"}</div>
          </div>
        )}
        <ErrorNote error={check.error} />
      </div>
    </section>
  );
}

// ---------- log ----------

const ACTIONS: Record<string, string> = { pull: "Pull", push: "Push", webhook: "Webhook", audience: "Audience", reconcile: "Full sync", test: "Test" };

function LogPanel({ log }: { log: IntegrationLogEntry[] }) {
  return (
    <section className="section">
      <h3>Sync log</h3>
      <p className="xs muted" style={{ marginBottom: 10 }}>The latest 100 entries, updated every few seconds; kept for a week.</p>
      <div className="panel">
        {log.length === 0 ? <Empty title="Nothing yet">Pulls, pushes, webhooks, uploads and tests show up here.</Empty> : (
          <div className="table-scroll">
            <table className="table">
              <thead><tr><th>When</th><th>What</th><th>Result</th><th className="num">Count</th><th>Details</th></tr></thead>
              <tbody>{log.map((e) => (
                <tr key={e.id}>
                  <td className="muted" style={{ whiteSpace: "nowrap" }} title={e.at}>{fmt(e.at)}</td>
                  <td>{ACTIONS[e.action] ?? e.action}</td>
                  <td>{e.ok ? <span className="badge ok">OK</span> : <span className="badge bad">Failed</span>}</td>
                  <td className="num">{e.count || ""}</td>
                  <td className="xs muted int-msg" title={e.message}>{e.message}</td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}
