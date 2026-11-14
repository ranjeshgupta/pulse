import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, type Broadcast, type Form, type Journey, type Segment, type Stage, type User } from "../api";
import { chipToken } from "../contactFilters";
import { defaults, FILTER_MENU, findItem, missing, type MenuField, type MenuItem, type OptionSource } from "../contactFilterMenu";
import { Field, Modal } from "../ui";
import { useCompanyOptions } from "./Companies";
import { useTopics } from "../TopicPick";

type Option = { value: string; label: string };

// useSources loads the lists the menu's pickers offer, only once the menu is open.
function useSources(): Record<OptionSource, Option[] | undefined> {
  const opts = { staleTime: 60_000, retry: false } as const;
  const segments = useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments"), ...opts }).data?.segments;
  const tags = useQuery({ queryKey: ["tags"], queryFn: () => api.get<{ tags: string[] }>("/v1/tags"), ...opts }).data?.tags;
  const stages = useQuery({ queryKey: ["stages"], queryFn: () => api.get<{ stages: Stage[] }>("/v1/stages"), ...opts }).data?.stages;
  const companies = useCompanyOptions().data;
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<{ users: User[] }>("/v1/users"), ...opts }).data?.users;
  const events = useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names"), ...opts }).data?.names;
  const broadcasts = useQuery({ queryKey: ["broadcasts"], queryFn: () => api.get<{ broadcasts: Broadcast[] }>("/v1/broadcasts"), ...opts }).data?.broadcasts;
  const journeys = useQuery({ queryKey: ["journeys"], queryFn: () => api.get<{ journeys: Journey[] }>("/v1/journeys"), ...opts }).data?.journeys;
  const forms = useQuery({ queryKey: ["forms"], queryFn: () => api.get<{ forms: Form[] }>("/v1/forms"), ...opts }).data?.forms;
  const topics = useTopics().data?.topics;
  return {
    segments: segments?.map((s) => ({ value: s.name, label: s.name })),
    segmentIds: segments?.map((s) => ({ value: String(s.id), label: s.name })),
    tags: tags?.map((t) => ({ value: t, label: t })),
    stages: stages?.map((s) => ({ value: s.name, label: s.name })),
    companies: companies?.map((c) => ({ value: String(c.id), label: c.name })),
    // Teammates need view access to settings; without it, "me" and "nobody" still work.
    users: [{ value: "me", label: "Me" }, { value: "none", label: "Nobody" }, ...(users ?? []).map((u) => ({ value: u.email, label: u.name || u.email }))],
    events: events?.map((e) => ({ value: e, label: e })),
    campaigns: [
      ...(broadcasts ?? []).map((b) => ({ value: `broadcast/${b.id}`, label: `${b.name} (one-time)` })),
      ...(journeys ?? []).map((j) => ({ value: `journey/${j.id}`, label: j.name })),
    ],
    journeys: journeys?.map((j) => ({ value: String(j.id), label: j.name })),
    forms: forms?.map((f) => ({ value: String(f.id), label: f.name })),
    unsubscribed: [
      { value: "email", label: "Email" }, { value: "sms", label: "SMS" }, { value: "whatsapp", label: "WhatsApp" }, { value: "rcs", label: "RCS" },
      { value: "push", label: "Push" }, { value: "all", label: "Everything" },
      ...(topics ?? []).map((t) => ({ value: t.key, label: `Topic: ${t.name}` })),
    ],
  };
}

/** FilterMenu: every filter of the contact list as a plain question; Add turns it into chips. */
export function FilterMenu({ initial, onAdd, onClose }: { initial?: string; onAdd: (chips: string[]) => void; onClose: () => void }) {
  const today = new Date().toISOString().slice(0, 10);
  const first = (initial && findItem(initial)) || FILTER_MENU[1].items[0]; // "Did an event" unless asked for another
  const [item, setItem] = useState<MenuItem>(first);
  const [values, setValues] = useState<Record<string, string>>(() => defaults(first, today));
  const sources = useSources();
  const pick = (i: MenuItem) => { setItem(i); setValues(defaults(i, today)); };
  const need = missing(item, values);
  const add = () => onAdd(item.build(values).filter(([, v]) => v !== "").map(([k, v]) => chipToken(k, String(v))));
  return (
    <Modal wide title="Add a filter" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!!need} title={need ? `Choose ${need.toLowerCase()} first` : undefined} onClick={add}>Add filter</button>
    </>}>
      <div className="cfm">
        <nav className="cfm-list" aria-label="Filters">
          {FILTER_MENU.map((g) => (
            <div key={g.group}>
              <div className="cfm-group">{g.group}</div>
              {g.items.map((i) => (
                <button key={i.id} type="button" className={i.id === item.id ? "on" : ""} aria-pressed={i.id === item.id} onClick={() => pick(i)}>{i.label}</button>
              ))}
            </div>
          ))}
        </nav>
        <div className="cfm-fields">
          <h4>{item.label}</h4>
          {item.hint && <p className="xs muted" style={{ marginTop: -4 }}>{item.hint}</p>}
          <div className="cfm-grid">
            {item.fields.map((f) => (
              <MenuInput key={f.key} f={f} value={values[f.key] ?? ""} options={f.options ?? (f.source ? sources[f.source] : undefined)}
                onChange={(v) => setValues({ ...values, [f.key]: v })} />
            ))}
          </div>
        </div>
      </div>
    </Modal>
  );
}

function MenuInput({ f, value, options, onChange }: { f: MenuField; value: string; options?: Option[]; onChange: (v: string) => void }) {
  const label = f.label + (f.optional ? " (optional)" : "");
  if (f.kind === "select") {
    const loading = !options;
    return (
      <Field label={label}>
        <select className="select" value={value} onChange={(e) => onChange(e.target.value)} disabled={loading}>
          {!f.options && <option value="">{loading ? "Loading…" : options.length ? "Choose…" : "None yet"}</option>}
          {(options ?? []).map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
      </Field>
    );
  }
  return (
    <Field label={label}>
      <input className="input" type={f.kind === "text" ? "text" : f.kind} value={value} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}
