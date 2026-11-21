// Sending domain health (O6): SPF, DKIM, DMARC, BIMI and MX for an email domain, against the Gmail
// and Yahoo bulk-sender rules, with the DNS record to add for each fix.
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, type Provider } from "../api";
import { Drawer, ErrorNote, Field, useCopy } from "../ui";

export interface DomainCheckResult {
  domain: string; bulk_ready: boolean; summary: string;
  checks: { key: string; title: string; status: "ok" | "warn" | "fail"; found?: string; note: string; fix?: string }[];
}

const ICON = { ok: "✅", warn: "⚠️", fail: "❌" } as const;
const SERVICES: [string, string][] = [["", "Detect / other"], ["ses", "Amazon SES"], ["sendgrid", "SendGrid"], ["mailgun", "Mailgun"],
  ["postmark", "Postmark"], ["mailjet", "Mailjet"], ["sparkpost", "SparkPost"], ["brevo", "Brevo"], ["gmail", "Google Workspace / Gmail"]];

/** domainOf picks a sending domain from the email providers' settings (their From address). */
export function domainOf(providers: Provider[]): string {
  for (const p of providers) {
    const from = p.config?.from ?? p.config?.from_email ?? "";
    const m = /@([^\s>]+)/.exec(from);
    if (m) return m[1].toLowerCase();
  }
  return "";
}

// SERVICE_DOMAINS: the domains each service's SMTP hosts live under (smtp.sendgrid.net,
// email-smtp.<region>.amazonaws.com, smtp-relay.gmail.com, …).
const SERVICE_DOMAINS: [string, string[]][] = [
  ["ses", ["amazonaws.com"]], ["sendgrid", ["sendgrid.net", "sendgrid.com"]], ["mailgun", ["mailgun.org", "mailgun.net"]],
  ["postmark", ["postmarkapp.com"]], ["mailjet", ["mailjet.com"]], ["sparkpost", ["sparkpostmail.com", "sparkpost.com"]],
  ["brevo", ["brevo.com", "sendinblue.com"]], ["gmail", ["gmail.com", "googlemail.com", "google.com"]],
];

/** hostName is the bare host name of a host setting ("smtp://Smtp.Sendgrid.net:587/" → "smtp.sendgrid.net"). */
function hostName(host: string): string {
  return host.trim().toLowerCase().replace(/^[a-z][a-z0-9+.-]*:\/\//, "").replace(/[/?#].*$/, "").replace(/:\d*$/, "").replace(/\.$/, "");
}

/** serviceOf guesses the sending service from a provider's settings. The host must be the
 * service's domain or a name under it: "amazonaws.com.example.net" or "mysendgrid-relay.example"
 * are not that service, and a wrong guess would check (and advise) the wrong SPF include. */
export function serviceOf(p?: Provider): string {
  if (!p) return "";
  const svc = (p.config?.service ?? "").toLowerCase();
  if (svc && svc !== "auto") return svc;
  const host = hostName(p.config?.host ?? "");
  if (!host) return "";
  const under = (d: string) => host === d || host.endsWith("." + d);
  return SERVICE_DOMAINS.find(([, ds]) => ds.some(under))?.[0] ?? "";
}

export function DomainCheckDrawer({ providers, onClose }: { providers: Provider[]; onClose: () => void }) {
  const [domain, setDomain] = useState(() => domainOf(providers));
  const [service, setService] = useState(() => serviceOf(providers.find((p) => p.is_default) ?? providers[0]));
  const [selector, setSelector] = useState("");
  const copy = useCopy();
  const run = useMutation({
    mutationFn: () => api.post<DomainCheckResult>("/v1/channels/domain-check", { domain, service, selectors: selector.trim() ? [selector.trim()] : [] }),
  });
  const r = run.data;
  return (
    <Drawer title="Sending domain health" subtitle="Gmail and Yahoo require SPF, DKIM and DMARC from bulk senders." onClose={onClose} guard={false}>
      <form className="grid-2" onSubmit={(e) => { e.preventDefault(); run.mutate(); }}>
        <Field label="Domain you send from"><input className="input" value={domain} placeholder="example.com" onChange={(e) => setDomain(e.target.value)} /></Field>
        <Field label="Sending service"><select className="select" value={service} onChange={(e) => setService(e.target.value)}>{SERVICES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></Field>
        <Field label="DKIM selector (optional)" hint="If your service gave you one, e.g. s1"><input className="input code" value={selector} onChange={(e) => setSelector(e.target.value)} /></Field>
        <div style={{ alignSelf: "end" }}><button className="btn primary" disabled={!domain.trim() || run.isPending}>{run.isPending ? "Checking DNS…" : "Check"}</button></div>
      </form>
      <ErrorNote error={run.error} />
      {r && (
        <>
          <div className={`alert ${r.bulk_ready ? "ok" : "error"}`} style={{ marginTop: 14 }}><strong>{r.bulk_ready ? "✅ " : "⚠️ "}{r.domain}</strong> — {r.summary}</div>
          <ul className="dc-list">
            {r.checks.map((c) => (
              <li key={c.key} className={`dc-${c.status}`}>
                <div><span aria-hidden="true">{ICON[c.status]}</span> <strong>{c.title}</strong></div>
                <p className="small" style={{ margin: "4px 0" }}>{c.note}</p>
                {c.found && <code className="dc-found">{c.found}</code>}
                {c.fix && (
                  <div className="dc-fix small">
                    <span>{c.fix}</span>
                    {/(TXT record|Use:|e\.g\.)/.test(c.fix) && <button type="button" className="btn ghost sm" onClick={() => copy(c.fix!.replace(/^.*?: /, "").replace(/ \(start with.*$/, ""), "Copied the record")}>Copy</button>}
                  </div>
                )}
              </li>
            ))}
          </ul>
          <p className="xs muted">DNS changes can take up to a few hours to show. Check again after updating your records.</p>
        </>
      )}
    </Drawer>
  );
}
