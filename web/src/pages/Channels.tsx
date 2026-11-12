import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, CHANNEL_NAMES, CHANNELS, PROVIDER_CHANNELS, type Channel, type DeliveryRules, type Provider, type ProviderLimits, type TestContact, type ProviderKind, type ProviderField, type SandboxMessage } from "../api";
import { useCan, useIsAdmin } from "../auth";
import { useTestContacts } from "../campaignUI";
import { ChannelLabel, Empty, ErrorNote, Field, Modal, PageHead, timeAgo, useToast } from "../ui";
import { BounceMailboxPanel } from "./BounceMailbox";
import { AuthEditor, ConfigField, CustomRequestEditor, ProviderTools } from "./ProviderSettings";
import { hostsChanged } from "../providerAuth";

export function Channels() {
  const admin = useIsAdmin();
  const canLimit = useCan("channels", "edit");
  const canTest = useCan("content", "edit");
  const [limiting, setLimiting] = useState<Provider | null>(null);
  const cat = useQuery({ queryKey: ["catalog"], queryFn: () => api.get<{ providers: ProviderKind[] }>("/v1/channels/catalog") });
  const list = useQuery({ queryKey: ["providers"], queryFn: () => api.get<{ providers: Provider[] }>("/v1/channels/providers") });
  const [editing, setEditing] = useState<{ channel: Channel; provider?: Provider } | null>(null);
  const [testing, setTesting] = useState<Provider | null>(null);
  const kinds = cat.data?.providers ?? [];
  const providers = list.data?.providers ?? [];

  return (
    <div className="page">
      <PageHead title="Channels" intro="Where each channel's messages go out. A provider is the service that delivers the messages (an SMTP server, an SMS gateway, WhatsApp…). Each channel sends through its default provider; the sandbox provider sends nothing real and shows messages in the Test inbox." />
      <ErrorNote error={cat.error ?? list.error} />
      {PROVIDER_CHANNELS.map((ch) => {
        const mine = providers.filter((p) => p.channel === ch);
        return (
          <section className="panel" key={ch} style={{ marginBottom: 16 }}>
            <div className="panel-head">
              <ChannelLabel channel={ch} />
              {admin && <button className="btn sm" onClick={() => setEditing({ channel: ch })}>Add provider</button>}
            </div>
            {mine.length === 0 ? (
              <div className="panel-body small muted">
                {ch === "email" ? "Using the server's SMTP relay. Add a provider to send through your own." : `No provider yet, so ${CHANNEL_NAMES[ch]} messages in campaigns fail with "no provider configured".`}
              </div>
            ) : (
              <div className="table-scroll"><table className="table">
                <tbody>{mine.map((p) => (
                  <tr key={p.id}>
                    <td style={{ width: "24%" }}><strong>{p.name}</strong><div className="xs muted">{kinds.find((k) => k.id === p.provider)?.name ?? p.provider}</div></td>
                    <td>{p.is_default ? <span className="badge ok">Default</span> : <span className="badge">Standby</span>}</td>
                    <td className="small" style={{ minWidth: 210 }}><LimitsSummary p={p} /></td>
                    <td className="xs muted" style={{ overflowWrap: "anywhere", minWidth: 180 }}>{p.webhook_url && <>Delivery reports (give this address to the provider): <code>{p.webhook_url}</code></>}</td>
                    <td className="num"><div className="row" style={{ justifyContent: "flex-end", flexWrap: "nowrap" }}>
                      {canLimit && <button className="btn sm" onClick={() => setLimiting(p)}>Limits</button>}
                      {admin && <button className="btn sm" onClick={() => setTesting(p)}>Send test</button>}
                      {admin && <button className="btn sm" onClick={() => setEditing({ channel: ch, provider: p })}>Edit</button>}
                    </div></td>
                  </tr>
                ))}</tbody>
              </table></div>
            )}
          </section>
        );
      })}
      <section className="panel" style={{ marginBottom: 16 }}>
        <div className="panel-head"><h3><span className="chan chan-inbox">App inbox</span></h3><span className="xs muted">Built in</span></div>
        <div className="panel-body small muted">
          Pulse keeps inbox messages itself, so there's nothing to connect. Apps show them with the Flutter SDK, and websites with
          {" "}<code>Pulse.inbox</code> in the web SDK. Messages expire after 30 days unless the template says otherwise.
        </div>
      </section>
      <TestContactsPanel canEdit={canTest} />
      <DeliveryRulesPanel admin={admin} />
      {admin && <BouncesPanel />}
      {admin && <BounceMailboxPanel />}
      {testing && <ProviderTest provider={testing} onClose={() => setTesting(null)} />}
      {limiting && <LimitsForm provider={limiting} onClose={() => setLimiting(null)} />}
      {editing && <ProviderForm channel={editing.channel} provider={editing.provider} kinds={kinds.filter((k) => k.channel === editing.channel)} onClose={() => setEditing(null)} />}
    </div>
  );
}

/** orderKinds lists provider kinds as the form offers them: services first, the custom gateway, then the sandbox. */
export function orderKinds<T extends { id: string }>(kinds: T[]): T[] {
  const rank = (k: T) => (k.id.startsWith("sandbox") ? 2 : k.id.startsWith("http_") ? 1 : 0);
  return [...kinds].sort((a, b) => rank(a) - rank(b));
}

