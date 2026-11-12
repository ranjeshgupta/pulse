import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { api, ApiError } from "../api";
import { useCan } from "../auth";
import { ErrorNote, Field, PageHead, timeAgo, useCopy, useToast } from "../ui";
import {
  browserFacts, checkItemView, checkSummary, DEFAULT_SERVICE_WORKER, frequencySummary, MAX_RULES, needsSnippet, normalise,
  pagesSummary, positionsFor, previewDoc, sameSettings, secondsLeft, siteWarning, STYLES, swSnippet, triggerSummary, validate,
  withStyle, type BrowserReport, type CheckReport, type CheckToken, type PromptConfig, type WebPushResponse, type WebPushSettings,
} from "../webPushSettings";
import "./webpush.css";

// Settings → Web push: the no-code opt-in prompt the website SDK shows before the browser's own
// permission prompt, and an install check of the customer's site (from the server, and from a
// real browser through a one-off check link).

export function WebPushSettingsPage() {
  const canEdit = useCan("channels", "edit");
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["web-push"], queryFn: () => api.get<WebPushResponse>("/v1/settings/web-push") });
  const [draft, setDraft] = useState<WebPushSettings | null>(null);
  const save = useMutation({
    mutationFn: (s: WebPushSettings) => api.put<{ settings: WebPushSettings }>("/v1/settings/web-push", s),
    onSuccess: (r) => {
      setDraft(null);
      qc.setQueryData<WebPushResponse>(["web-push"], (old) => (old ? { ...old, settings: r.settings } : old));
      qc.invalidateQueries({ queryKey: ["web-push"] });
      toast({ text: "Saved. Your site picks up the change on the next page a visitor opens." });
    },
  });
  const data = q.data;
  if (!data) return <div className="page"><ErrorNote error={q.error} /></div>;
  const cur = draft ?? data.settings;
  const p = cur.prompt;
  const problems = validate(cur);
  const dirty = !sameSettings(cur, data.settings);
  const set = (s: WebPushSettings) => setDraft(s);
  const setP = (patch: Partial<PromptConfig>) => set({ ...cur, prompt: { ...cur.prompt, ...patch } });
  const num = (v: string) => Math.trunc(Number(v) || 0);
  const positions = positionsFor(p.style);

  return (
    <div className="page webpush">
      <PageHead
        title="Web push"
        intro={<>
          Ask website visitors to get notifications from your site, then send them web push from campaigns and journeys.
          It works with the website snippet you already have: no extra code beyond one small file on your site.
          Web push needs a secure (https) site; on iPhone and iPad, visitors must first add your site to their home screen.
        </>}
      />
      <ErrorNote error={q.error} />
      {!canEdit && <div className="alert info">You can look at these settings; changing them or running the check needs edit access to channels.</div>}

      <fieldset disabled={!canEdit} className="bare">
        <section className="panel section">
          <div className="panel-head">
            <h3>Opt-in prompt</h3>
            <label className="check switch">
              <input type="checkbox" checked={p.enabled} onChange={(e) => setP({ enabled: e.target.checked })} />
              <span>{p.enabled ? "On" : "Off"}</span>
            </label>
          </div>
          <div className="panel-body">
            <p className="small muted wp-lead">
              Browsers allow one question about notifications, and a visitor who says no there can't be asked again.
              So Pulse first shows your own friendly message; the browser's question opens only when the visitor clicks your Allow button.
              {!p.enabled && <> The prompt is off: visitors are only asked if your site calls <code>Pulse.enablePush()</code> itself.</>}
            </p>
            <div className="wp-grid">
              <div className="wp-form">
                <h4>Look</h4>
                <Field label="Style" group>
                  <div className="wp-styles" role="radiogroup" aria-label="Style">
                    {STYLES.map((s) => (
                      <label key={s.value} className={`wp-style${p.style === s.value ? " on" : ""}`}>
                        <input type="radio" name="wp-style" checked={p.style === s.value} onChange={() => setP(withStyle(p, s.value))} />
                        <strong>{s.label}</strong><span className="xs muted">{s.hint}</span>
                      </label>
                    ))}
                  </div>
                </Field>
                {positions.length > 0 && (
                  <Field label="Where">
                    <select className="select" value={p.position || positions[0].value} onChange={(e) => setP({ position: e.target.value })}>
                      {positions.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                    </select>
                  </Field>
                )}
                <Field label="Title" hint="Optional. Up to 60 characters.">
                  <input className="input" maxLength={60} value={p.title} onChange={(e) => setP({ title: e.target.value })} />
                </Field>
                <Field label="Message" hint={`${p.text.length}/300. Say what visitors will get and how often.`}>
                  <textarea className="textarea" rows={3} maxLength={300} value={p.text} onChange={(e) => setP({ text: e.target.value })} />
                </Field>
                <div className="wp-two">
                  <Field label="Allow button">
                    <input className="input" maxLength={60} value={p.allow} onChange={(e) => setP({ allow: e.target.value })} />
                  </Field>
                  <Field label="Not now button">
                    <input className="input" maxLength={60} placeholder="Not now" value={p.later} onChange={(e) => setP({ later: e.target.value })} />
                  </Field>
                </div>
                <div className="wp-colors">
                  <ColorField label="Background" value={p.background} fallback="#ffffff" onChange={(v) => setP({ background: v })} />
                  <ColorField label="Text" value={p.text_color} fallback="#152238" onChange={(v) => setP({ text_color: v })} />
                  <ColorField label="Button" value={p.button} fallback="#2747d6" onChange={(v) => setP({ button: v })} />
                </div>

                <h4>When it appears</h4>
                <div className="wp-three">
                  <Field label="After" hint="seconds on the page">
                    <input className="input" type="number" min={0} max={600} value={p.delay_seconds} onChange={(e) => setP({ delay_seconds: num(e.target.value) })} />
                  </Field>
                  <Field label="Scrolled at least" hint="% of the page (0: no need)">
                    <input className="input" type="number" min={0} max={100} value={p.scroll_percent} onChange={(e) => setP({ scroll_percent: num(e.target.value) })} />
                  </Field>
                  <Field label="Pages seen" hint="in this visit (0: any)">
                    <input className="input" type="number" min={0} max={50} value={p.page_views} onChange={(e) => setP({ page_views: num(e.target.value) })} />
                  </Field>
                </div>
                <p className="small wp-summary">{triggerSummary(p)}.</p>

                <Field label="If the visitor clicks “Not now”" group>
                  <div className="row">
                    <label className="check inline-check">
                      <input type="radio" name="wp-repeat" checked={p.repeat_days > 0} onChange={() => setP({ repeat_days: p.repeat_days || 7 })} />
                      <span>Ask again after</span>
                    </label>
                    <input className="input inline num-input" type="number" min={1} max={365} aria-label="Days before asking again" disabled={!p.repeat_days}
                      value={p.repeat_days || ""} onChange={(e) => setP({ repeat_days: Math.max(1, num(e.target.value)) })} />
                    <span className="small">days</span>
                    <label className="check inline-check">
                      <input type="radio" name="wp-repeat" checked={p.repeat_days === 0} onChange={() => setP({ repeat_days: 0 })} />
                      <span>Never ask again</span>
                    </label>
                  </div>
                </Field>
                <p className="xs muted">{frequencySummary(p.repeat_days)} Visitors who already allowed or blocked notifications never see it.</p>

                <h4>Pages</h4>
                <div className="wp-two">
                  <ListEditor label="Only on pages whose address contains" hint="Empty: every page. For example /blog or /products."
                    values={p.show_on ?? []} onChange={(v) => setP({ show_on: v })} />
                  <ListEditor label="Never on pages whose address contains" hint="For example /checkout or /login."
                    values={p.hide_on ?? []} onChange={(v) => setP({ hide_on: v })} />
                </div>
                <p className="xs muted">{pagesSummary(p)}</p>
              </div>

              <div className="wp-preview">
                <div className="xs muted wp-preview-label">Preview{p.style === "bell" ? " (the message opens when the bell is clicked)" : ""}</div>
                <iframe className="wp-frame" title="Prompt preview" sandbox="" srcDoc={previewDoc(p)} />
                {!p.enabled && <p className="xs muted">The prompt is off, so visitors won't see this.</p>}
              </div>
            </div>
          </div>
        </section>

        <InstallCheck cur={cur} saved={data.settings} pulseUrl={data.pulse_url} canEdit={canEdit} dirty={dirty}
          onSite={(site_url) => set({ ...cur, site_url })} onWorker={(service_worker) => set({ ...cur, service_worker })} />

        {canEdit && (
          <div className="wp-save">
            {problems.length > 0 && dirty && (
              <ul className="alert error wp-problems" role="alert">{problems.map((m) => <li key={m}>{m}</li>)}</ul>
            )}
            <ErrorNote error={save.error} />
            <div className="row">
              <button className="btn primary" type="button" disabled={!dirty || problems.length > 0 || save.isPending} onClick={() => save.mutate(normalise(cur))}>
                {save.isPending ? "Saving…" : "Save"}
              </button>
              {dirty && <button className="btn ghost" type="button" onClick={() => setDraft(null)}>Discard changes</button>}
              {!dirty && <span className="xs muted">No unsaved changes.</span>}
            </div>
          </div>
        )}
      </fieldset>
    </div>
  );
}

function ColorField({ label, value, fallback, onChange }: { label: string; value?: string; fallback: string; onChange: (v: string) => void }) {
  const v = value || fallback;
  const ok = /^#[0-9a-fA-F]{6}$/.test(v);
  return (
    <Field label={label} group>
      <div className="row wp-color">
        <input type="color" aria-label={`${label} colour`} value={ok ? v.toLowerCase() : fallback} onChange={(e) => onChange(e.target.value)} />
        <input className="input inline" aria-label={`${label} colour code`} maxLength={7} value={v} onChange={(e) => onChange(e.target.value.trim())} />
      </div>
    </Field>
  );
}

function ListEditor({ label, hint, values, onChange }: { label: string; hint: string; values: string[]; onChange: (v: string[]) => void }) {
  const [text, setText] = useState("");
  const add = () => {
    const v = text.trim();
    if (!v || values.includes(v) || values.length >= MAX_RULES) return;
    onChange([...values, v]);
    setText("");
  };
  return (
    <Field label={label} hint={hint} group>
      {values.length > 0 && (
        <ul className="wp-list">
          {values.map((v, i) => (
            <li key={`${v}-${i}`}>
              <code>{v}</code>
              <button type="button" className="btn ghost sm" aria-label={`Remove ${v}`} onClick={() => onChange(values.filter((_, j) => j !== i))}>Remove</button>
            </li>
          ))}
        </ul>
      )}
      {values.length < MAX_RULES ? (
        <div className="row">
          <input className="input inline wp-list-input" placeholder="/blog" aria-label={label} value={text} maxLength={300}
            onChange={(e) => setText(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }} />
          <button type="button" className="btn sm" disabled={!text.trim()} onClick={add}>Add</button>
        </div>
      ) : <span className="xs muted">That's the most ({MAX_RULES}).</span>}
    </Field>
  );
}

// ---- install check ----

function InstallCheck({ cur, saved, pulseUrl, canEdit, dirty, onSite, onWorker }: {
  cur: WebPushSettings; saved: WebPushSettings; pulseUrl: string; canEdit: boolean; dirty: boolean;
  onSite: (v: string) => void; onWorker: (v: string) => void;
}) {
  const copy = useCopy();
  const sw = cur.service_worker || DEFAULT_SERVICE_WORKER;
  const snippet = swSnippet(pulseUrl);
  const check = useMutation({
    mutationFn: () => api.post<CheckReport>("/v1/settings/web-push/check", {
      site_url: (cur.site_url ?? "").trim() || undefined, service_worker: (cur.service_worker ?? "").trim() || undefined,
    }),
  });
  const report = check.data;
  const summary = report ? checkSummary(report) : null;
  const warning = siteWarning(cur.site_url);
  const checkErr = check.error instanceof ApiError && check.error.status === 429
    ? new Error("That's a lot of checks in a short time. Wait a minute and try again.") : check.error;

  return (
    <section className="panel section">
      <div className="panel-head"><h3>Install check</h3></div>
      <div className="panel-body">
        <p className="small muted wp-lead">
          For web push your site needs the website snippet on its pages (Settings → <Link to="/settings/access">Sign-in &amp; keys</Link>)
          and one small file, the service worker, at the address below. Pulse can look at your site from outside, or you can open it in your own browser for a full test.
        </p>
        <div className="wp-two">
          <Field label="Your site's address" hint={warning || "The page to check, usually your home page."}>
            <input className="input" type="url" placeholder="https://www.example.com/" value={cur.site_url ?? ""} onChange={(e) => onSite(e.target.value)} />
          </Field>
          <Field label="Service worker file" hint="Where the file lives on your site. Change it only if your web team put it elsewhere.">
            <input className="input" placeholder={DEFAULT_SERVICE_WORKER} value={cur.service_worker ?? ""} onChange={(e) => onWorker(e.target.value)} />
          </Field>
        </div>

        <div className="wp-sw">
          <div className="small"><strong>The service worker file</strong> at <code>{sw}</code> on your site holds just this line:</div>
          <div className="row">
            <code className="code-block wp-code">{snippet}</code>
            <button type="button" className="btn sm" onClick={() => copy(snippet, "Copied the line.")}>Copy</button>
          </div>
          <div className="xs muted">If your site already has a service worker, add the line at its top instead, and enter that file's path above.</div>
        </div>

        <div className="wp-checks">
          <div className="wp-check">
            <h4>Check my site</h4>
            <p className="xs muted">Pulse opens your site from its server and looks for the snippet, the site key and the service worker. Only public https sites can be checked.</p>
            <button type="button" className="btn" disabled={!canEdit || !(cur.site_url ?? "").trim() || check.isPending} onClick={() => check.mutate()}>
              {check.isPending ? "Checking…" : "Check my site"}
            </button>
            <ErrorNote error={checkErr} />
            {report && summary && (
              <div className="wp-report">
                <div className={`wp-headline ${summary.tone}`}>{summary.text}</div>
                <div className="xs muted">{report.site_url} · checked {timeAgo(report.checked_at)}</div>
                <ul className="wp-items">
                  {report.items.map((it) => {
                    const v = checkItemView(it);
                    return (
                      <li key={it.key} className={`wp-item ${v.tone}`}>
                        <span className={`wp-icon ${v.tone}`} aria-hidden="true">{v.icon}</span>
                        <div>
                          <div className="small"><strong>{v.label}</strong> <span className={`badge ${v.tone}`}>{v.word}</span></div>
                          {v.detail && <div className="xs">{v.detail}</div>}
                          {v.next && <div className="xs wp-next">What to do: {v.next}</div>}
                        </div>
                      </li>
                    );
                  })}
                </ul>
                {needsSnippet(report) && <div className="xs">The line for <code>{sw}</code>: <code>{snippet}</code></div>}
              </div>
            )}
          </div>
          <BrowserCheck canEdit={canEdit} savedSite={saved.site_url ?? ""} siteChanged={dirty && (cur.site_url ?? "").trim() !== (saved.site_url ?? "")} />
        </div>
      </div>
    </section>
  );
}

function useVisible() {
  const [visible, setVisible] = useState(typeof document === "undefined" || document.visibilityState === "visible");
  useEffect(() => {
    const h = () => setVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", h);
    return () => document.removeEventListener("visibilitychange", h);
  }, []);
  return visible;
}

function BrowserCheck({ canEdit, savedSite, siteChanged }: { canEdit: boolean; savedSite: string; siteChanged: boolean }) {
  const [tok, setTok] = useState<CheckToken | null>(null);
  const [now, setNow] = useState(Date.now());
  const visible = useVisible();
  const create = useMutation({
    mutationFn: () => api.post<CheckToken>("/v1/settings/web-push/check-token"),
    onSuccess: (t) => { setTok(t); setNow(Date.now()); },
  });
  const left = tok ? secondsLeft(tok.expires_at, now) : 0;
  const diag = useQuery({
    queryKey: ["web-push-diagnosis", tok?.token],
    queryFn: () => api.get<{ report: BrowserReport | null }>(`/v1/settings/web-push/diagnosis?token=${encodeURIComponent(tok!.token)}`),
    enabled: !!tok && left > 0,
    retry: (n, err) => !(err instanceof ApiError && err.status === 404) && n < 2,
    refetchInterval: (query) => (query.state.data?.report || !visible || left <= 0 ? false : 3000),
    refetchIntervalInBackground: false,
  });
  const report = diag.data?.report ?? null;
  const expired = (tok && left <= 0 && !report) || (diag.error instanceof ApiError && diag.error.status === 404);

  // A clock for the countdown while waiting.
  useEffect(() => {
    if (!tok || report) return;
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, [tok, report]);

  return (
    <div className="wp-check">
      <h4>Check in my browser</h4>
      <p className="xs muted">
        Opens your site with a one-off check link. The website snippet tests this browser (https, notifications, the service worker) and reports back here.
        Use the browser and device your visitors use, for example a phone.
      </p>
      {!savedSite && <p className="xs wp-next">Save your site's address first; the link opens it.</p>}
      {savedSite && siteChanged && <p className="xs wp-next">The link opens the saved address ({savedSite}); save to use the new one.</p>}
      <button type="button" className="btn" disabled={!canEdit || !savedSite || create.isPending} onClick={() => create.mutate()}>
        {create.isPending ? "Creating link…" : tok ? "New check link" : "Check in my browser"}
      </button>
      <ErrorNote error={create.error ?? (diag.error instanceof ApiError && diag.error.status === 404 ? null : diag.error)} />
      {tok && (
        <div className="wp-report">
          {tok.url ? (
            <>
              <div className="row wp-link">
                <code className="code-block wp-code">{tok.url}</code>
                <a className="btn primary sm" href={tok.url} target="_blank" rel="noopener noreferrer">Open my site</a>
              </div>
              <div className="xs muted">On another device, copy this link there. It works once your site has the website snippet.</div>
            </>
          ) : <p className="xs wp-next">No site address is saved, so there's no link to open.</p>}
          {!report && !expired && (
            <div className="small wp-waiting" aria-live="polite">
              {visible ? "Waiting for your browser to report…" : "Paused while this tab is in the background."} {Math.floor(left / 60)}:{String(left % 60).padStart(2, "0")} left.
            </div>
          )}
          {expired && <div className="small wp-next">The link expired before your browser reported. Is the website snippet on that page? Create a new link to try again.</div>}
          {report && <BrowserFindings d={report} />}
        </div>
      )}
    </div>
  );
}

function BrowserFindings({ d }: { d: BrowserReport }) {
  const problems = d.problems ?? [];
  const facts = browserFacts(d);
  return (
    <div>
      <div className={`wp-headline ${problems.length ? "bad" : "ok"}`}>
        {problems.length ? `Your browser found ${problems.length === 1 ? "a problem" : `${problems.length} problems`}.` : "Your browser can receive web push from this site."}
      </div>
      <div className="xs muted">{d.url} · reported {timeAgo(d.reported_at)}{d.sdk_version ? ` · snippet version ${d.sdk_version}` : ""}</div>
      {problems.length > 0 && <ul className="wp-problem-list small">{problems.map((m) => <li key={m}>{m}</li>)}</ul>}
      <dl className="wp-facts">
        {facts.map((f) => (
          <Fact key={f.label} label={f.label}><span className={`badge ${f.tone}`}>{f.value}</span></Fact>
        ))}
      </dl>
    </div>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return <div className="wp-fact"><dt className="xs">{label}</dt><dd>{children}</dd></div>;
}
