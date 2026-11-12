import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Fragment, useState } from "react";
import { Link } from "react-router-dom";
import { api, type AuditEntry, type User } from "../api";
import { useIsAdmin } from "../auth";
import { Empty, ErrorNote, PageHead, timeAgo } from "../ui";
import "./audit.css";

// Readable labels for audited actions, keyed by "METHOD /v1/path pattern".
const ACTIONS: Record<string, string> = {
  "sign-in": "Signed in",
  "sign-in (SSO)": "Signed in with SSO",
  "POST /v1/segments": "Created a segment",
  "PUT /v1/segments/{id}": "Changed a segment",
  "DELETE /v1/segments/{id}": "Deleted a segment",
  "POST /v1/lists": "Created a static segment",
  "PUT /v1/lists/{id}": "Changed a static segment",
  "POST /v1/lists/{id}/members": "Changed a static segment's members",
  "POST /v1/journeys": "Created a triggered or automation campaign",
  "PUT /v1/journeys/{id}": "Changed a triggered or automation campaign",
  "DELETE /v1/journeys/{id}": "Deleted a triggered or automation campaign",
  "POST /v1/journeys/{id}/{action}": "Put live, paused or archived a campaign",
  "POST /v1/journeys/{id}/archive": "Archived a campaign",
  "POST /v1/journeys/{id}/unarchive": "Restored a campaign from the archive",
  "POST /v1/journeys/{id}/activate": "Put a campaign live",
  "POST /v1/journeys/{id}/pause": "Paused a campaign",
  "POST /v1/broadcasts": "Created a one-time campaign",
  "PUT /v1/broadcasts/{id}": "Changed a one-time campaign",
  "DELETE /v1/broadcasts/{id}": "Deleted a one-time campaign",
  "POST /v1/broadcasts/{id}/schedule": "Scheduled a one-time campaign",
  "POST /v1/broadcasts/{id}/cancel": "Cancelled a one-time campaign",
  "POST /v1/broadcasts/{id}/runs/{run}/winner": "Picked an A/B winner",
  "POST /v1/templates": "Created a template",
  "PUT /v1/templates/{id}": "Changed a template",
  "DELETE /v1/templates/{id}": "Deleted a template",
  "POST /v1/focus-items": "Created a pop-up, bar or slide-in",
  "PUT /v1/focus-items/{id}": "Changed a pop-up, bar or slide-in",
  "DELETE /v1/focus-items/{id}": "Deleted a pop-up, bar or slide-in",
  "POST /v1/dwc": "Created web content",
  "PUT /v1/dwc/{id}": "Changed web content",
  "DELETE /v1/dwc/{id}": "Deleted web content",
  "POST /v1/forms": "Created a form",
  "PUT /v1/forms/{id}": "Changed a form",
  "DELETE /v1/forms/{id}": "Deleted a form",
  "POST /v1/funnels": "Created a funnel",
  "PUT /v1/funnels/{id}": "Changed a funnel",
  "DELETE /v1/funnels/{id}": "Deleted a funnel",
  "POST /v1/contacts/import": "Imported contacts",
  "POST /v1/imports": "Started an import",
  "POST /v1/imports/{id}/cancel": "Cancelled an import",
  "POST /v1/contacts/{external_id}/tags": "Changed a contact's tags",
  "DELETE /v1/contacts/{external_id}": "Deleted a contact",
  "GET /v1/contacts/{external_id}/data": "Downloaded a contact's data",
  "POST /v1/contacts/{external_id}/points": "Changed a contact's points",
  "PUT /v1/attributes/{key}": "Defined an attribute",
  "DELETE /v1/attributes/{key}": "Removed an attribute",
  "PUT /v1/scoring/rules": "Saved a scoring rule",
  "DELETE /v1/scoring/rules/{id}": "Removed a scoring rule",
  "POST /v1/users": "Added a teammate",
  "DELETE /v1/users/{id}": "Removed a teammate",
  "PUT /v1/users/{id}/role": "Changed a user's role",
  "POST /v1/roles": "Created a role",
  "PUT /v1/roles/{id}": "Changed a role",
  "DELETE /v1/roles/{id}": "Deleted a role",
  "POST /v1/channels/providers": "Added a provider",
  "PUT /v1/channels/providers/{id}": "Changed a provider",
  "DELETE /v1/channels/providers/{id}": "Removed a provider",
  "POST /v1/channels/providers/{id}/test": "Tested a provider",
  "POST /v1/channels/test": "Sent a test message",
  "PUT /v1/settings/sso": "Changed single sign-on",
  "DELETE /v1/settings/sso": "Removed single sign-on",
  "POST /v1/settings/api-key": "Replaced the API key",
  "PUT /v1/settings/identity": "Changed identity verification",
  "POST /v1/settings/identity/rotate": "Replaced the identity secret",
  "PUT /v1/settings/delivery": "Changed delivery rules",
  "POST /v1/settings/webhooks": "Created a webhook",
  "PUT /v1/settings/webhooks/{id}": "Changed a webhook",
  "DELETE /v1/settings/webhooks/{id}": "Deleted a webhook",
  "POST /v1/settings/webhooks/{id}/test": "Tested a webhook",
};