function ProviderForm({ channel, provider, kinds, onClose }: { channel: Channel; provider?: Provider; kinds: ProviderKind[]; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  // M7: the usual choices first (SMTP and presets, the services), then the custom gateway, then
  // the sandbox; a new provider starts on the first of them, not the most technical one.
  kinds = orderKinds(kinds);
  const [kindId, setKindId] = useState(provider?.provider ?? kinds.find((k) => !k.id.startsWith("sandbox"))?.id ?? kinds[0]?.id);
  const kind = kinds.find((k) => k.id === kindId);
  const [name, setName] = useState(provider?.name ?? "");
  const [config, setConfig] = useState<Record<string, string>>(provider?.config ?? {});
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const [isDefault, setDefault] = useState(provider?.is_default ?? true);
  const setKey = (k: string, v: string) => setConfig((c) => ({ ...c, [k]: v }));
  const custom = config.mode === "custom";
  // The gateway's address and signing secret are needed for its standard request (the custom
  // request has its own address and no signature), so they aren't "optional" there.
  const requiredNow = (f: ProviderField): ProviderField => (kind?.custom_request && !custom && (f.key === "url" || f.key === "signing_secret") ? { ...f, required: true } : f);
  const moved = !!provider && hostsChanged(provider.config, config);

  const save = useMutation({
    mutationFn: () => {
      const body = { channel, provider: kindId, name: name || kind?.name, config, secrets, is_default: isDefault };
      return provider ? api.put(`/v1/channels/providers/${provider.id}`, body) : api.post("/v1/channels/providers", body);
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["providers"] }); toast({ text: "Provider saved. New sends use it within 30 seconds." }); onClose(); },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/channels/providers/${provider!.id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["providers"] }); onClose(); },
  });

  return (
    <Modal title={provider ? `Edit ${provider.name}` : `Add ${CHANNEL_NAMES[channel]} provider`} onClose={onClose}
      footer={<>
        {provider && <button className="btn danger" style={{ marginRight: "auto" }} onClick={() => confirm("Remove this provider?") && remove.mutate()}>Remove</button>}
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn primary" disabled={save.isPending} onClick={() => save.mutate()}>Save provider</button>
      </>}>
      <ErrorNote error={save.error ?? remove.error} />
      {!provider && (
        <Field label="Provider">
          <select className="select" value={kindId} onChange={(e) => { setKindId(e.target.value); setConfig({}); setSecrets({}); }}>
            {kinds.map((k) => <option key={k.id} value={k.id}>{k.name}</option>)}
          </select>
        </Field>
      )}
      {kind && <p className="small muted" style={{ marginBottom: 14 }}>{kind.description}</p>}
      {kind?.notes && <div className="alert info" style={{ marginBottom: 14 }}>{kind.notes}</div>}
      {kind?.default_limits && !provider && (
        <p className="xs muted" style={{ marginBottom: 14 }}>
          Starts with a daily quota of {kind.default_limits.daily_quota?.toLocaleString() ?? "none"}
          {kind.default_limits.rate_per_second ? ` and ${kind.default_limits.rate_per_second} messages a second` : ""}; change it under Limits.
        </p>
      )}
      <Field label="Name in Pulse"><input className="input" value={name} placeholder={kind?.name} onChange={(e) => setName(e.target.value)} /></Field>
      {kind?.config.filter((f) => !(custom && f.key === "url")).map((f) => (
        <ConfigField key={f.key} f={requiredNow(f)} value={config[f.key] ?? ""} onChange={(v) => setConfig({ ...config, [f.key]: v })} />
      ))}
      {kind?.custom_request && custom && <CustomRequestEditor channel={channel} config={config} setConfig={setKey} />}
      {kind?.auth && <AuthEditor config={config} setConfig={setKey} secrets={secrets} setSecret={(k, v) => setSecrets({ ...secrets, [k]: v })} saved={!!provider?.has_secrets && !moved} />}
      {provider && moved && <div className="alert info" style={{ marginBottom: 12 }}>An endpoint's host changed: the saved secrets will be cleared, so enter every secret again.</div>}
      {kind?.secrets.filter((f) => !(custom && f.key === "signing_secret") && !(!custom && f.key === "receipt_token" && kind.custom_request)).map((f) => (
        <Field key={f.key} label={f.label + (requiredNow(f).required ? "" : " (optional)")} hint={<>{f.hint && <>{f.hint} </>}{provider?.has_secrets && !moved ? "Stored encrypted. Leave empty to keep the saved value." : "Stored encrypted; never shown again."}</>}>
          {f.key.includes("json")
            ? <textarea className="textarea code" value={secrets[f.key] ?? ""} placeholder={f.placeholder ?? "Paste the service account JSON"} onChange={(e) => setSecrets({ ...secrets, [f.key]: e.target.value })} />
            : <input className="input" type="password" autoComplete="off" value={secrets[f.key] ?? ""} placeholder={f.placeholder} onChange={(e) => setSecrets({ ...secrets, [f.key]: e.target.value })} />}
        </Field>
      ))}
      {kind && <ProviderTools draft={{ provider_id: provider?.id, provider: kind.id, config, secrets }} canAuth={!!kind.auth || kind.id.endsWith("_smtp")} canPreview={!!kind.auth || kind.id.startsWith("gupshup_")} />}
      <label className="check"><input type="checkbox" checked={isDefault} onChange={(e) => setDefault(e.target.checked)} />Use for all {CHANNEL_NAMES[channel]} messages</label>
    </Modal>
  );
}

const ZONES = ["Asia/Kolkata", "Asia/Dubai", "Asia/Singapore", "Europe/London", "America/New_York", "America/Los_Angeles", "UTC"];

