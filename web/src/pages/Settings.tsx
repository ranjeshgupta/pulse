import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, type AttributeDef, type AttrOption, type AttrType, type IdentitySettings, type PointRule, type RoleKey, type SSOConfig, type User } from "../api";
import { useIsAdmin, useMe } from "../auth";
import { Empty, ErrorNote, Field, Modal, PageHead, timeAgo, useToast } from "../ui";
import { RoleOptions, roleName, useRoles } from "./Roles";

export function Team() {
  const admin = useIsAdmin();
  const me = useMe().data;
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["users"], queryFn: () => api.get<{ users: User[] }>("/v1/users") });
  const roles = useRoles().data;
  const toast = useToast();
  const [adding, setAdding] = useState(false);
  const remove = useMutation({ mutationFn: (id: number) => api.del(`/v1/users/${id}`), onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }) });
  const setRole = useMutation({
    mutationFn: (v: { user: User; role: RoleKey }) => api.put(`/v1/users/${v.user.id}/role`, { role: v.role }),
    onSuccess: (_, v) => {
      qc.invalidateQueries({ queryKey: ["users"] });
      qc.invalidateQueries({ queryKey: ["roles"] });
      toast({ text: `${v.user.name || v.user.email} is now ${roleName(v.role, roles)}` });
    },
    onError: (e) => toast({ text: e.message, error: true }),
  });
  return (
    <div className="page">
      <PageHead title="Team" intro="Admins manage settings, sign-in and providers. Editors build segments, journeys and content. Viewers look but don't change anything. Make your own roles under Roles." actions={admin && <button className="btn primary" onClick={() => setAdding(true)}>Add teammate</button>} />
      <ErrorNote error={q.error ?? remove.error ?? setRole.error} />
      <div className="panel">
        <table className="table">
          <thead><tr><th>Name</th><th>Email</th><th>Role</th><th>Signs in with</th><th>Last sign-in</th><th /></tr></thead>
          <tbody>{q.data?.users.map((u) => (
            <tr key={u.id}>
              <td>{u.name || <span className="faint">not set</span>}</td>
              <td>{u.email}</td>
              <td>
                {admin ? (
                  <select
                    className="select inline"
                    aria-label={`Role for ${u.email}`}
                    value={u.role}
                    disabled={u.id === me?.user?.id || setRole.isPending}
                    title={u.id === me?.user?.id ? "You can't change your own role" : undefined}
                    onChange={(e) => setRole.mutate({ user: u, role: e.target.value as RoleKey })}
                  >
                    <RoleOptions roles={roles} />
                  </select>
                ) : roleName(u.role, roles)}
              </td>
              <td>{signsInWith(u)}</td>
              <td className="muted">{timeAgo(u.last_login_at)}</td>
              <td className="num">{admin && u.id !== me?.user?.id && <button className="btn ghost sm" onClick={() => confirm(`Remove ${u.email}? They're signed out everywhere.`) && remove.mutate(u.id)}>Remove</button>}</td>
            </tr>
          ))}</tbody>
        </table>
      </div>
      {adding && <AddUser onClose={() => setAdding(false)} />}
    </div>
  );
}

function AddUser({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const roles = useRoles().data;
  const [u, setU] = useState<{ email: string; name: string; role: RoleKey; password: string }>({ email: "", name: "", role: "editor", password: "" });
  const add = useMutation({
    mutationFn: () => api.post("/v1/users", u),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["users"] }); toast({ text: `${u.email} added` }); onClose(); },
  });
  return (
    <Modal title="Add a teammate" onClose={onClose} footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" onClick={() => add.mutate()}>Add teammate</button></>}>
      <ErrorNote error={add.error} />
      <Field label="Email"><input className="input" type="email" value={u.email} onChange={(e) => setU({ ...u, email: e.target.value })} autoFocus /></Field>
      <Field label="Name"><input className="input" value={u.name} onChange={(e) => setU({ ...u, name: e.target.value })} /></Field>
      <Field label="Role">
        <select className="select" value={u.role} onChange={(e) => setU({ ...u, role: e.target.value as RoleKey })}><RoleOptions roles={roles} /></select>
      </Field>
      <Field label="Temporary password (optional)" hint="Leave empty if they'll sign in with SSO. Otherwise share it with them privately; at least 10 characters.">
        <input className="input" type="password" autoComplete="new-password" value={u.password} onChange={(e) => setU({ ...u, password: e.target.value })} />
      </Field>
    </Modal>
  );
}

