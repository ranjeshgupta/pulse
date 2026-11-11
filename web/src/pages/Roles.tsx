import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, LEVELS, type Area, type BuiltinRole, type CustomRole, type Level, type Perms, type RoleKey } from "../api";
import { useIsAdmin } from "../auth";
import { Drawer, Empty, ErrorNote, Field, PageHead, useToast } from "../ui";
import "./roles.css";

export interface RolesResponse { builtin: BuiltinRole[]; custom: CustomRole[]; areas: Area[] }

/** useRoles loads the built-in and custom roles. Anyone signed in may read them. */
export function useRoles() {
  return useQuery({ queryKey: ["roles"], queryFn: () => api.get<RolesResponse>("/v1/roles"), staleTime: 30_000 });
}

const BUILTIN_NAMES: Record<string, string> = { admin: "Admin", editor: "Editor", viewer: "Viewer" };

/** roleName turns a role key into what people see: "Admin", or the custom role's name. */
export function roleName(key: RoleKey | string | undefined, roles?: RolesResponse): string {
  if (!key) return "";
  if (BUILTIN_NAMES[key]) return roles?.builtin.find((r) => r.key === key)?.name ?? BUILTIN_NAMES[key];
  const id = Number(key.startsWith("role:") ? key.slice(5) : NaN);
  return roles?.custom.find((r) => r.id === id)?.name ?? "Custom role";
}

/** RoleOptions renders <option>s for every role: built-in first, then custom ones. */
export function RoleOptions({ roles }: { roles?: RolesResponse }) {
  const builtin = roles?.builtin ?? (["admin", "editor", "viewer"] as const).map((key) => ({ key, name: BUILTIN_NAMES[key] }));
  const custom = roles?.custom ?? [];
  return (
    <>
      <optgroup label="Built-in roles">
        {builtin.map((r) => <option key={r.key} value={r.key}>{r.name}</option>)}
      </optgroup>
      {custom.length > 0 && (
        <optgroup label="Custom roles">
          {custom.map((r) => <option key={r.id} value={`role:${r.id}`}>{r.name}</option>)}
        </optgroup>
      )}
    </>
  );
}

const AREA_ORDER: Area[] = ["contacts", "segments", "journeys", "broadcasts", "content", "reports", "channels", "settings"];
export const AREA_INFO: Record<Area, { name: string; about: string }> = {
  contacts: { name: "Contacts", about: "Profiles, events, tags, imports and exports." },
  segments: { name: "Segments", about: "Segments (dynamic and static), contact fields, scoring and stages." },
  journeys: { name: "Journeys", about: "Build journeys. Publish lets you activate and pause them." },
  broadcasts: { name: "Broadcasts", about: "Build broadcasts. Publish lets you schedule, cancel and pick A/B winners." },
  content: { name: "Content", about: "Templates, focus items, web content, forms and test sends." },
  reports: { name: "Reports", about: "Overview, reports and funnels." },
  channels: { name: "Channels", about: "Providers and the test inbox." },
  settings: { name: "Settings", about: "Team, roles, sign-in, keys, delivery rules, webhooks and the audit log." },
};
/** Areas where publish means more than edit. */
const PUBLISH_AREAS: Area[] = ["journeys", "broadcasts"];
const LEVEL_TEXT: Record<Level, string> = { none: "No access", view: "View", edit: "Edit", publish: "Publish" };
const LEVEL_TONE: Record<Level, string> = { none: "none", view: "", edit: "info", publish: "ok" };

/** shown is the level as it matters in area: publish counts as edit outside journeys and broadcasts. */
const shown = (area: Area, l: Level | undefined): Level => (l === "publish" && !PUBLISH_AREAS.includes(area) ? "edit" : l ?? "none");

function LevelBadge({ area, level }: { area: Area; level?: Level }) {
  const l = shown(area, level);
  return <span className={`badge lvl ${LEVEL_TONE[l]}`}>{LEVEL_TEXT[l]}</span>;
}

const areasOf = (r?: RolesResponse) => (r?.areas?.length ? r.areas : AREA_ORDER);

function summary(perms: Perms, areas: Area[]): string {
  const by = (l: Level) => areas.filter((a) => shown(a, perms[a]) === l).map((a) => AREA_INFO[a]?.name ?? a);
  const parts: string[] = [];
  const pub = by("publish"), edit = by("edit"), view = by("view");
  if (pub.length) parts.push(`Publishes ${pub.join(", ")}`);
  if (edit.length) parts.push(`Edits ${edit.join(", ")}`);
  if (view.length) parts.push(`Views ${view.join(", ")}`);
  return parts.join(" · ") || "No access to anything";
}