// DeliveryRulesPanel edits workspace frequency caps and quiet hours. Templates marked transactional
// (OTPs, receipts) skip both.
function DeliveryRulesPanel({ admin }: { admin: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["delivery"], queryFn: () => api.get<DeliveryRules>("/v1/settings/delivery") });
  const [r, setR] = useState<DeliveryRules | null>(null);
  const rules = r ?? q.data;
  const save = useMutation({
    mutationFn: () => api.put<DeliveryRules>("/v1/settings/delivery", rules),
    onSuccess: (d) => { qc.setQueryData(["delivery"], d); setR(null); toast({ text: "Delivery rules saved. They apply within 30 seconds." }); },
  });
  if (!rules) return null;
  const edit = (p: Partial<DeliveryRules>) => setR({ ...rules, ...p });
  const setCap = (ch: Channel, cap?: { max: number; hours: number }) => {
    const caps = { ...rules.caps };
    if (cap) caps[ch] = cap;
    else delete caps[ch];
    edit({ caps });
  };
  const qh = rules.quiet;
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Delivery rules</h3><span className="xs muted">Transactional templates skip these</span></div>
      <div className="panel-body">
        <ErrorNote error={save.error ?? q.error} />
        <fieldset disabled={!admin} style={{ border: 0, padding: 0, margin: 0 }}>
          <h4 className="small" style={{ marginBottom: 8 }}>Frequency caps</h4>
          <p className="xs muted" style={{ marginBottom: 10 }}>The most marketing messages one contact gets per channel. Messages over the cap are dropped and show as "capped".</p>
          <table className="table" style={{ marginBottom: 18 }}>
            <tbody>{CHANNELS.map((ch) => {
              const cap = rules.caps[ch];
              return (
                <tr key={ch}>
                  <td style={{ width: 130 }}><span className={`chan chan-${ch}`}>{CHANNEL_NAMES[ch]}</span></td>
                  <td>
                    <label className="check" style={{ margin: 0 }}>
                      <input type="checkbox" checked={!!cap} onChange={(e) => setCap(ch, e.target.checked ? { max: 3, hours: 24 } : undefined)} />
                      {cap ? <>At most <input className="input inline" style={{ width: 64 }} type="number" min={1} value={cap.max} onChange={(e) => setCap(ch, { ...cap, max: Number(e.target.value) })} aria-label="Messages" /> per <input className="input inline" style={{ width: 64 }} type="number" min={1} value={cap.hours} onChange={(e) => setCap(ch, { ...cap, hours: Number(e.target.value) })} aria-label="Hours" /> hours</> : "No limit"}
                    </label>
                  </td>
                </tr>
              );
            })}</tbody>
          </table>
          <h4 className="small" style={{ marginBottom: 8 }}>Quiet hours</h4>
          <label className="check"><input type="checkbox" checked={qh.enabled} onChange={(e) => edit({ quiet: { ...qh, enabled: e.target.checked } })} />Hold marketing messages overnight and send them when quiet hours end</label>
          {qh.enabled && (
            <>
              <div className="row" style={{ marginBottom: 12 }}>
                From <input className="input inline" type="time" value={qh.start} onChange={(e) => edit({ quiet: { ...qh, start: e.target.value } })} aria-label="Start" />
                to <input className="input inline" type="time" value={qh.end} onChange={(e) => edit({ quiet: { ...qh, end: e.target.value } })} aria-label="End" />
                in <select className="select inline" value={qh.timezone} onChange={(e) => edit({ quiet: { ...qh, timezone: e.target.value } })} aria-label="Time zone">{ZONES.map((z) => <option key={z}>{z}</option>)}</select>
              </div>
              <p className="xs muted" style={{ marginBottom: 8 }}>A contact with a "timezone" attribute gets quiet hours in their own time. On these channels:</p>
              <div className="row" style={{ marginBottom: 12 }}>
                {CHANNELS.map((ch) => (
                  <label key={ch} className="check" style={{ margin: 0 }}>
                    <input type="checkbox" checked={qh.channels.includes(ch)} onChange={(e) => edit({ quiet: { ...qh, channels: e.target.checked ? [...qh.channels, ch] : qh.channels.filter((c) => c !== ch) } })} />
                    {CHANNEL_NAMES[ch]}
                  </label>
                ))}
              </div>
            </>
          )}
          <h4 className="small" style={{ margin: "6px 0 8px" }}>Reply keywords</h4>
          <p className="xs muted" style={{ marginBottom: 10 }}>
            When someone replies to an SMS, WhatsApp or RCS message with just one of these words, they're unsubscribed from (or subscribed back to) that channel.
            Case and punctuation don't matter, so "Stop." counts. Point your provider's incoming-message webhook at its Pulse webhook URL.
          </p>
          <div className="grid-2">
            <KeywordField label="Unsubscribe" value={rules.keywords?.opt_out ?? []} onChange={(opt_out) => edit({ keywords: { opt_in: rules.keywords?.opt_in ?? [], opt_out } })} />
            <KeywordField label="Subscribe again" value={rules.keywords?.opt_in ?? []} onChange={(opt_in) => edit({ keywords: { opt_out: rules.keywords?.opt_out ?? [], opt_in } })} />
          </div>
          <h4 className="small" style={{ margin: "6px 0 8px" }}>Reply tracking</h4>
          <Field label="Reply tracking address" hint="e.g. replies@inbound.example.com — point this domain's MX at your email provider's inbound parse, and its webhook at the Reply webhook URL shown under Bounces">
            <input className="input" type="email" value={rules.reply_to ?? ""} placeholder="replies@inbound.example.com" onChange={(e) => edit({ reply_to: e.target.value.trim() || undefined })} />
          </Field>
          <p className="xs muted" style={{ marginTop: -6, marginBottom: 14 }}>Emails from triggered and automation campaigns use it as their Reply-To, so a "Replies to email" decision can see who answered.</p>
          {admin && <button className="btn primary" disabled={!r || save.isPending} onClick={() => save.mutate()}>Save delivery rules</button>}
        </fieldset>
      </div>
    </section>
  );
}

const TO_LABEL: Record<Channel, [string, string]> = {
  email: ["Send to email address", "you@company.com"],
  sms: ["Send to phone number", "+919812345678"],
  whatsapp: ["Send to WhatsApp number", "+919812345678"],
  rcs: ["Send to phone number", "+919812345678"],
  inbox: ["Send to contact (external ID)", "u-1042"],
  push: ["Send to device token", "FCM registration token"],
};