export function Access() {
  const me = useMe().data;
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["sso"], queryFn: () => api.get<SSOConfig>("/v1/settings/sso") });
  const roles = useRoles().data;
  const [sso, setSSO] = useState<SSOConfig | null>(null);
  const [newKey, setNewKey] = useState<string | null>(null);
  useEffect(() => { if (q.data && !sso) setSSO({ ...q.data, client_secret: "" }); }, [q.data, sso]);
  const save = useMutation({
    mutationFn: () => api.put<SSOConfig>("/v1/settings/sso", sso),
    onSuccess: (c) => { qc.setQueryData(["sso"], c); setSSO({ ...c, client_secret: "" }); toast({ text: "Single sign-on settings saved" }); },
  });
  const remove = useMutation({
    mutationFn: () => api.del("/v1/settings/sso"),
    onSuccess: () => {
      const blank: SSOConfig = { enabled: false, issuer: "", discovery_url: "", client_id: "", client_secret: "", has_client_secret: false, auto_create_users: true, default_role: "editor" };
      qc.setQueryData(["sso"], blank); setSSO(blank); toast({ text: "Single sign-on removed" });
    },
  });
  const saved = !!q.data?.issuer;
  const rotate = useMutation({ mutationFn: () => api.post<{ api_key: string }>("/v1/settings/api-key"), onSuccess: (r) => setNewKey(r.api_key) });
  const callback = `${window.location.origin}/v1/auth/sso/callback`;

  return (
    <div className="page">
      <PageHead title="Sign-in & keys" intro="How your team signs in, and the keys your servers and website use." />
      <section className="panel">
        <div className="panel-head"><h3>Single sign-on (OIDC)</h3>{sso?.enabled ? <span className="badge ok">On</span> : <span className="badge">Off</span>}</div>
        <div className="panel-body">
          <p className="small muted" style={{ marginBottom: 16, maxWidth: "70ch" }}>
            Works with Keycloak, Microsoft Entra ID, Google Workspace, Okta or any OpenID Connect provider. Register Pulse with your provider using this redirect URI: <code>{callback}</code>. Teammates choose "Sign in with SSO" and enter <strong>{me?.workspace.slug}</strong>.
          </p>
          <ErrorNote error={save.error ?? remove.error ?? q.error} />
          {sso && (
            <>
              <div className="grid-2">
                <Field label="Issuer URL" hint="e.g. https://auth.example.com/realms/acme"><input className="input" value={sso.issuer} onChange={(e) => setSSO({ ...sso, issuer: e.target.value.trim() })} /></Field>
                <Field label="Discovery URL (optional)" hint="Only if Pulse reaches the provider at a different address than browsers do."><input className="input" value={sso.discovery_url} onChange={(e) => setSSO({ ...sso, discovery_url: e.target.value.trim() })} /></Field>
                <Field label="Client ID"><input className="input" value={sso.client_id} onChange={(e) => setSSO({ ...sso, client_id: e.target.value.trim() })} /></Field>
                <Field label="Client secret" hint={sso.has_client_secret ? "Stored encrypted. Leave empty to keep it." : "Stored encrypted."}><input className="input" type="password" autoComplete="off" value={sso.client_secret ?? ""} onChange={(e) => setSSO({ ...sso, client_secret: e.target.value })} /></Field>
              </div>
              <label className="check"><input type="checkbox" checked={sso.auto_create_users} onChange={(e) => setSSO({ ...sso, auto_create_users: e.target.checked })} />Give anyone your provider signs in access to this workspace</label>
              <Field label="Their role"><select className="select inline" value={sso.default_role} onChange={(e) => setSSO({ ...sso, default_role: e.target.value as RoleKey })}><RoleOptions roles={roles} /></select></Field>
              <label className="check"><input type="checkbox" checked={sso.enabled} onChange={(e) => setSSO({ ...sso, enabled: e.target.checked })} />Allow signing in with SSO</label>
              <div className="row" style={{ gap: 8 }}>
                <button className="btn primary" disabled={save.isPending} onClick={() => save.mutate()}>Save sign-on settings</button>
                {saved && <button className="btn danger" disabled={remove.isPending}
                  onClick={() => confirm("Remove single sign-on? The provider settings and client secret are deleted, and teammates sign in with a password (anyone who only used SSO needs a password reset or a new invitation).") && remove.mutate()}>Remove single sign-on</button>}
              </div>
            </>
          )}
        </div>
      </section>

      <section className="panel section">
        <div className="panel-head"><h3>Your website</h3><a className="small" href={`/sdk/demo.html?key=${me?.workspace.site_key}`} target="_blank" rel="noreferrer">Open the test page</a></div>
        <div className="panel-body">
          <p className="small muted" style={{ marginBottom: 10, maxWidth: "72ch" }}>
            Add this before <code>&lt;/body&gt;</code> on every page. It records page views, shows your focus items and fills content slots (<code>&lt;div data-pulse-slot="home-hero"&gt;</code>). Call <code>Pulse.identify("user-id", {"{ email, name }"})</code> when someone signs in, <code>Pulse.track("purchase", {"{ amount: 499 }"})</code> for actions, and <code>Pulse.reset()</code> on sign-out.
          </p>
          <textarea className="textarea code" readOnly rows={3} onFocus={(e) => e.target.select()} value={`<script src="${window.location.origin}/sdk/pulse.js" defer onload="Pulse.init({ siteKey: '${me?.workspace.site_key}' })"></script>`} />
        </div>
      </section>

      <IdentityPanel />

      <section className="panel section">
        <div className="panel-head"><h3>Keys</h3></div>
        <div className="panel-body">
          <dl className="kv" style={{ gridTemplateColumns: "180px 1fr" }}>
            <dt>Workspace ID</dt><dd><code>{me?.workspace.slug}</code></dd>
            <dt>Site key</dt><dd><code>{me?.workspace.site_key}</code><div className="xs muted">Public: used by the web SDK on your website for on-site content.</div></dd>
            <dt>Server API key</dt><dd>Hidden. Servers send events and manage data with it.</dd>
          </dl>
          {newKey ? (
            <div className="alert ok" style={{ marginTop: 16 }}>New API key, shown only now: <code>{newKey}</code><br />The old key has stopped working. Update your servers.</div>
          ) : (
            <button className="btn danger" style={{ marginTop: 16 }} onClick={() => confirm("Create a new server API key? The current one stops working immediately.") && rotate.mutate()}>Replace API key</button>
          )}
          <ErrorNote error={rotate.error} />
        </div>
      </section>
    </div>
  );
}