export function Roles() {
  const admin = useIsAdmin();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useRoles();
  const [editing, setEditing] = useState<CustomRole | null>(null);
  const remove = useMutation({
    mutationFn: (r: CustomRole) => api.del(`/v1/roles/${r.id}`),
    onSuccess: (_, r) => { qc.invalidateQueries({ queryKey: ["roles"] }); toast({ text: `${r.name} deleted` }); },
  });
  const areas = areasOf(q.data);
  const builtin = q.data?.builtin ?? [];
  const custom = q.data?.custom ?? [];
  const blank: CustomRole = { id: 0, name: "", description: "", permissions: {}, users: 0, updated_at: "" };

  return (
    <div className="page">
      <PageHead
        title="Roles"
        intro="A role decides what someone can see and change in each part of Pulse. Use the built-in roles, or make your own."
        actions={admin && <button className="btn primary" onClick={() => setEditing(blank)}>New role</button>}
      />
      <ErrorNote error={q.error} />

      <section className="panel">
        <div className="panel-head"><h3>Built-in roles</h3><span className="xs muted">These can't be changed.</span></div>
        <div className="table-scroll">
          <table className="table role-compare">
            <thead>
              <tr>
                <th>Area</th>
                {builtin.map((r) => <th key={r.key}>{r.name}<span className="xs">{r.description}</span></th>)}
              </tr>
            </thead>
            <tbody>
              {areas.map((a) => (
                <tr key={a}>
                  <td>{AREA_INFO[a]?.name ?? a}<div className="xs muted">{AREA_INFO[a]?.about}</div></td>
                  {builtin.map((r) => <td key={r.key}><LevelBadge area={a} level={r.permissions[a]} /></td>)}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
      <p className="xs muted" style={{ marginTop: 8 }}>Each level includes the ones before it. Publish only adds something for journeys and broadcasts; elsewhere it's the same as edit.</p>

      <div className="section"><ErrorNote error={remove.error} /></div>
      <section className="panel">
        <div className="panel-head"><h3>Custom roles</h3></div>
        {custom.length === 0 && !q.isLoading ? (
          <Empty title="No custom roles yet" action={admin && <button className="btn primary" onClick={() => setEditing(blank)}>Create a role</button>}>
            For example, "Content writer": edits templates and content, views reports, nothing else.
          </Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Role</th><th>Access</th><th className="num">People</th><th /></tr></thead>
            <tbody>
              {custom.map((r) => (
                <tr key={r.id}>
                  <td><strong>{r.name}</strong>{r.description && <div className="xs muted">{r.description}</div>}</td>
                  <td className="small muted">{summary(r.permissions, areas)}</td>
                  <td className="num">{r.users}</td>
                  <td className="num" style={{ whiteSpace: "nowrap" }}>
                    <button className="btn ghost sm" onClick={() => setEditing(r)}>{admin ? "Edit" : "View"}</button>
                    {admin && (
                      <button
                        className="btn ghost sm danger"
                        disabled={remove.isPending}
                        onClick={() => confirm(`Delete the role ${r.name}? This can't be undone.`) && remove.mutate(r)}
                      >Delete</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {editing && <RoleEditor role={editing} roles={q.data} readOnly={!admin} onClose={() => setEditing(null)} />}
    </div>
  );
}

function RoleEditor({ role, roles, readOnly, onClose }: { role: CustomRole; roles?: RolesResponse; readOnly: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const isNew = role.id === 0;
  const areas = areasOf(roles);
  const [name, setName] = useState(role.name);
  const [description, setDescription] = useState(role.description);
  const [perms, setPerms] = useState<Perms>(() => (isNew ? { ...roles?.builtin.find((r) => r.key === "viewer")?.permissions } : { ...role.permissions }));
  const save = useMutation({
    mutationFn: () => {
      const body = { name: name.trim(), description: description.trim(), permissions: Object.fromEntries(areas.map((a) => [a, perms[a] ?? "none"])) };
      return isNew ? api.post<{ id: number; key: string }>("/v1/roles", body) : api.put<{ id: number; key: string }>(`/v1/roles/${role.id}`, body);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["roles"] });
      qc.invalidateQueries({ queryKey: ["me"] });
      toast({ text: isNew ? `${name.trim()} created` : `${name.trim()} saved` });
      onClose();
    },
  });
  const title = readOnly ? role.name : isNew ? "New role" : `Edit ${role.name}`;

  return (
    <Drawer title={title} subtitle={!isNew && role.users > 0 ? `${role.users} ${role.users === 1 ? "person has" : "people have"} this role. Changes apply to them right away.` : undefined} onClose={onClose}>
      <ErrorNote error={save.error} />
      <Field label="Name"><input className="input" value={name} disabled={readOnly} onChange={(e) => setName(e.target.value)} placeholder="Content writer" autoFocus={!readOnly} /></Field>
      <Field label="Description"><input className="input" value={description} disabled={readOnly} onChange={(e) => setDescription(e.target.value)} placeholder="Writes templates and web content" /></Field>
      {!readOnly && (
        <Field label="Start from" hint="Copies a built-in role's access. You can change it below.">
          <select className="select" value="" onChange={(e) => { const b = roles?.builtin.find((r) => r.key === e.target.value); if (b) setPerms({ ...b.permissions }); }}>
            <option value="">Choose a role</option>
            {roles?.builtin.map((r) => <option key={r.key} value={r.key}>{r.name}</option>)}
          </select>
        </Field>
      )}
      <h4 className="small" style={{ margin: "6px 0 8px" }}>Access</h4>
      <div className="area-grid">
        {areas.map((a) => {
          const opts = PUBLISH_AREAS.includes(a) ? LEVELS : LEVELS.filter((l) => l !== "publish");
          const cur = shown(a, perms[a]);
          return (
            <div className="area-row" key={a}>
              <div><strong>{AREA_INFO[a]?.name ?? a}</strong><p>{AREA_INFO[a]?.about}</p></div>
              <div className="lvl-toggle" role="group" aria-label={`${AREA_INFO[a]?.name ?? a} access`}>
                {opts.map((l) => (
                  <button key={l} type="button" aria-pressed={cur === l} disabled={readOnly} onClick={() => setPerms({ ...perms, [a]: l })}>
                    {l === "none" ? "None" : LEVEL_TEXT[l]}
                  </button>
                ))}
              </div>
            </div>
          );
        })}
      </div>
      <p className="xs muted" style={{ margin: "8px 0 18px" }}>Publish only exists for journeys and broadcasts. Elsewhere, edit already includes everything.</p>
      {!readOnly && (
        <div className="row">
          <button className="btn primary" disabled={!name.trim() || save.isPending} onClick={() => save.mutate()}>{isNew ? "Create role" : "Save role"}</button>
          <button className="btn" onClick={onClose}>Cancel</button>
        </div>
      )}
    </Drawer>
  );
}