// ProviderTest sends a fixed test message straight through one provider and shows its answer.
function ProviderTest({ provider, onClose }: { provider: Provider; onClose: () => void }) {
  const [to, setTo] = useState("");
  const [tpl, setTpl] = useState("");
  const send = useMutation({
    mutationFn: async () => {
      const res = await fetch(`/v1/channels/providers/${provider.id}/test`, {
        method: "POST", credentials: "same-origin",
        headers: { "Content-Type": "application/json", "X-Pulse-CSRF": "1" },
        body: JSON.stringify({ to, template_name: tpl }),
      });
      return (await res.json()) as { ok: boolean; provider_message_id?: string; error?: string; meaning?: string };
    },
  });
  const [label, ph] = TO_LABEL[provider.channel];
  return (
    <Modal title={`Send a test through ${provider.name}`} onClose={onClose} footer={<><button className="btn" onClick={onClose}>Close</button><button className="btn primary" disabled={!to || send.isPending} onClick={() => send.mutate()}>Send test</button></>}>
      <p className="small muted" style={{ marginBottom: 14 }}>Sends a short fixed message directly through this provider, to check its settings. Nothing is recorded on any contact.</p>
      <Field label={label}><input className="input" value={to} placeholder={ph} onChange={(e) => setTo(e.target.value.trim())} autoFocus /></Field>
      {provider.channel === "whatsapp" && (
        <Field label="Approved template to send" hint="WhatsApp only allows approved templates. Meta's sample hello_world is used if empty."><input className="input" value={tpl} placeholder="hello_world" onChange={(e) => setTpl(e.target.value.trim())} /></Field>
      )}
      {send.data?.ok && <div className="alert ok">Accepted by the provider{send.data.provider_message_id ? ` (message ${send.data.provider_message_id})` : ""}.{provider.provider.startsWith("sandbox") ? " It's in the test inbox." : ""}</div>}
      {send.data && !send.data.ok && <div className="alert error"><strong>Not sent: {send.data.meaning}.</strong><br />{send.data.error}</div>}
      <ErrorNote error={send.error} />
    </Modal>
  );
}

// KeywordField edits a list of single-word keywords as comma-separated text.
function KeywordField({ label, value, onChange }: { label: string; value: string[]; onChange: (v: string[]) => void }) {
  const [text, setText] = useState<string | null>(null);
  return (
    <Field label={label} hint="Single words, separated by commas.">
      <input className="input" value={text ?? value.join(", ")}
        onChange={(e) => { setText(e.target.value); onChange(e.target.value.split(",").map((w) => w.trim().toUpperCase()).filter(Boolean)); }}
        onBlur={() => setText(null)} />
    </Field>
  );
}

const BOUNCE_SERVICES: Record<string, string> = {
  sendgrid: "SendGrid (Event Webhook)",
  postmark: "Postmark (Bounce and Spam Complaint webhooks)",
  mailgun: "Mailgun (Permanent Failure, Temporary Failure and Spam Complaint webhooks)",
  ses: "Amazon SES (an SNS topic with an HTTPS subscription; Pulse confirms it for you)",
  sparkpost: "SparkPost (Bounce, Out of Band and Spam Complaint events)",
  generic: 'Anything else: post JSON like {"email": "…", "type": "hard"} (or a list of them). type is hard, soft or complaint.',
};

// BouncesPanel shows where the email service should report bounces and complaints, and what each does.
function BouncesPanel() {
  const toast = useToast();
  const q = useQuery({ queryKey: ["bounce-settings"], queryFn: () => api.get<BounceSettings>("/v1/settings/bounces") });
  const copy = (text: string) =>
    navigator.clipboard.writeText(text).then(() => toast({ text: "URL copied" }), () => toast({ text: "Couldn't copy. Select the text and copy it yourself.", error: true }));
  const formats = q.data?.formats ?? Object.keys(BOUNCE_SERVICES);
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Bounces and complaints</h3><span className="xs muted">Keeps your sender reputation clean</span></div>
      <div className="panel-body">
        <ErrorNote error={q.error} />
        <p className="small muted" style={{ marginBottom: 12 }}>
          In your email service, point the bounce and spam complaint webhook at this URL. Pulse then stops emailing addresses that don't work or that marked you as spam.
        </p>
        {q.data && (
          <div className="row" style={{ marginBottom: 16, flexWrap: "nowrap" }}>
            <code style={{ background: "var(--porcelain)", border: "1px solid var(--rule)", borderRadius: "var(--r-sm)", padding: "6px 10px", wordBreak: "break-all", flex: 1 }}>{q.data.url}</code>
            <button className="btn sm" onClick={() => copy(q.data.url)}>Copy</button>
          </div>
        )}
        {q.data?.reply_url && (
          <>
            <p className="small muted" style={{ marginBottom: 8 }}>Reply webhook URL: point your email provider's inbound parse here so replies to campaign emails are recorded (set the reply tracking address under Delivery rules).</p>
            <div className="row" style={{ marginBottom: 16, flexWrap: "nowrap" }}>
              <code style={{ background: "var(--porcelain)", border: "1px solid var(--rule)", borderRadius: "var(--r-sm)", padding: "6px 10px", wordBreak: "break-all", flex: 1 }}>{q.data.reply_url}</code>
              <button className="btn sm" onClick={() => copy(q.data!.reply_url!)}>Copy</button>
            </div>
          </>
        )}
        <div className="grid-2">
          <div>
            <h4 className="small" style={{ marginBottom: 8 }}>Works with</h4>
            <ul className="small" style={{ margin: "0 0 14px", paddingLeft: 18 }}>
              {formats.map((f) => <li key={f} style={{ marginBottom: 4 }}>{BOUNCE_SERVICES[f] ?? f}</li>)}
            </ul>
          </div>
          <div>
            <h4 className="small" style={{ marginBottom: 8 }}>What happens</h4>
            <dl className="kv" style={{ gridTemplateColumns: "110px 1fr" }}>
              <dt>Hard bounce</dt><dd>The address doesn't exist. The contact gets <code>email_bounced</code> = true and no more email is sent to them.</dd>
              <dt>Soft bounce</dt><dd>A passing problem, like a full inbox. Recorded as an <code>email_bounced</code> event; email keeps going.</dd>
              <dt>Complaint</dt><dd>They marked you as spam. The contact is unsubscribed from email.</dd>
            </dl>
          </div>
        </div>
        {q.data && q.data.linking === false && (
          <div className="alert info" style={{ marginTop: 14 }}>
            Reports can't be matched to the messages they're about: the server has no secret key (PULSE_SECRET_KEY). Contacts are still updated, but bounces and complaints don't show in campaign reports.
          </div>
        )}
        {q.data?.signing && <BounceSigning url={q.data.url} signing={q.data.signing} />}
      </div>
    </section>
  );
}