// IdentityPanel turns on identity verification for the web SDK: the customer's server signs each
// signed-in user's ID, so a script on the page can't record events as someone else.
function IdentityPanel() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["identity"], queryFn: () => api.get<IdentitySettings>("/v1/settings/identity") });
  const [shown, setShown] = useState(false);
  const save = useMutation({
    mutationFn: (required: boolean) => api.put<IdentitySettings>("/v1/settings/identity", { required }),
    onSuccess: (v) => { qc.setQueryData(["identity"], v); toast({ text: v.required ? "Identity verification is on" : "Identity verification is off" }); },
  });
  const rotate = useMutation({
    mutationFn: () => api.post<IdentitySettings>("/v1/settings/identity/rotate"),
    onSuccess: (v) => { qc.setQueryData(["identity"], v); setShown(true); toast({ text: "New signing secret created" }); },
  });
  const v = q.data;
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Identity verification</h3></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 12, maxWidth: "72ch" }}>
          Your site key is public, so anyone could call <code>Pulse.identify()</code> with someone else's ID. With verification on, your server signs each signed-in user's ID with this secret and passes the result to the page: <code>Pulse.identify(userId, attrs, {"{ userHash }"})</code>. Events for a user ID without a valid hash are refused. Anonymous visitors aren't affected.
        </p>
        <ErrorNote error={q.error ?? save.error ?? rotate.error} />
        {v && (
          <>
            <dl className="kv" style={{ gridTemplateColumns: "180px 1fr", marginBottom: 14 }}>
              <dt>Signing secret</dt>
              <dd>
                {shown ? <code>{v.secret}</code> : <button className="btn sm" onClick={() => setShown(true)}>Show secret</button>}
                <div className="xs muted">Keep it on your server. userHash = hex(HMAC-SHA256(secret, user ID)).</div>
              </dd>
            </dl>
            <label className="check" style={{ marginBottom: 12 }}>
              <input type="checkbox" checked={v.required} disabled={save.isPending} onChange={(e) => {
                if (!e.target.checked || confirm("Refuse web events for users without a valid userHash? Deploy the server-side signing first.")) save.mutate(e.target.checked);
              }} />
              Require verified identity for web events
            </label>
            <div>
              <button className="btn danger sm" disabled={rotate.isPending} onClick={() => confirm("Create a new signing secret? Hashes made with the current one stop working immediately.") && rotate.mutate()}>Replace secret</button>
            </div>
          </>
        )}
      </div>
    </section>
  );
}