/** actionLabel turns "POST /v1/segments" into "Created a segment"; unknown actions stay as they are. */
// M7: requests without their own words still read as a sentence ("Changed: automation settings");
// the exact request shows when a row is opened.
const VERBS: Record<string, string> = { POST: "Created or ran", PUT: "Changed", PATCH: "Changed", DELETE: "Deleted", GET: "Downloaded" };
const NOUNS: Record<string, string> = {
  dwc: "web content", "focus-items": "pop-ups, bars and slide-ins", journeys: "campaign", broadcasts: "one-time campaign", segments: "segment",
  templates: "template", contacts: "contacts", settings: "settings", channels: "channels", integrations: "integration", "landing-pages": "landing page",
  assets: "asset", forms: "form", inapp: "in-app message", "test-contacts": "test contacts", cohorts: "cohort report", reports: "report",
  revenue: "revenue settings", stages: "stages", companies: "company", lists: "static segment", "journey-steps": "campaign steps",
};
export function actionLabel(action: string): string {
  if (ACTIONS[action]) return ACTIONS[action];
  const [method, path] = action.split(" ");
  const parts = (path ?? "").replace(/^\/v1\//, "").split("/").filter((x) => x && !x.startsWith("{"));
  if (!VERBS[method] || parts.length === 0) return action;
  const noun = NOUNS[parts[0]] ?? parts[0].replace(/-/g, " ");
  const rest = parts.slice(1).map((x) => x.replace(/-/g, " ")).join(" ");
  return `${VERBS[method]}: ${noun}${rest ? ` (${rest})` : ""}`;
}

// Where an object type's items open in the app.
const OBJECT_PATH: Record<string, string> = {
  segments: "/segments", lists: "/segments", journeys: "/journeys", broadcasts: "/broadcasts", templates: "/templates", // campaign pages
  "focus-items": "/on-site/focus", dwc: "/on-site/content", forms: "/forms", funnels: "/funnels",
};

const OBJECT_TYPES: { value: string; label: string }[] = [
  { value: "auth", label: "Sign-ins" },
  { value: "segments", label: "Segments" },
  { value: "lists", label: "Static segments" },
  { value: "journeys", label: "Triggered & automation campaigns" },
  { value: "broadcasts", label: "One-time campaigns" },
  { value: "templates", label: "Templates" },
  { value: "focus-items", label: "Focus items" },
  { value: "dwc", label: "Web content" },
  { value: "forms", label: "Forms" },
  { value: "funnels", label: "Funnels" },
  { value: "contacts", label: "Contacts" },
  { value: "imports", label: "Imports" },
  { value: "attributes", label: "Attributes" },
  { value: "scoring", label: "Scoring" },
  { value: "users", label: "Team" },
  { value: "roles", label: "Roles" },
  { value: "channels", label: "Channels" },
  { value: "settings", label: "Settings" },
];

function objectLink(e: AuditEntry): string | null {
  if (!e.object_id) return null;
  if (e.object_type === "settings" && e.action.includes("/webhooks/")) return `/settings/webhooks/${e.object_id}`;
  const base = OBJECT_PATH[e.object_type];
  return base && /^\d+$/.test(e.object_id) ? `${base}/${e.object_id}` : null;
}

function Outcome({ status }: { status: number }) {
  if (status === 401 || status === 403) return <span className="badge bad" title={`HTTP ${status}`}>Refused</span>;
  if (status >= 400) return <span className="badge bad" title={`HTTP ${status}`}>Failed</span>;
  return <span className="badge ok" title={`HTTP ${status}`}>Done</span>;
}

/** auditExportParams builds the export query from the page's filters (dates are whole days, "to" inclusive). */
export function auditExportParams(f: { userId: string; objectType: string; from: string; to: string; format: "csv" | "json" }): string {
  const p = new URLSearchParams({ format: f.format });
  if (f.userId) p.set("user_id", f.userId);
  if (f.objectType) p.set("object_type", f.objectType);
  if (f.from) p.set("from", f.from);
  if (f.to) p.set("to", f.to);
  return p.toString();
}

const exact = (iso: string) => new Date(iso).toLocaleString("en-IN", { dateStyle: "medium", timeStyle: "medium" });

export function AuditLog() {
  const admin = useIsAdmin();
  const [userId, setUserId] = useState("");
  const [objectType, setObjectType] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [open, setOpen] = useState<number | null>(null);
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<{ users: User[] }>("/v1/users"), enabled: admin });
  const q = useInfiniteQuery({
    queryKey: ["audit", userId, objectType, from, to],
    enabled: admin,
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const p = new URLSearchParams();
      if (pageParam) p.set("before", String(pageParam));
      if (userId) p.set("user_id", userId);
      if (objectType) p.set("object_type", objectType);
      if (from) p.set("from", from);
      if (to) p.set("to", to);
      return api.get<{ entries: AuditEntry[] }>(`/v1/audit?${p}`);
    },
    getNextPageParam: (last) => (last.entries.length >= 100 ? last.entries[last.entries.length - 1].id : undefined),
  });
  const entries = q.data?.pages.flatMap((p) => p.entries) ?? [];
  const filtered = !!(userId || objectType || from || to);
  const exportURL = (format: "csv" | "json") => `/v1/audit/export?${auditExportParams({ userId, objectType, from, to, format })}`;

  if (!admin) {
    return (
      <div className="page">
        <PageHead title="Audit log" />
        <div className="panel"><Empty title="Only admins can see the audit log">Ask an admin if you need to know who changed something.</Empty></div>
      </div>
    );
  }

  return (
    <div className="page">
      <PageHead title="Audit log" intro="Every change made in the app or through the API, and every sign-in. Click a row to see the request." />
      <div className="row audit-filters">
        <select className="select inline" aria-label="Person" value={userId} onChange={(e) => { setUserId(e.target.value); setOpen(null); }}>
          <option value="">Everyone</option>
          {users.data?.users.map((u) => <option key={u.id} value={u.id}>{u.name || u.email}</option>)}
        </select>
        <select className="select inline" aria-label="Object type" value={objectType} onChange={(e) => { setObjectType(e.target.value); setOpen(null); }}>
          <option value="">Everything</option>
          {OBJECT_TYPES.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
        <label className="audit-date">From <input className="input inline" type="date" value={from} max={to || undefined} onChange={(e) => { setFrom(e.target.value); setOpen(null); }} /></label>
        <label className="audit-date">To <input className="input inline" type="date" value={to} min={from || undefined} onChange={(e) => { setTo(e.target.value); setOpen(null); }} /></label>
        {filtered && <button className="btn ghost sm" onClick={() => { setUserId(""); setObjectType(""); setFrom(""); setTo(""); }}>Clear filters</button>}
        <span className="audit-export">
          <a className="btn sm" href={exportURL("csv")} download title="Download the entries matching these filters as CSV">Export CSV</a>
          <a className="btn sm" href={exportURL("json")} download title="Download the entries matching these filters as JSON">Export JSON</a>
        </span>
      </div>
      <ErrorNote error={q.error} />
      <div className="panel">
        {entries.length === 0 && !q.isLoading ? (
          <Empty title={filtered ? "Nothing matches" : "Nothing recorded yet"}>{filtered ? "Try another person or object type." : "Changes and sign-ins show up here as they happen."}</Empty>
        ) : (
          <table className="table audit-table">
            <thead><tr><th>When</th><th>Who</th><th>What</th><th>Object</th><th>Outcome</th></tr></thead>
            <tbody>
              {entries.map((e) => {
                const link = objectLink(e);
                const isOpen = open === e.id;
                return (
                  <Fragment key={e.id}>
                    <tr
                      className={`clickable${isOpen ? " open" : ""}`}
                      tabIndex={0}
                      aria-expanded={isOpen}
                      onClick={() => setOpen(isOpen ? null : e.id)}
                      onKeyDown={(k) => { if (k.key === "Enter" || k.key === " ") { k.preventDefault(); setOpen(isOpen ? null : e.id); } }}
                    >
                      <td className="muted" title={exact(e.at)} style={{ whiteSpace: "nowrap" }}>{timeAgo(e.at)}</td>
                      <td>{e.actor}</td>
                      <td>{actionLabel(e.action)}</td>
                      <td>
                        {link ? <Link to={link} onClick={(ev) => ev.stopPropagation()}>{e.object_type} #{e.object_id}</Link>
                          : e.object_id ? <span className="muted">{e.object_type} {e.object_id}</span>
                          : <span className="faint">{e.object_type}</span>}
                      </td>
                      <td><Outcome status={e.status} /></td>
                    </tr>
                    {isOpen && (
                      <tr className="audit-detail">
                        <td colSpan={5}>
                          <dl className="kv">
                            <dt>Time</dt><dd>{exact(e.at)}</dd>
                            <dt>Request</dt><dd><code>{e.action}</code></dd>
                            <dt>Response</dt><dd>HTTP {e.status}</dd>
                            <dt>IP address</dt><dd>{e.ip || <span className="faint">unknown</span>}</dd>
                          </dl>
                          {e.detail != null ? <pre className="audit-json">{JSON.stringify(e.detail, null, 2)}</pre> : <p className="xs muted" style={{ marginTop: 10 }}>No request body.</p>}
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
      {q.hasNextPage && (
        <div style={{ marginTop: 14 }}>
          <button className="btn" disabled={q.isFetchingNextPage} onClick={() => q.fetchNextPage()}>{q.isFetchingNextPage ? "Loading…" : "Load older"}</button>
        </div>
      )}
    </div>
  );
}