interface SigningStatus { sendgrid: boolean; mailgun: boolean; ses: boolean; basic: boolean; basic_user?: string }
interface BounceSettings { url: string; formats: string[]; reply_url?: string; signing?: SigningStatus; linking?: boolean }
type SigningKey = "sendgrid" | "mailgun" | "basic";

// Checked shows how a format's posts are checked. Once any key is saved the workspace is verified:
// a format without its own key then needs the basic auth password, or its posts are refused.
function Checked({ on, basic, signing }: { on: boolean; basic?: boolean; signing: SigningStatus }) {
  if (on) return <span className="badge ok">{basic ? "Password checked" : "Signature checked"}</span>;
  if (signing.basic) return <span className="badge ok">Password checked</span>;
  if (signing.sendgrid || signing.mailgun) {
    return <span className="badge bad" title="Another service's key is saved, so unchecked posts are refused. Save its key, or a basic auth password.">Refused</span>;
  }
  return <span className="badge warn" title="Anyone who knows the URL can post reports">URL secret only</span>;
}

// BounceSigning sets the keys each email service's webhook posts are checked with. Secrets are
// never shown again; empty fields keep what is saved. Once a key is saved, unsigned posts are refused.
function BounceSigning({ url, signing }: { url: string; signing: SigningStatus }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [sendgrid, setSendgrid] = useState("");
  const [mailgun, setMailgun] = useState("");
  const [user, setUser] = useState(signing.basic_user ?? "");
  const [password, setPassword] = useState("");
  const done = (r: { signing: SigningStatus }, text: string) => {
    qc.setQueryData<BounceSettings>(["bounce-settings"], (old) => (old ? { ...old, signing: r.signing } : old));
    qc.invalidateQueries({ queryKey: ["bounce-settings"] });
    toast({ text });
  };
  const save = useMutation({
    mutationFn: () => api.put<{ signing: SigningStatus }>("/v1/settings/bounces/signing", {
      ...(sendgrid.trim() ? { sendgrid_public_key: sendgrid.trim() } : {}),
      ...(mailgun.trim() ? { mailgun_signing_key: mailgun.trim() } : {}),
      ...(password ? { basic_user: user.trim(), basic_password: password } : {}),
    }),
    onSuccess: (r) => { setSendgrid(""); setMailgun(""); setPassword(""); setUser(r.signing.basic_user ?? user.trim()); done(r, "Webhook keys saved"); },
  });
  const clear = useMutation({
    mutationFn: (k: SigningKey) => api.put<{ signing: SigningStatus }>("/v1/settings/bounces/signing", { clear: [k] }),
    onSuccess: (r, k) => { if (k === "basic") setUser(""); done(r, "Key removed: those posts are accepted on the URL alone again"); },
  });
  const userChanged = user.trim() !== (signing.basic_user ?? "");
  const basicProblem = password && !user.trim() ? "Enter a user name too."
    : password && password.length < 12 ? "The password needs at least 12 characters."
    : !password && userChanged && user.trim() ? "Enter the password to change the user name."
    : "";
  const anything = !!(sendgrid.trim() || mailgun.trim() || password);
  const remove = (k: SigningKey, name: string) =>
    confirm(`Remove the ${name} key? Posts in that format are then accepted on the URL alone.`) && clear.mutate(k);
  const exampleUser = encodeURIComponent(user.trim() || "user");
  const basicURL = url.replace(/^(https?:\/\/)/, `$1${exampleUser}:<password>@`);

  return (
    <div style={{ marginTop: 18 }}>
      <h4 className="small" style={{ marginBottom: 6 }}>Webhook signatures</h4>
      <p className="small muted" style={{ marginBottom: 12 }}>
        Without a key, anyone who learns the URL can report bounces. Once any key is saved, every post must pass a check: its service's signature, or the basic auth password for services without one; anything else is refused. Saved keys are never shown again; leave a field empty to keep what's saved.
      </p>
      <form onSubmit={(e) => { e.preventDefault(); if (anything && !basicProblem) save.mutate(); }}>
        <div className="table-scroll">
          <table className="table">
            <thead><tr><th scope="col">Service</th><th scope="col">Checked</th><th scope="col">Key</th><th scope="col" /></tr></thead>
            <tbody>
              <tr>
                <td>SendGrid</td>
                <td><Checked on={signing.sendgrid} signing={signing} /></td>
                <td>
                  <Field label="Signed Event Webhook verification key" hint="The base64 public key SendGrid shows when you turn on the signed Event Webhook.">
                    <textarea className="textarea" rows={2} value={sendgrid} onChange={(e) => setSendgrid(e.target.value)} placeholder={signing.sendgrid ? "Saved. Paste a new key to replace it" : "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE…"} spellCheck={false} autoComplete="off" />
                  </Field>
                </td>
                <td className="num">{signing.sendgrid && <button type="button" className="btn ghost sm danger" disabled={clear.isPending} onClick={() => remove("sendgrid", "SendGrid")}>Remove</button>}</td>
              </tr>
              <tr>
                <td>Mailgun</td>
                <td><Checked on={signing.mailgun} signing={signing} /></td>
                <td>
                  <Field label="HTTP webhook signing key" hint="Mailgun → Sending → Webhooks. Each signature is accepted once.">
                    <input className="input" type="password" value={mailgun} onChange={(e) => setMailgun(e.target.value)} placeholder={signing.mailgun ? "Saved. Enter a new key to replace it" : ""} autoComplete="new-password" />
                  </Field>
                </td>
                <td className="num">{signing.mailgun && <button type="button" className="btn ghost sm danger" disabled={clear.isPending} onClick={() => remove("mailgun", "Mailgun")}>Remove</button>}</td>
              </tr>
              <tr>
                <td>Amazon SES</td>
                <td><Checked on={signing.ses} signing={signing} /></td>
                <td className="small muted">Through SNS: every notification's SNS signature is checked. Nothing to set.</td>
                <td />
              </tr>
              <tr>
                <td>Postmark, SparkPost and other posts</td>
                <td><Checked on={signing.basic} basic signing={signing} /></td>
                <td>
                  <div className="grid-2">
                    <Field label="Basic auth user"><input className="input" value={user} onChange={(e) => setUser(e.target.value)} autoComplete="off" /></Field>
                    <Field label="Password" hint="At least 12 characters.">
                      <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder={signing.basic ? "Saved. Enter a new one to replace it" : ""} autoComplete="new-password" />
                    </Field>
                  </div>
                  <p className="xs muted" style={{ margin: 0 }}>Put them in the webhook URL: <code style={{ wordBreak: "break-all" }}>{basicURL}</code></p>
                  {basicProblem && <p className="xs" style={{ color: "var(--danger)", margin: "4px 0 0" }} role="alert">{basicProblem}</p>}
                </td>
                <td className="num">{signing.basic && <button type="button" className="btn ghost sm danger" disabled={clear.isPending} onClick={() => remove("basic", "basic auth")}>Remove</button>}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div className="row" style={{ marginTop: 12 }}>
          <button className="btn primary sm" disabled={!anything || !!basicProblem || save.isPending}>{save.isPending ? "Saving…" : "Save keys"}</button>
        </div>
      </form>
      <ErrorNote error={save.error ?? clear.error} />
    </div>
  );
}