const TYPE_LABEL: Record<AttrType, string> = { string: "Text", number: "Number", boolean: "Yes / no", date: "Date", list: "List", enum: "Choice (enum)" };

// Attributes describes profile fields: a label and type make the segment builder offer the right
// comparisons, and imports check and convert values of numbers and yes/no fields.
export function Attributes() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["attributes"], queryFn: () => api.get<{ attributes: AttributeDef[]; undefined: string[] }>("/v1/attributes") });
  const [editing, setEditing] = useState<AttributeDef | null>(null);
  const remove = useMutation({
    mutationFn: (key: string) => api.del(`/v1/attributes/${key}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["attributes"] }); toast({ text: "Definition removed. The values on contacts stay." }); },
  });
  const defs = q.data?.attributes ?? [];
  const undef = (q.data?.undefined ?? []).filter((k) => !k.startsWith("optout_") && k !== "merged_into");
  const blank = (key = ""): AttributeDef => ({ key, label: "", type: "string", pii: false, description: "" });
  return (
    <div className="page">
      <PageHead
        title="Contact fields"
        intro="Name and type the profile fields (attributes) your team uses. Types decide which comparisons the segment builder offers, and imports check numbers and yes/no values. A list field holds several values, like tags (products a contact bought: segments ask “has” or “doesn't have”); a choice field holds one of the options you set."
        actions={<button className="btn primary" onClick={() => setEditing(blank())}>Define field</button>}
      />
      {editing && <AttributeModal def={editing} isNew={!defs.some((d) => d.key === editing.key)} onClose={() => setEditing(null)} />}
      <ErrorNote error={q.error ?? remove.error} />
      <div className="panel section">
        {defs.length === 0 && !q.isLoading ? (
          <Empty title="No contact fields defined">Define the fields you segment on most, such as plan, city or lifetime_value.</Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Attribute</th><th>Label</th><th>Type</th><th>Personal data</th><th /></tr></thead>
            <tbody>
              {defs.map((d) => (
                <tr key={d.key}>
                  <td><code>{d.key}</code></td>
                  <td>{d.label}{d.description && <div className="xs muted">{d.description}</div>}</td>
                  <td>
                    {TYPE_LABEL[d.type]}
                    {d.type === "enum" && d.options?.length ? <div className="xs muted">{d.options.map((o) => o.label || o.value).join(", ")}</div> : null}
                  </td>
                  <td>{d.pii ? <span className="badge warn">Personal</span> : <span className="faint">No</span>}</td>
                  <td className="num">
                    <button className="btn ghost sm" onClick={() => setEditing(d)}>Edit</button>
                    <button className="btn ghost sm danger" onClick={() => confirm(`Remove the definition of ${d.key}?`) && remove.mutate(d.key)}>Remove</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {undef.length > 0 && (
        <section className="panel section">
          <div className="panel-head"><h3>Seen on contacts, not defined yet</h3></div>
          <div className="panel-body row">
            {undef.map((k) => <button key={k} className="btn sm" onClick={() => setEditing(blank(k))}>{k}</button>)}
          </div>
        </section>
      )}
    </div>
  );
}

function AttributeModal({ def, isNew, onClose }: { def: AttributeDef; isNew: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [d, setD] = useState(def);
  const save = useMutation({
    mutationFn: () => api.put(`/v1/attributes/${d.key}`, d),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["attributes"] }); onClose(); },
  });
  const validKey = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/.test(d.key);
  const opts = d.options ?? [];
  const optsOK = d.type !== "enum" || (opts.length > 0 && opts.every((o) => o.value.trim()) && new Set(opts.map((o) => o.value.trim())).size === opts.length);
  const setOpt = (i: number, o: AttrOption) => setD({ ...d, options: opts.map((x, j) => (j === i ? o : x)) });
  return (
    <Modal title={isNew ? "Define attribute" : `Edit ${d.key}`} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!validKey || !optsOK || save.isPending} onClick={() => save.mutate()}>Save attribute</button>
    </>}>
      <Field label="Attribute key" hint="As it appears in events and imports: letters, digits and _.">
        <input className="input" value={d.key} disabled={!isNew && !!def.key} onChange={(e) => setD({ ...d, key: e.target.value })} placeholder="lifetime_value" />
      </Field>
      <Field label="Label"><input className="input" value={d.label} onChange={(e) => setD({ ...d, label: e.target.value })} placeholder="Lifetime value" /></Field>
      <Field label="Type">
        <select className="select" value={d.type} onChange={(e) => {
          const type = e.target.value as AttrType;
          setD({ ...d, type, options: type === "enum" ? (opts.length ? opts : [{ value: "", label: "" }]) : undefined });
        }}>
          {(Object.keys(TYPE_LABEL) as AttrType[]).map((t) => <option key={t} value={t}>{TYPE_LABEL[t]}</option>)}
        </select>
      </Field>
      {d.type === "list" && <p className="xs muted">Send a list as a JSON array (<code>["SKU-1", "SKU-2"]</code>); in imports, separate values with | or commas. An event schema can add a property's value to it on every event.</p>}
      {d.type === "enum" && (
        <fieldset className="attr-options" style={{ border: "1px solid var(--rule)", borderRadius: 8, padding: "8px 12px", display: "grid", gap: 8, marginBottom: 12 }}>
          <legend className="small">Options</legend>
          {opts.map((o, i) => (
            <div key={i} style={{ display: "grid", gridTemplateColumns: "1fr 1fr auto", gap: 8, alignItems: "center" }}>
              <input className="input" value={o.value} onChange={(e) => setOpt(i, { ...o, value: e.target.value })} placeholder="value, e.g. gold" aria-label={`Option ${i + 1} value`} />
              <input className="input" value={o.label ?? ""} onChange={(e) => setOpt(i, { ...o, label: e.target.value })} placeholder="label, e.g. Gold" aria-label={`Option ${i + 1} label`} />
              <button type="button" className="btn ghost sm danger" disabled={opts.length === 1} onClick={() => setD({ ...d, options: opts.filter((_, j) => j !== i) })} aria-label={`Remove option ${i + 1}`}>Remove</button>
            </div>
          ))}
          <button type="button" className="btn ghost sm" onClick={() => setD({ ...d, options: [...opts, { value: "", label: "" }] })}>+ Add option</button>
          {!optsOK && <p className="xs" style={{ color: "var(--danger)" }}>Each option needs a value, and values must be different.</p>}
        </fieldset>
      )}
      <Field label="Description"><input className="input" value={d.description} onChange={(e) => setD({ ...d, description: e.target.value })} placeholder="Total paid, in rupees" /></Field>
      <label className="check"><input type="checkbox" checked={d.pii} onChange={(e) => setD({ ...d, pii: e.target.checked })} />Holds personal data (phone, address, date of birth)</label>
      <ErrorNote error={save.error} />
    </Modal>
  );
}

// Scoring gives points for events, so sales can see who's warming up and segments can use
// "points is at least 50".
export function Scoring() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["point-rules"], queryFn: () => api.get<{ rules: PointRule[] }>("/v1/scoring/rules") });
  const [ev, setEv] = useState("");
  const [pts, setPts] = useState("10");
  const save = useMutation({
    mutationFn: () => api.put("/v1/scoring/rules", { event: ev.trim(), points: Number(pts) }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["point-rules"] }); toast({ text: `${ev.trim()} now gives ${pts} points` }); setEv(""); },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.del(`/v1/scoring/rules/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["point-rules"] }),
  });
  const rules = q.data?.rules ?? [];
  const n = Number(pts);
  const ok = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/.test(ev.trim()) && Number.isInteger(n) && n !== 0;
  return (
    <div className="page">
      <PageHead
        title="Scoring"
        intro="Each time a contact does one of these events, their points change. Points are a profile attribute, so segments, journey checks and exports can use them. Journeys can also add points with a step."
      />
      <ErrorNote error={q.error ?? save.error ?? remove.error} />
      <section className="panel section">
        <div className="panel-body">
          <form className="row" onSubmit={(e) => { e.preventDefault(); if (ok) save.mutate(); }}>
            <span className="small">When a contact does</span>
            <input className="input" style={{ maxWidth: 220 }} value={ev} onChange={(e) => setEv(e.target.value)} placeholder="demo_booked" aria-label="Event" />
            <span className="small">give</span>
            <input className="input" style={{ width: 90 }} inputMode="numeric" value={pts} onChange={(e) => setPts(e.target.value)} aria-label="Points" />
            <span className="small">points</span>
            <button className="btn primary" disabled={!ok || save.isPending}>Save rule</button>
          </form>
          <p className="xs muted" style={{ marginTop: 8 }}>Use a negative number to take points away, e.g. -20 for unsubscribed. Saving an event that already has a rule replaces it.</p>
        </div>
      </section>
      <div className="panel section">
        {rules.length === 0 && !q.isLoading ? (
          <Empty title="No scoring rules">Start with the events that show buying intent: pricing_viewed, demo_booked, trial_started.</Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Event</th><th className="num">Points</th><th /></tr></thead>
            <tbody>
              {rules.map((r) => (
                <tr key={r.id}>
                  <td><code>{r.event}</code></td>
                  <td className="num">{r.points > 0 ? `+${r.points}` : r.points}</td>
                  <td className="num"><button className="btn ghost sm danger" onClick={() => remove.mutate(r.id)}>Remove</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// signsInWith says how a teammate signs in: a teammate added without a password can only use SSO.
function signsInWith(u: User): string {
  if (u.password && u.sso) return "Password or SSO";
  return u.password ? "Password" : "SSO";
}