// SimulateBounce reports a fake bounce or complaint for an address, as the email service would.
function SimulateBounce() {
  const toast = useToast();
  const [email, setEmail] = useState("");
  const [type, setType] = useState<"hard" | "soft" | "complaint">("hard");
  const send = useMutation({
    mutationFn: () => api.post("/v1/sandbox/bounce", { email: email.trim(), type }),
    onSuccess: () => toast({ text: type === "hard" ? "Hard bounce recorded. Email to this address is now suppressed." : type === "soft" ? "Soft bounce recorded. Email keeps going." : "Complaint recorded. The contact is unsubscribed from email." }),
  });
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Simulate a bounce</h3><span className="xs muted">As if the email service reported it</span></div>
      <div className="panel-body">
        <div className="row" style={{ alignItems: "flex-end" }}>
          <Field label="Email"><input className="input" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="someone@example.com" /></Field>
          <Field label="Kind">
            <select className="select" value={type} onChange={(e) => setType(e.target.value as "hard" | "soft" | "complaint")}>
              <option value="hard">Hard bounce</option><option value="soft">Soft bounce</option><option value="complaint">Spam complaint</option>
            </select>
          </Field>
          <button className="btn" style={{ marginBottom: 14 }} disabled={!email.trim() || send.isPending} onClick={() => send.mutate()}>Report</button>
        </div>
        <p className="xs muted">The contact with this email gets an <code>{type === "complaint" ? "email_complained" : "email_bounced"}</code> event; journeys and segments can use it.</p>
        <ErrorNote error={send.error} />
      </div>
    </section>
  );
}

// SimulateReply sends a fake inbound SMS, WhatsApp or RCS message (for RCS optionally a tapped
// suggestion), as if a contact had replied.
function SimulateReply() {
  const toast = useToast();
  const [ch, setCh] = useState<"sms" | "whatsapp" | "rcs">("sms");
  const [from, setFrom] = useState("");
  const [text, setText] = useState("STOP");
  const [tap, setTap] = useState<"" | "reply" | "action">("");
  const [postback, setPostback] = useState("");
  const send = useMutation({
    mutationFn: () => api.post<{ keyword: string; action: string }>("/v1/sandbox/inbound",
      ch === "rcs" && tap ? { channel: ch, from, text, suggestion: tap, postback } : { channel: ch, from, text }),
    onSuccess: (r) => toast({ text: r.action === "opt_out" ? `"${r.keyword}" unsubscribes this number from ${CHANNEL_NAMES[ch]}.` : r.action === "opt_in" ? `"${r.keyword}" subscribes this number again.` : "Recorded as a reply. It isn't a keyword, so subscriptions don't change." }),
  });
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Simulate a reply</h3><span className="xs muted">As if a contact texted back</span></div>
      <div className="panel-body">
        <div className="row" style={{ alignItems: "flex-end", flexWrap: "wrap" }}>
          <Field label="Channel">
            <select className="select" value={ch} onChange={(e) => setCh(e.target.value as "sms" | "whatsapp" | "rcs")}>
              <option value="sms">SMS</option><option value="whatsapp">WhatsApp</option><option value="rcs">RCS</option>
            </select>
          </Field>
          <Field label="From"><input className="input" value={from} onChange={(e) => setFrom(e.target.value)} placeholder="+919812345678" /></Field>
          {ch === "rcs" && (
            <Field label="Kind">
              <select className="select" value={tap} onChange={(e) => setTap(e.target.value as "" | "reply" | "action")}>
                <option value="">Typed message</option><option value="reply">Tapped a suggested reply</option><option value="action">Tapped a suggested action</option>
              </select>
            </Field>
          )}
          <Field label={ch === "rcs" && tap ? "Suggestion text" : "Message"}><input className="input" value={text} onChange={(e) => setText(e.target.value)} /></Field>
          {ch === "rcs" && tap && <Field label="Postback data"><input className="input" value={postback} onChange={(e) => setPostback(e.target.value)} placeholder="optional" /></Field>}
          <button className="btn" style={{ marginBottom: 14 }} disabled={!from.trim() || !text.trim() || send.isPending} onClick={() => send.mutate()}>Send reply</button>
        </div>
        <p className="xs muted">
          Every contact with this phone number gets a <code>{ch}_received</code> event; journeys and segments can use it.
          {ch === "rcs" && <> If the contact got an RCS message in the last 7 days, it also records <code>rcs_replied</code> on that message, for journeys&rsquo; &ldquo;gets a reply&rdquo; decision.</>}
        </p>
        <ErrorNote error={send.error} />
      </div>
    </section>
  );
}

export function TestInbox() {
  const [channel, setChannel] = useState<Channel | "">("");
  const toast = useToast();
  const q = useQuery({
    queryKey: ["sandbox", channel],
    queryFn: () => api.get<{ messages: SandboxMessage[] }>(`/v1/sandbox/messages${channel ? `?channel=${channel}` : ""}`),
    refetchInterval: 2000,
  });
  const fail = useMutation({
    mutationFn: (m: SandboxMessage) => api.post("/v1/sandbox/receipts", { provider_message_id: `sandbox-${m.message_id}`, status: "failed", unreachable: true, reason: "simulated: number not reachable" }),
    onSuccess: () => toast({ text: "Reported as undeliverable. If it has an SMS fallback, the SMS appears here shortly." }),
  });
  const msgs = q.data?.messages ?? [];
  return (
    <div className="page">
      <PageHead title="Test inbox" intro="Messages sent through sandbox providers land here instead of reaching anyone. Mark a WhatsApp or RCS message undeliverable to test its SMS fallback." />
      <div className="tabs" role="tablist">
        {(["", ...CHANNELS] as const).map((c) => <button key={c} role="tab" aria-selected={channel === c} onClick={() => setChannel(c)}>{c ? CHANNEL_NAMES[c] : "All"}</button>)}
      </div>
      <ErrorNote error={q.error ?? fail.error} />
      <div className="panel">
        {msgs.length === 0 ? (
          <Empty title="Nothing here yet">Add a sandbox provider on Channels, then send a test from a template or run a journey.</Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Channel</th><th>To</th><th>Message</th><th>Received</th><th /></tr></thead>
            <tbody>{msgs.map((m) => {
              const c = m.payload.content ?? {};
              const text = m.payload.subject || c.text || (c.template_name && `${c.template_name}(${(c.body_params ?? []).join(", ")})`) || (c.title && `${c.title}: ${c.body}`) || c.card?.title;
              return (
                <tr key={m.id}>
                  <td><ChannelLabel channel={m.channel} /></td>
                  <td>{m.recipient.length > 28 ? m.recipient.slice(0, 28) + "…" : m.recipient}</td>
                  <td style={{ maxWidth: 460 }}>{text}{m.message_id.endsWith(":sms") && <span className="badge warn" style={{ marginLeft: 8 }}>Fallback</span>}</td>
                  <td className="muted">{timeAgo(m.created_at)}</td>
                  <td className="num">{(m.channel === "whatsapp" || m.channel === "rcs") && <button className="btn sm" onClick={() => fail.mutate(m)}>Mark undeliverable</button>}</td>
                </tr>
              );
            })}</tbody>
          </table>
        )}
      </div>
      <div className="grid-2" style={{ alignItems: "start" }}>
        <SimulateReply />
        <SimulateBounce />
      </div>
    </div>
  );
}

// ---- sending limits (M3) ----

const LIMIT_ZONES = ["UTC", "Asia/Kolkata", "Asia/Dubai", "Asia/Singapore", "Europe/London", "America/New_York", "America/Los_Angeles"];

/** LimitsSummary shows a provider's rate, daily quota and today's usage. */
function LimitsSummary({ p }: { p: Provider }) {
  const l = p.limits ?? {};
  const used = p.usage_today ?? 0;
  if (!l.rate_per_second && !l.daily_quota) return <span className="xs muted">No limits{used ? ` · ${used.toLocaleString("en-IN")} sent today` : ""}</span>;
  const pct = l.daily_quota ? Math.min(100, (used / l.daily_quota) * 100) : 0;
  return (
    <div>
      <div className="xs">{l.rate_per_second ? `${l.rate_per_second}/s` : "No rate limit"}{l.daily_quota ? ` · ${used.toLocaleString("en-IN")} of ${l.daily_quota.toLocaleString("en-IN")} today` : ` · ${used.toLocaleString("en-IN")} sent today`}</div>
      {!!l.daily_quota && <div className={`quota-bar${used >= l.daily_quota ? " full" : ""}`} role="meter" aria-label="Daily quota used" aria-valuemin={0} aria-valuemax={l.daily_quota} aria-valuenow={used}><span style={{ width: `${pct}%` }} /></div>}
    </div>
  );
}

/** LimitsForm sets a provider's messages per second and daily quota. */
function LimitsForm({ provider, onClose }: { provider: Provider; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [l, setL] = useState<ProviderLimits>({ timezone: "Asia/Kolkata", ...provider.limits });
  const save = useMutation({
    mutationFn: () => api.put(`/v1/channels/providers/${provider.id}/limits`, { rate_per_second: l.rate_per_second || 0, daily_quota: l.daily_quota || 0, timezone: l.timezone ?? "" }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["providers"] }); toast({ text: "Sending limits saved. Every dispatcher follows them within 30 seconds." }); onClose(); },
  });
  return (
    <Modal title={`Sending limits: ${provider.name}`} onClose={onClose} footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={save.isPending} onClick={() => save.mutate()}>Save limits</button></>}>
      <ErrorNote error={save.error} />
      <p className="small muted" style={{ marginTop: 0 }}>Shared by every Pulse server, on top of the workspace's own rate for the channel. Leave empty (or 0) for no limit.</p>
      <div className="grid-2">
        <Field label="Messages per second" hint="The provider's throughput, e.g. 10 for a small SMS account.">
          <input className="input" type="number" min={0} max={10000} step="any" value={l.rate_per_second ?? ""} onChange={(e) => setL({ ...l, rate_per_second: e.target.value ? Number(e.target.value) : undefined })} />
        </Field>
        <Field label="Daily quota" hint="Messages a day. Over it, messages wait for the next day, or are dropped at their send deadline.">
          <input className="input" type="number" min={0} value={l.daily_quota ?? ""} onChange={(e) => setL({ ...l, daily_quota: e.target.value ? Math.trunc(Number(e.target.value)) : undefined })} />
        </Field>
        <Field label="The day starts at midnight in">
          <select className="select" value={l.timezone || "UTC"} onChange={(e) => setL({ ...l, timezone: e.target.value })}>
            {[...new Set([...(l.timezone ? [l.timezone] : []), ...LIMIT_ZONES])].map((z) => <option key={z} value={z}>{z}</option>)}
          </select>
        </Field>
      </div>
      <p className="xs muted">Sent today: {(provider.usage_today ?? 0).toLocaleString("en-IN")}.</p>
    </Modal>
  );
}

// ---- saved test contacts (M3) ----

const BLANK_TC: TestContact = { id: 0, name: "", external_id: "", email: "", phone: "", push_token: "" };

/** TestContactsPanel keeps the workspace's test contacts, offered by every "Send a test" dialog. */
function TestContactsPanel({ canEdit }: { canEdit: boolean }) {
  const q = useTestContacts();
  const qc = useQueryClient();
  const [edit, setEdit] = useState<TestContact | null>(null);
  const save = useMutation({
    mutationFn: (tc: TestContact) => (tc.id ? api.put(`/v1/test-contacts/${tc.id}`, tc) : api.post("/v1/test-contacts", tc)),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["test-contacts"] }); setEdit(null); },
  });
  const remove = useMutation({ mutationFn: (id: number) => api.del(`/v1/test-contacts/${id}`), onSuccess: () => qc.invalidateQueries({ queryKey: ["test-contacts"] }) });
  const tcs = q.data?.test_contacts ?? [];
  return (
    <section className="panel" style={{ marginBottom: 16 }} aria-labelledby="tc-head">
      <div className="panel-head">
        <h3 id="tc-head">Test contacts</h3>
        {canEdit && <button className="btn sm" onClick={() => setEdit({ ...BLANK_TC })}>Add test contact</button>}
      </div>
      <ErrorNote error={q.error ?? remove.error} />
      {tcs.length === 0 ? (
        <div className="panel-body small muted">People every "Send a test" dialog can send to: yourself, your team, a QA phone. Pick an existing contact (the test is personalised with their data) or enter addresses per channel.</div>
      ) : (
        <table className="table">
          <thead><tr><th>Name</th><th>Contact</th><th>Email</th><th>Phone</th><th>Push device</th><th aria-label="Actions" /></tr></thead>
          <tbody>{tcs.map((t) => (
            <tr key={t.id}>
              <td><strong>{t.name}</strong></td>
              <td className="small">{t.external_id || <span className="faint">–</span>}</td>
              <td className="small">{t.email || <span className="faint">–</span>}</td>
              <td className="small">{t.phone || <span className="faint">–</span>}</td>
              <td className="small">{t.push_token ? `${t.push_token.slice(0, 14)}…` : <span className="faint">–</span>}</td>
              <td className="num">{canEdit && <div className="row" style={{ justifyContent: "flex-end" }}>
                <button className="btn sm" onClick={() => setEdit({ ...BLANK_TC, ...t })}>Edit</button>
                <button className="btn ghost sm danger" onClick={() => confirm(`Remove ${t.name} from the test contacts?`) && remove.mutate(t.id)}>Remove</button>
              </div>}</td>
            </tr>
          ))}</tbody>
        </table>
      )}
      {edit && (
        <Modal title={edit.id ? `Edit ${edit.name}` : "Add a test contact"} onClose={() => setEdit(null)} footer={<><button className="btn" onClick={() => setEdit(null)}>Cancel</button><button className="btn primary" disabled={save.isPending} onClick={() => save.mutate(edit)}>Save</button></>}>
          <ErrorNote error={save.error} />
          <Field label="Name"><input className="input" value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="Priya (QA)" autoFocus /></Field>
          <Field label="An existing contact (external ID, optional)" hint="Tests to them use their own data and addresses, like any contact."><input className="input" value={edit.external_id ?? ""} onChange={(e) => setEdit({ ...edit, external_id: e.target.value })} /></Field>
          <p className="xs muted">Or addresses, used as they are (nothing is tracked or recorded):</p>
          <div className="grid-2">
            <Field label="Email"><input className="input" type="email" value={edit.email ?? ""} onChange={(e) => setEdit({ ...edit, email: e.target.value })} /></Field>
            <Field label="Phone (SMS, WhatsApp, RCS)" hint="With country code, e.g. +919812345678."><input className="input" value={edit.phone ?? ""} onChange={(e) => setEdit({ ...edit, phone: e.target.value })} /></Field>
          </div>
          <Field label="Push device token (optional)"><input className="input" value={edit.push_token ?? ""} onChange={(e) => setEdit({ ...edit, push_token: e.target.value })} /></Field>
        </Modal>
      )}
    </section>
  );
}
