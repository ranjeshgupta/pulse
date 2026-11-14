// Inspector: the right-hand panel that sets up the selected step, one editor per step type, plus
// Start (trigger, re-entry, goal, schedule).
import { useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { CHANNEL_NAMES, CHANNELS, type Channel, type ChannelRule, type JourneyGraph, type JourneyNode, type PropFilter, type Rule, type Segment, type SplitDecision, type SplitWinner, type Template } from "../../api";
import {
  DEVICE_TYPES, DNC_CHANNELS, FORM_FIELD_OPS, groupOf, MAX_JUMPS, nodeChannel, OUTCOME_WORDS, OUTCOMES, outputs, sendSteps, share,
  SLOT_RE, STEP_GROUPS, stepLabel, TRIGGER_ID, upstream, winnerProblem, type Outcome,
} from "../../journeyGraph";
import { itemForNode } from "../../journeyCatalog";
import { TemplateActions } from "../../messagePreview";
import { describeWindow, fromLocalInput, ScheduleFields, toLocalInput } from "../../schedule";
import { filtersFromWhere, OP_LABELS, problems as ruleProblems, rootFromRule, toRule, whereFromFilters, type AttrOp, type Where } from "../../segmentRules";
import { Field } from "../../ui";
import { DeadlineFields, ReachByChannel } from "../../campaignUI";
import { browserZone } from "../Broadcasts";
import { PersonalizeScopeProvider, TemplatedText, usePersonalizeScope, useVariables } from "../../VarPicker";
import { GroupEditor, PropertyFilters } from "../SegmentBuilder";
import type { Pickers } from "./data";
import { JIcon } from "./icons";
import { ProviderPick } from "../../ProviderPick";
import { TopicPick } from "../../TopicPick";
import {
  deviceRule, ownerRule, OS_CHOICES, parseDevice, parseOwner, parsePoints, parseStage, parseTags, pointsRule, POINT_OPS, stageRule, tagsRule,
  type PointsPreset,
} from "./presets";

type SetN = (p: Partial<JourneyNode>) => void;
type Win = { start_at?: string; end_at?: string };

export function Inspector({ graph, setGraph, win, setWin, selected, locked, decisions, pickers, templates, journeyId, numbers, canEditContacts, onDelete, onDeleteBranch }: {
  graph: JourneyGraph; setGraph: (g: JourneyGraph, key?: string) => void; selected: string | null; locked: boolean;
  decisions: Record<string, SplitDecision>; win: Win; setWin: (w: Win) => void; pickers: Pickers; templates: Template[];
  journeyId?: number; numbers: Record<string, number>; canEditContacts: boolean;
  onDelete: (id: string) => void; onDeleteBranch: (id: string) => void;
}) {
  if (!selected) return <p className="small muted">Select a step to set it up, or use a <strong>+</strong> on the canvas to add one.</p>;
  if (selected === TRIGGER_ID) return <StartFields graph={graph} setGraph={setGraph} win={win} setWin={setWin} locked={locked} segments={pickers.lists.segments} forms={pickers.lists.forms} />;
  const n = graph.nodes[selected];
  if (!n) return null;
  const setN: SetN = (patch) => setGraph({ ...graph, nodes: { ...graph.nodes, [selected]: { ...n, ...patch } } }, `node:${selected}`);
  const item = itemForNode(graph, n);
  const group = STEP_GROUPS.find((g) => g.group === groupOf(n.type))!;
  const branching = outputs(n).length > 1;
  return (
    <fieldset disabled={locked} className="insp">
      <div className={`insp-head c-${group.group}`}>
        <span className="insp-num" aria-label={`Step ${numbers[selected]}`}>{numbers[selected]}</span>
        {item && <span className="insp-icon"><JIcon name={item.icon} size={18} /></span>}
        <div>
          <h3 tabIndex={-1} id="inspector-title">{stepLabel(graph, n)}</h3>
          <p className="xs faint">{group.label} · step ID {selected}</p>
        </div>
      </div>
      {item && <p className="xs muted" style={{ margin: "0 0 12px" }}>{item.description}</p>}
      <PersonalizeScopeProvider value={{ event: triggerEvent(graph) }}>
        <StepFields id={selected} n={n} setN={setN} graph={graph} pickers={pickers} templates={templates} decisions={decisions} journeyId={journeyId} numbers={numbers} canEditContacts={canEditContacts} />
      </PersonalizeScopeProvider>
      <div className="row" style={{ marginTop: 14 }}>
        <button type="button" className="btn danger sm" onClick={() => onDelete(selected)}>Remove step</button>
        {branching && <button type="button" className="btn ghost sm" onClick={() => onDeleteBranch(selected)}>Remove with the steps after it</button>}
      </div>
      {outputs(n).length === 1 && <p className="xs faint" style={{ marginTop: 6 }}>Removing it joins the step before it to the step after it.</p>}
    </fieldset>
  );
}

function StepFields({ id, n, setN, graph, pickers, templates, decisions, journeyId, numbers, canEditContacts }: {
  id: string; n: JourneyNode; setN: SetN; graph: JourneyGraph; pickers: Pickers; templates: Template[];
  decisions: Record<string, SplitDecision>; journeyId?: number; numbers: Record<string, number>; canEditContacts: boolean;
}) {
  const L = pickers.lists;
  const ch = nodeChannel(n.type);
  const otherJourneys = L.journeys.filter((j) => j.id !== journeyId);
  const limit = <WaitField label="Take “No” if it hasn't happened within" seconds={n.seconds ?? 0} onChange={(seconds) => setN({ seconds })} />;
  switch (n.type) {
    // ---- waits ----
    case "wait": return <WaitField seconds={n.seconds ?? 0} onChange={(seconds) => setN({ seconds })} />;
    case "wait_until": return <WaitUntilFields n={n} setN={setN} />;
    // ---- decisions ----
    case "wait_for_event":
      return (<>
        <Field label="Take “Yes” when the contact does" hint="The event exactly as your app sends it, e.g. purchase."><input className="input" value={n.event ?? ""} placeholder="purchase" onChange={(e) => setN({ event: e.target.value.trim() })} /></Field>
        {limit}
      </>);
    case "message_decision": return <DecisionFields id={id} n={n} graph={graph} templates={templates} setN={setN} numbers={numbers} />;
    case "on_form":
      return (<>
        <Field label="Take “Yes” when the contact submits"><PickSelect value={n.form_id} items={L.forms} any="Any form" onChange={(form_id) => setN({ form_id })} /></Field>
        {L.forms.length === 0 && <Note>No forms yet. <Link to="/forms/new">Create one</Link></Note>}
        {limit}
      </>);
    case "on_asset":
      return (<>
        <Field label="Take “Yes” when the contact downloads"><PickSelect value={n.asset_id} items={L.assets} any="Any asset" onChange={(asset_id) => setN({ asset_id })} /></Field>
        {limit}
      </>);
    case "on_page": {
      const mode = n.page_id ? "page" : n.url !== undefined && n.url !== "" ? "url" : n.url === "" ? "url" : "any";
      return (<>
        <Field label="Take “Yes” when the contact visits">
          <select className="select" value={mode} onChange={(e) => setN(e.target.value === "page" ? { page_id: L.pages[0]?.id, url: undefined } : e.target.value === "url" ? { url: "", page_id: undefined } : { url: undefined, page_id: undefined })}>
            <option value="any">any page</option>
            <option value="url">a page whose URL contains…</option>
            <option value="page">one of your landing pages</option>
          </select>
        </Field>
        {mode === "url" && <Field label="URL contains" hint="e.g. /pricing. Matched anywhere in the address."><input className="input" value={n.url ?? ""} placeholder="/pricing" onChange={(e) => setN({ url: e.target.value })} /></Field>}
        {mode === "page" && <Field label="Landing page"><PickSelect value={n.page_id} items={L.pages} placeholder="Choose a landing page" onChange={(page_id) => setN({ page_id })} /></Field>}
        <Note>Needs the web SDK on your site (landing pages track themselves).</Note>
        {limit}
      </>);
    }
    case "on_device":
      return (<>
        <Field label="Device type">
          <select className="select" value={n.device_type ?? ""} onChange={(e) => setN({ device_type: e.target.value || undefined })}>
            <option value="">Any device type</option>
            {DEVICE_TYPES.map((d) => <option key={d} value={d}>{d}</option>)}
          </select>
        </Field>
        <Field label="Operating system" hint="Leave empty for any. Choose at least a device type or an OS.">
          <input className="input" list="j-os" value={n.os ?? ""} placeholder="Any OS" onChange={(e) => setN({ os: e.target.value.trim() || undefined })} />
          <datalist id="j-os">{OS_CHOICES.map((o) => <option key={o} value={o} />)}</datalist>
        </Field>
        {limit}
      </>);
    case "on_dwc":
      return (<>
        <SlotField value={n.slot ?? ""} slots={L.slots} onChange={(slot) => setN({ slot })} />
        <Note>Yes as soon as a page with this slot loads for the contact. Put <strong>Push dynamic content</strong> on Yes to give them their own content.</Note>
        {limit}
      </>);
    // ---- conditions ----
    case "condition":
      return (
        <Field label="Is the contact in this segment?" hint="Checked at the moment the contact reaches this step.">
          <PickSelect value={n.segment_id} items={L.segments} placeholder="Choose a segment" onChange={(segment_id) => setN({ segment_id })} />
        </Field>
      );
    case "check": return <CheckFields key={`${id}:${n.preset ?? "field"}`} n={n} setN={setN} pickers={pickers} />;
    case "if_journey":
      return (<>
        <Field label="Yes if the contact">
          <select className="select" value={n.journey_state ?? "active"} onChange={(e) => setN({ journey_state: e.target.value as JourneyNode["journey_state"] })}>
            <option value="active">is in any of these journeys now</option>
            <option value="completed">has finished any of these journeys</option>
            <option value="entered">has ever entered any of these journeys</option>
          </select>
        </Field>
        <CheckList label="Journeys" items={otherJourneys.map((j) => ({ value: j.id, label: j.name }))} value={n.journey_ids ?? []} onChange={(journey_ids) => setN({ journey_ids })} empty="No other journeys yet." max={20} />
      </>);
    case "if_form_field": {
      const form = pickers.lists.forms.find((f) => f.id === n.form_id) as ({ fields?: { key: string; label: string }[] } & { id: number }) | undefined;
      const valueless = n.op === "exists" || n.op === "not_exists";
      return (<>
        <Field label="Form" hint="The submission that brought the contact here, else their latest one.">
          <PickSelect value={n.form_id} items={L.forms} any="Any form (the latest submission)" onChange={(form_id) => setN({ form_id })} />
        </Field>
        <Field label="Field">
          <input className="input" list="j-form-fields" value={n.field ?? ""} placeholder="e.g. city" onChange={(e) => setN({ field: e.target.value.trim() })} />
          <datalist id="j-form-fields">{(form?.fields ?? []).map((f) => <option key={f.key} value={f.key}>{f.label}</option>)}</datalist>
        </Field>
        <div className="row" style={{ flexWrap: "nowrap", marginBottom: 12 }}>
          <select className="select" aria-label="Comparison" value={n.op ?? "eq"} onChange={(e) => setN({ op: e.target.value, value: e.target.value === "exists" || e.target.value === "not_exists" ? undefined : n.value ?? "" })}>
            {FORM_FIELD_OPS.map((o) => <option key={o} value={o}>{o === "exists" ? "was answered" : o === "not_exists" ? "wasn't answered" : OP_LABELS[o as AttrOp]}</option>)}
          </select>
          {!valueless && <input className="input" aria-label="Value" value={n.value ?? ""} onChange={(e) => setN({ value: e.target.value })} />}
        </div>
      </>);
    }
    case "if_notification":
      return (
        <Field label="Yes if the contact">
          <select className="select" value={n.notify ?? "any"} onChange={(e) => setN({ notify: e.target.value as JourneyNode["notify"] })}>
            <option value="push">has a device that can get push</option>
            <option value="inbox">has an unread, unexpired app inbox message</option>
            <option value="any">has either</option>
          </select>
        </Field>
      );
    case "if_valid_email":
      return (<>
        <Note>Yes if the address is well formed and its domain accepts mail (MX or A record).</Note>
        <label className="check"><input type="checkbox" checked={!!n.reject_disposable} onChange={(e) => setN({ reject_disposable: e.target.checked || undefined })} />Treat disposable addresses (e.g. mailinator) as invalid</label>
      </>);
    case "if_dnc":
      return (<>
        <Field label="Yes if the contact can't be contacted on">
          <select className="select" value={n.channel ?? "any"} onChange={(e) => setN({ channel: e.target.value })}>
            <option value="any">any channel (unsubscribed from everything)</option>
            {DNC_CHANNELS.map((c) => <option key={c} value={c}>{CHANNEL_NAMES[c]}</option>)}
          </select>
        </Field>
        <Note>Counts unsubscribes, and bounces for email and SMS.</Note>
      </>);
    case "if_before":
      return (<>
        <Field label="Yes until" hint="After this date contacts take No. Put the time-limited action on Yes.">
          <input className="input" type="datetime-local" value={toLocalInput(n.until)} onChange={(e) => setN({ until: fromLocalInput(e.target.value) || undefined })} />
        </Field>
      </>);
    case "if_visited":
      return (<>
        <Field label="URL contains" hint="Leave empty for any page."><input className="input" value={n.url ?? ""} placeholder="/pricing" onChange={(e) => setN({ url: e.target.value })} /></Field>
        <Field label="In the last" hint="0 looks at their whole history.">
          <div className="row"><input className="input" style={{ width: 90 }} type="number" min={0} max={365} value={n.days ?? 0} onChange={(e) => setN({ days: Math.trunc(Number(e.target.value)) })} /><span className="small muted">days</span></div>
        </Field>
      </>);
    // ---- splits ----
    case "split": return <SplitFields id={id} n={n} setN={setN} graph={graph} decision={decisions[id]} />;
    // ---- actions ----
    case "notify_user":
      return (<>
        <TemplatePick channel="email" label="Email template" templates={templates} value={n.template_id} onChange={(template_id) => setN({ template_id })} />
        <Note>Sent to your team with this contact's details filled in. Not a marketing email: unsubscribes don't apply.</Note>
        <label className="check"><input type="checkbox" checked={!!n.to_owner} onChange={(e) => setN({ to_owner: e.target.checked || undefined })} />The contact's owner</label>
        <CheckList label="Teammates" items={L.users.map((u) => ({ value: u.id, label: u.name }))} value={n.user_ids ?? []} onChange={(user_ids) => setN({ user_ids: user_ids.length ? user_ids : undefined })} empty="No teammates to list (or you can't view users)." max={20} />
        <Field label="Other addresses" hint="Separate with commas; up to 20.">
          <ListInput value={n.emails ?? []} placeholder="sales@example.com" onChange={(emails) => setN({ emails: emails.length ? emails : undefined })} />
        </Field>
      </>);
    case "send_message": return (<>
      <ChannelRules n={n} setN={setN} templates={templates} event={triggerEvent(graph)} />
      <DeadlineFields value={n.deadline} onChange={(deadline) => setN({ deadline: deadline ?? undefined })} zone={browserZone()} />
    </>);
    case "show_focus":
      return (<>
        <Field label="Focus item"><PickSelect value={n.focus_id} items={L.focus} placeholder="Choose a focus item" onChange={(focus_id) => setN({ focus_id })} /></Field>
        <Note>Shown on the contact's next page view, whatever the item's own targeting. {L.focus.length === 0 && <Link to="/on-site/focus/new">Create a focus item</Link>}</Note>
      </>);
    case "push_integration":
      return (<>
        <Field label="CRM"><PickSelect value={n.integration_id} items={L.integrations} placeholder="Choose a CRM" onChange={(integration_id) => setN({ integration_id })} /></Field>
        <Note>Fields go through the integration's field mapping; a failed push is recorded as <code>integration_push_failed</code> and the journey carries on. {L.integrations.length === 0 && <Link to="/settings/integrations">Connect HubSpot or Salesforce</Link>}</Note>
      </>);
    case "push_dwc":
      return (<>
        <SlotField value={n.slot ?? ""} slots={L.slots} onChange={(slot) => setN({ slot })} />
        <Field label="Content (HTML)" hint="Replaces the slot's default for this contact. Up to 64 KB. Variables are filled when the step runs (values escaped).">
          <TemplatedText className="textarea code" label="Content (HTML)" value={n.html ?? ""} onChange={(html) => setN({ html })} />
        </Field>
        <Field label="Keep it for">
          <div className="row"><input className="input" style={{ width: 90 }} type="number" min={1} max={365} value={n.days ?? 30} onChange={(e) => setN({ days: Math.trunc(Number(e.target.value)) || undefined })} /><span className="small muted">days</span></div>
        </Field>
      </>);
    case "update_attr": return <AttrFields attrs={n.attrs ?? {}} onChange={(attrs) => setN({ attrs })} />;
    case "update_owner":
      return (
        <Field label="New owner">
          <select className="select" value={n.owner_id === undefined ? "" : String(n.owner_id)} onChange={(e) => setN({ owner_id: e.target.value === "" ? undefined : Number(e.target.value) })}>
            <option value="">Choose a teammate</option>
            <option value="0">No one (unassign)</option>
            {L.users.map((u) => <option key={u.id} value={u.id}>{u.name}</option>)}
            {n.owner_id && !L.users.some((u) => u.id === n.owner_id) ? <option value={n.owner_id}>User {n.owner_id}</option> : null}
          </select>
        </Field>
      );
    case "update_company": return <CompanyAttrFields value={n.company_attrs ?? {}} onChange={(company_attrs) => setN({ company_attrs })} />;
    case "add_company":
      return (<>
        <Field label="Company"><PickSelect value={n.company_id} items={L.companies} placeholder="Choose a company" onChange={(company_id) => setN({ company_id })} /></Field>
        <Note>A contact belongs to one company, so this also replaces their current one. The company's fields are copied to the contact as company_….</Note>
      </>);
    case "company_points": case "adjust_points":
      return (
        <Field label="Points" hint={n.type === "company_points" ? "Added to the company's score; contacts see it as company_score." : "Use a negative number to take points away. Scoring rules add points for events on their own."}>
          <input className="input" type="number" step={1} value={n.points ?? 0} onChange={(e) => setN({ points: Math.trunc(Number(e.target.value)) })} />
        </Field>
      );
    case "change_stage": return <StageField value={n.stage ?? ""} stages={L.stages} onChange={(stage) => setN({ stage: stage || undefined })} />;
    case "update_tags":
      return (<>
        <Field label="Add tags" hint="Separate with commas, e.g. lead, webinar-2026."><ListInput key={`${id}-add`} value={n.add_tags ?? []} placeholder="lead, webinar" onChange={(add_tags) => setN({ add_tags })} /></Field>
        <Field label="Remove tags"><ListInput key={`${id}-rm`} value={n.remove_tags ?? []} placeholder="trial" onChange={(remove_tags) => setN({ remove_tags })} /></Field>
      </>);
    case "update_list": {
      // Older steps hold one list and an action; editing turns them into add/remove lists.
      const add = n.list_id && n.list_action !== "remove" ? [n.list_id] : n.add_lists ?? [];
      const rm = n.list_id && n.list_action === "remove" ? [n.list_id] : n.remove_lists ?? [];
      const set = (a: number[], r: number[]) => setN({ list_id: undefined, list_action: undefined, add_lists: a, remove_lists: r.length ? r : undefined });
      const items = L.staticLists.map((s) => ({ value: s.id, label: s.name }));
      return (<>
        <CheckList label="Add to static segments" items={items} value={add} onChange={(a) => set(a, rm.filter((x) => !a.includes(x)))} empty="No static segments yet." />
        <CheckList label="Remove from static segments" items={items} value={rm} onChange={(r) => set(add.filter((x) => !r.includes(x)), r)} empty="" />
        <Note>Dynamic segments follow their rules and can't take members, so only static segments are listed. {L.staticLists.length === 0 && <Link to="/segments?type=static">Create a static segment</Link>}</Note>
      </>);
    }
    case "change_journey": {
      const add = n.add_journeys ?? [], rm = n.remove_journeys ?? [];
      const items = otherJourneys.map((j) => ({ value: j.id, label: j.name }));
      return (<>
        <CheckList label="Add the contact to" items={items} value={add} onChange={(a) => setN({ add_journeys: a, remove_journeys: rm.filter((x) => !a.includes(x)) })} empty="No other journeys yet." max={20} />
        <CheckList label="Remove the contact from" items={items} value={rm} onChange={(r) => setN({ remove_journeys: r, add_journeys: add.filter((x) => !r.includes(x)) })} empty="" max={20} />
        <Note>Adding enrolls them at that journey's start (if its re-entry rules allow); removing ends their place in it.</Note>
      </>);
    }
    case "webhook":
      return (<>
        <Field label="POST to" hint="Must be a public https:// address. Pulse sends the contact as JSON and retries briefly; a failure is recorded and the journey continues."><input className="input" value={n.url ?? ""} placeholder="https://api.example.com/hooks/pulse" onChange={(e) => setN({ url: e.target.value.trim() })} /></Field>
        <Field label="Body (optional)" hint={'Leave empty to send {"contact": …}. Or write JSON with variables; | json makes a value safe in JSON: {"name": {{contact.first_name | json}}, "cart": {{event.cart_id | json}}}. {{json .Attrs}} still works.'}>
          <TemplatedText className="textarea code" label="Body" value={n.body ?? ""} onChange={(body) => setN({ body: body || undefined })} />
        </Field>
      </>);
    case "jump": {
      const options = Object.keys(graph.nodes).filter((k) => k !== id && graph.nodes[k].type !== "jump").sort((a, b) => numbers[a] - numbers[b]);
      return (<>
        <Field label="Continue from" hint={`Contacts go on from that step. After ${MAX_JUMPS} jumps a contact leaves the journey, so loops always end.`}>
          <select className="select" value={n.target ?? ""} onChange={(e) => setN({ target: e.target.value || undefined })}>
            <option value="">Choose a step</option>
            {options.map((k) => <option key={k} value={k}>Step {numbers[k]} · {stepLabel(graph, graph.nodes[k])}</option>)}
          </select>
        </Field>
        <Note>The dashed line on the canvas shows where it goes.</Note>
      </>);
    }
    case "dnc_add": case "dnc_remove":
      return (<>
        <Field label={n.type === "dnc_add" ? "Stop contacting them on" : "Allow again"}>
          <select className="select" value={n.channel ?? "email"} onChange={(e) => setN({ channel: e.target.value })}>
            {DNC_CHANNELS.map((c) => <option key={c} value={c}>{CHANNEL_NAMES[c]}</option>)}
            <option value="all">every channel</option>
          </select>
        </Field>
        {n.type === "dnc_add" && <Field label="Reason (optional)" hint="Shown on the contact. Up to 200 characters."><input className="input" maxLength={200} value={n.reason ?? ""} onChange={(e) => setN({ reason: e.target.value || undefined })} /></Field>}
        {n.type === "dnc_remove" && <Note>Only use this when the contact asked to hear from you again.</Note>}
      </>);
    case "delete_contact":
      return (<>
        <div className="alert error" role="alert"><strong>Permanently deletes the contact and all their data. This can't be undone.</strong></div>
        {!canEditContacts && <Note>You need edit access to contacts to set this up.</Note>}
        <Field label="Type DELETE to confirm" hint="Every contact who reaches this step is deleted.">
          <input className="input" value={n.confirm ?? ""} aria-label="Type DELETE to confirm" onChange={(e) => setN({ confirm: e.target.value || undefined })} disabled={!canEditContacts} />
        </Field>
      </>);
    case "exit": return <Note>Contacts who reach this step finish the journey.</Note>;
  }
  if (ch) {
    const smsTemplates = templates.filter((t) => t.channel === "sms");
    return (<>
      <TemplatePick channel={ch} label={`${CHANNEL_NAMES[ch]} template`} templates={templates} value={n.template_id} onChange={(template_id) => setN({ template_id })} />
      {(ch === "whatsapp" || ch === "rcs") && (
        <Field label="If it can't be delivered, send this SMS instead" hint={`Used when the number isn't on ${CHANNEL_NAMES[ch]} or delivery fails. Sent at most once.`}>
          <select className="select" value={n.fallback_template_id ?? ""} onChange={(e) => setN({ fallback_template_id: Number(e.target.value) || undefined })}>
            <option value="">No fallback</option>
            {smsTemplates.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        </Field>
      )}
      <ProviderPick channel={ch} value={n.provider_id} onChange={(id) => setN({ provider_id: id || undefined })} />
      <TopicPick value={n.topic} onChange={(topic) => setN({ topic: topic || undefined })} />
      <DeadlineFields value={n.deadline} onChange={(deadline) => setN({ deadline: deadline ?? undefined })} zone={browserZone()} />
    </>);
  }
  return <Note>This step type isn't known to this version of Pulse.</Note>;
}

const Note = ({ children }: { children: ReactNode }) => <p className="xs muted" style={{ marginTop: -4, marginBottom: 12 }}>{children}</p>;

function PickSelect({ value, items, onChange, any, placeholder }: { value?: number; items: { id: number; name: string }[]; onChange: (v?: number) => void; any?: string; placeholder?: string }) {
  return (
    <select className="select" value={value ?? ""} onChange={(e) => onChange(Number(e.target.value) || undefined)}>
      <option value="">{any ?? placeholder ?? "Choose"}</option>
      {items.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
      {value && !items.some((x) => x.id === value) ? <option value={value}>#{value}</option> : null}
    </select>
  );
}

function CheckList<T extends string | number>({ label, items, value, onChange, empty, max }: { label: string; items: { value: T; label: string }[]; value: T[]; onChange: (v: T[]) => void; empty: string; max?: number }) {
  return (
    <div className="field" role="group" aria-label={label}>
      <span>{label}{max ? <small className="faint"> · up to {max}</small> : null}</span>
      {items.length === 0 ? (empty && <small className="muted">{empty}</small>) : (
        <div className="j-checklist">
          {items.map((it) => (
            <label key={String(it.value)} className="check">
              <input type="checkbox" checked={value.includes(it.value)} disabled={!value.includes(it.value) && max !== undefined && value.length >= max}
                onChange={(e) => onChange(e.target.checked ? [...value, it.value] : value.filter((x) => x !== it.value))} />
              {it.label}
            </label>
          ))}
        </div>
      )}
    </div>
  );
}

// ListInput edits a list as comma-separated text, keeping what's typed until it parses.
function ListInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const [text, setText] = useState(value.join(", "));
  return (
    <input className="input" value={text} placeholder={placeholder} onChange={(e) => {
      setText(e.target.value);
      onChange([...new Set(e.target.value.split(",").map((t) => t.trim()).filter(Boolean))]);
    }} />
  );
}

function TemplatePick({ channel, label, templates, value, onChange }: { channel: Channel; label: string; templates: Template[]; value?: number; onChange: (v?: number) => void }) {
  const mine = templates.filter((t) => t.channel === channel);
  return (<>
    <Field label={label}>
      <select className="select" value={value ?? ""} onChange={(e) => onChange(Number(e.target.value) || undefined)}>
        <option value="">Choose a template</option>
        {mine.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
      </select>
    </Field>
    <TemplateActions templateId={value} event={usePersonalizeScope().event} />
    {mine.length === 0 && <Note>No {CHANNEL_NAMES[channel]} templates yet. <Link to={`/templates/new?channel=${channel}`}>Create one</Link></Note>}
  </>);
}

function SlotField({ value, slots, onChange }: { value: string; slots: { slot: string; name: string }[]; onChange: (s: string) => void }) {
  return (
    <Field label="Dynamic content slot" hint={value && !SLOT_RE.test(value) ? "Lowercase letters, digits, - and _." : "The slot name your page asks for, e.g. hero-banner."}>
      <input className="input" list="j-slots" value={value} placeholder="hero-banner" onChange={(e) => onChange(e.target.value.trim())} />
      <datalist id="j-slots">{slots.map((s) => <option key={s.slot} value={s.slot}>{s.name}</option>)}</datalist>
    </Field>
  );
}

// ChannelRules edits a "Send marketing message": rules in order, each "when <conditions> → channel and
// template". The first rule whose conditions match and whose channel the contact can receive sends;
// a rule without conditions always matches (put it last as "Otherwise").
function ChannelRules({ n, setN, templates, event }: { n: JourneyNode; setN: SetN; templates: Template[]; event?: string }) {
  const cs = n.channels ?? [];
  const set = (next: ChannelRule[]) => setN({ channels: next });
  const setAt = (i: number, patch: Partial<ChannelRule>) => set(cs.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  const move = (i: number, d: -1 | 1) => { const c = [...cs]; [c[i], c[i + d]] = [c[i + d], c[i]]; set(c); };
  const vars = useVariables();
  const paths = [...vars.contact.map((v) => v.var), ...(vars.event ?? []).map((v) => v.var.replace(/\.0\./, "[]."))].filter((p, i, a) => a.indexOf(p) === i);
  return (
    <div className="field">
      <span>Send on the first rule that matches</span>
      <Note>Rules are checked in order. A rule applies when its conditions match and the contact can receive the channel (address, consent); otherwise the next rule is tried. A rule without conditions always applies.</Note>
      <ol className="j-rules">
        {cs.map((c, i) => (
          <li key={i} className="j-rule">
            <div className="row" style={{ justifyContent: "space-between", flexWrap: "nowrap" }}>
              <strong className="small">{c.when?.length ? `Rule ${i + 1}: when` : i === cs.length - 1 && i > 0 ? "Otherwise" : `Rule ${i + 1}: always`}</strong>
              <span className="row" style={{ gap: 2, flexWrap: "nowrap" }}>
                <button type="button" className="btn ghost sm" aria-label={`Move rule ${i + 1} up`} disabled={i === 0} onClick={() => move(i, -1)}>↑</button>
                <button type="button" className="btn ghost sm" aria-label={`Move rule ${i + 1} down`} disabled={i === cs.length - 1} onClick={() => move(i, 1)}>↓</button>
                <button type="button" className="btn ghost sm" aria-label={`Remove rule ${i + 1}`} disabled={cs.length === 1} onClick={() => set(cs.filter((_, j) => j !== i))}>×</button>
              </span>
            </div>
            <RuleConditions when={c.when ?? []} paths={paths} onChange={(when) => setAt(i, { when: when.length ? when : undefined })} />
            <div className="j-cond-line j-send">
              <span className="small">send</span>
              <select className="select" aria-label={`Rule ${i + 1} channel`} value={c.channel} onChange={(e) => setAt(i, { channel: e.target.value as Channel, template_id: undefined, fallback_template_id: undefined, provider_id: undefined })}>
                {CHANNELS.map((ch) => <option key={ch} value={ch}>{CHANNEL_NAMES[ch]}</option>)}
              </select>
            </div>
            <select className="select j-full" aria-label={`Rule ${i + 1} template`} value={c.template_id ?? ""} onChange={(e) => setAt(i, { template_id: Number(e.target.value) || undefined })}>
              <option value="">Choose a template</option>
              {templates.filter((t) => t.channel === c.channel).map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
            </select>
            {(c.channel === "whatsapp" || c.channel === "rcs") && (
              <label className="j-fallback">
                <span className="xs muted">SMS if it can't be delivered</span>
                <select className="select j-full" aria-label={`Rule ${i + 1} SMS fallback`} value={c.fallback_template_id ?? ""} onChange={(e) => setAt(i, { fallback_template_id: Number(e.target.value) || undefined })}>
                  <option value="">No fallback</option>
                  {templates.filter((t) => t.channel === "sms").map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
                </select>
              </label>
            )}
            <ProviderPick compact channel={c.channel} value={c.provider_id} onChange={(id) => setAt(i, { provider_id: id || undefined })} />
            {c.template_id && <TemplateActions templateId={c.template_id} event={event} />}
          </li>
        ))}
      </ol>
      {cs.length < 10 && (
        <div className="row">
          <button type="button" className="btn sm" onClick={() => set([...cs, { channel: "sms", when: [{ prop: "contact.phone_country", op: "eq", value: "IN" }] }])}>+ Rule with a condition</button>
          <button type="button" className="btn sm ghost" onClick={() => set([...cs, { channel: "whatsapp" }])}>+ Otherwise</button>
        </div>
      )}
      <Note>Nobody matches: the contact goes on without a message, recorded as <code>message_skipped</code> with the reason. Step stats show which channel each contact got.</Note>
      <TopicPick value={n.topic} onChange={(topic) => setN({ topic: topic || undefined })} />
    </div>
  );
}

const RULE_OPS: { op: string; label: string; needs: boolean }[] = [
  { op: "eq", label: "is", needs: true }, { op: "ne", label: "is not", needs: true },
  { op: "contains", label: "contains", needs: true }, { op: "starts_with", label: "starts with", needs: true },
  { op: "gt", label: "is more than", needs: true }, { op: "lt", label: "is less than", needs: true },
  { op: "exists", label: "is set", needs: false }, { op: "not_exists", label: "is not set", needs: false },
];

// RuleConditions edits a rule's conditions on contact.<field> and event.<property> (all must match).
function RuleConditions({ when, paths, onChange }: { when: PropFilter[]; paths: string[]; onChange: (w: PropFilter[]) => void }) {
  const list = `rule-paths-${paths.length}`;
  const setAt = (i: number, patch: Partial<PropFilter>) => onChange(when.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  return (
    <div className="j-rule-when">
      <datalist id={list}>{paths.map((p) => <option key={p} value={p} />)}</datalist>
      {when.map((w, i) => {
        const op = RULE_OPS.find((o) => o.op === w.op) ?? RULE_OPS[0];
        return (
          <div className="j-cond" key={i}>
            {i > 0 && <span className="xs muted j-cond-and">and</span>}
            <div className="j-cond-line">
              <input className="input" list={list} value={w.prop} placeholder="contact.phone_country" aria-label="Field or property" onChange={(e) => setAt(i, { prop: e.target.value.trim() })} />
              <button type="button" className="btn ghost sm" aria-label="Remove condition" onClick={() => onChange(when.filter((_, j) => j !== i))}>×</button>
            </div>
            <div className="j-cond-line">
              <select className="select" value={w.op} aria-label="Comparison" onChange={(e) => setAt(i, { op: e.target.value, value: RULE_OPS.find((o) => o.op === e.target.value)?.needs ? w.value ?? "" : undefined })}>
                {RULE_OPS.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
              </select>
              {op.needs && <input className="input" value={String(w.value ?? "")} aria-label="Value" placeholder="IN"
                onChange={(e) => setAt(i, { value: (w.op === "gt" || w.op === "lt") && e.target.value !== "" && !Number.isNaN(Number(e.target.value)) ? Number(e.target.value) : e.target.value })} />}
            </div>
          </div>
        );
      })}
      {when.length < 5 && <button type="button" className="btn ghost sm" style={{ marginTop: 4 }} onClick={() => onChange([...when, { prop: "contact.phone_country", op: "eq", value: "" }])}>+ Condition</button>}
    </div>
  );
}

function CompanyAttrFields({ value, onChange }: { value: Record<string, string | number | boolean>; onChange: (v: Record<string, string | number | boolean>) => void }) {
  const rows = Object.entries(value);
  const kind = (v: unknown) => (typeof v === "number" ? "number" : typeof v === "boolean" ? "bool" : "text");
  const cast = (k: string, raw: string) => (k === "number" ? Number(raw) || 0 : k === "bool" ? raw === "true" : raw);
  const setRow = (i: number, k: string, v: string | number | boolean) => onChange(Object.fromEntries(rows.map(([kk, vv], j) => (j === i ? [k, v] : [kk, vv]))));
  return (
    <div className="field">
      <span>Set these company fields</span>
      {rows.map(([k, v], i) => (
        <div className="row" key={i} style={{ flexWrap: "nowrap" }}>
          <input className="input" placeholder="field" value={k} aria-label="Company field" onChange={(e) => setRow(i, e.target.value.trim(), v)} />
          <select className="select inline" aria-label="Type" value={kind(v)} onChange={(e) => setRow(i, k, cast(e.target.value, String(v)))}>
            <option value="text">text</option><option value="number">number</option><option value="bool">yes/no</option>
          </select>
          {kind(v) === "bool" ? (
            <select className="select inline" aria-label="Value" value={String(v)} onChange={(e) => setRow(i, k, e.target.value === "true")}><option value="true">yes</option><option value="false">no</option></select>
          ) : (
            <input className="input" placeholder="value" aria-label="Value" type={kind(v) === "number" ? "number" : "text"} value={String(v)} onChange={(e) => setRow(i, k, cast(kind(v), e.target.value))} />
          )}
          <button type="button" className="btn ghost sm" aria-label="Remove field" onClick={() => onChange(Object.fromEntries(rows.filter((_, j) => j !== i)))}>×</button>
        </div>
      ))}
      <div><button type="button" className="btn sm" onClick={() => onChange({ ...value, [`field_${rows.length + 1}`]: "" })}>Add field</button></div>
      <small>Letters, digits and _ (not id or name). Skipped for contacts without a company.</small>
    </div>
  );
}

// ---- Start ----

function StartFields({ graph, setGraph, win, setWin, locked, segments, forms }: { graph: JourneyGraph; setGraph: (g: JourneyGraph, key?: string) => void; win: Win; setWin: (w: Win) => void; locked: boolean; segments: Segment[]; forms: Pickers["lists"]["forms"] }) {
  const t = graph.trigger;
  const setT = (patch: Partial<JourneyGraph["trigger"]>) => setGraph({ ...graph, trigger: { ...t, ...patch } }, "trigger");
  return (
    <fieldset disabled={locked} className="insp">
      <div className="insp-head c-start">
        <span className="insp-icon"><JIcon name="play" size={18} /></span>
        <div><h3 tabIndex={-1} id="inspector-title">Start</h3><p className="xs faint">{describeWindow(win.start_at, win.end_at)}</p></div>
      </div>
      <Field label="Start this campaign for a contact when they" hint={t.type === "segment_entered" ? "This makes it an Automation campaign." : "This makes it a Triggered campaign."}>
        <select className="select" value={t.type} onChange={(e) => setT({ type: e.target.value as JourneyGraph["trigger"]["type"] })}>
          <option value="segment_entered">join a segment</option>
          <option value="event">do something (an event)</option>
          <option value="form_submitted">submit a form</option>
        </select>
      </Field>
      {t.type === "segment_entered" ? (
        <>
          <Field label="Segment">
            <select className="select" value={t.segment_id ?? ""} onChange={(e) => setT({ segment_id: Number(e.target.value) || undefined })}>
              <option value="">Choose a segment</option>
              {segments.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </Field>
          <label className="check"><input type="checkbox" checked={!!t.include_backfill} onChange={(e) => setT({ include_backfill: e.target.checked })} />Also start it for contacts added when the segment is rebuilt</label>
          <p className="xs muted">Off: only people who newly join the segment from now on start. On: when Pulse rebuilds the segment (after you edit its filters, or its daily refresh), everyone added starts too, which can be many people at once.</p>
          {!!t.segment_id && <ReachByChannel segmentId={t.segment_id} channels={journeyChannels(graph)} title="Members it can reach now" />}
        </>
      ) : t.type === "form_submitted" ? (
        <>
          <Field label="Form" hint="Each submission starts the journey for the contact who sent it; form field conditions can then check what they entered.">
            <PickSelect value={t.form_id} items={forms} any="Any form" onChange={(form_id) => setT({ form_id })} />
          </Field>
          {forms.length === 0 && <Note>No forms yet. <Link to="/forms/new">Create one</Link></Note>}
        </>
      ) : (
        <>
          <Field label="Event name" hint="Exactly as your app sends it, e.g. signup or cart_abandoned.">
            <input className="input" value={t.event ?? ""} onChange={(e) => setT({ event: e.target.value.trim() })} />
          </Field>
          <TriggerFilters event={t.event ?? ""} filters={t.where} onChange={(where) => setT({ where: where.length ? where : undefined })} />
        </>
      )}
      <h4 className="small" style={{ margin: "18px 0 8px" }}>Entering again</h4>
      <Field label="A contact who has been through this journey">
        <select className="select" value={t.reentry ?? "once"} onChange={(e) => setT({ reentry: e.target.value as "once" | "always" | "after_days", days: e.target.value === "after_days" ? t.days ?? 30 : undefined })}>
          <option value="once">never enters again</option>
          <option value="always">can enter again after finishing</option>
          <option value="after_days">can enter again after some days</option>
        </select>
      </Field>
      {t.reentry === "after_days" && (
        <Field label="Days since they last entered"><input className="input" type="number" min={1} value={t.days ?? 30} onChange={(e) => setT({ days: Number(e.target.value) })} /></Field>
      )}
      <h4 className="small" style={{ margin: "18px 0 8px" }}>Goal</h4>
      <label className="check"><input type="checkbox" checked={!!graph.goal} onChange={(e) => setGraph({ ...graph, goal: e.target.checked ? { event: "purchase" } : null })} />End the journey early for contacts who reach a goal</label>
      {graph.goal && (
        <Field label="Goal event" hint="When a contact in the journey does this, they leave it and count as converted.">
          <input className="input" value={graph.goal.event} onChange={(e) => setGraph({ ...graph, goal: { event: e.target.value.trim() } }, "goal")} />
        </Field>
      )}
      <h4 className="small" style={{ margin: "18px 0 8px" }}>Schedule</h4>
      <ScheduleFields stacked start={win.start_at} end={win.end_at} onChange={setWin} startLabel="Contacts can enter from" />
      <p className="xs muted">Contacts already inside keep going after the end.</p>
    </fieldset>
  );
}

// TriggerFilters: property conditions an event must meet to start the journey ("only when
// payment_method is upi"). The edited rows stay local so half-typed values aren't lost.
function TriggerFilters({ event, filters, onChange }: { event: string; filters?: PropFilter[]; onChange: (f: PropFilter[]) => void }) {
  const [where, setWhere] = useState<Where[]>(() => whereFromFilters(filters));
  const issues = where.length && event ? ruleProblems({ kind: "event", event, op: "gte", count: 1, days: 1, where }).filter((p) => !p.startsWith("Enter an event")) : [];
  const set = (w: Where[]) => { setWhere(w); onChange(filtersFromWhere(w)); };
  return (
    <div className="insp-where">
      <PropertyFilters event={event} where={where} onChange={set} />
      {issues.length > 0 && <ul className="xs" style={{ color: "var(--danger)", margin: "6px 0 0" }}>{[...new Set(issues)].map((p) => <li key={p}>{p}</li>)}</ul>}
      <p className="xs muted">Leave empty to start on every {event || "event"}. Paths reach nested properties: product.code, or items[].code for any item.</p>
    </div>
  );
}

// ---- message decisions ----

// DecisionFields sets up a message decision: which earlier message, what should happen to it,
// and how long to wait before taking the "No" path.
function DecisionFields({ id, n, graph, templates, setN, numbers }: { id: string; n: JourneyNode; graph: JourneyGraph; templates: Template[]; setN: SetN; numbers: Record<string, number> }) {
  const sends = sendSteps(graph);
  const before = upstream(graph, id);
  const name = (sid: string, s: JourneyNode, ch: Channel) => {
    const tpl = templates.find((t) => t.id === s.template_id);
    return `Step ${numbers[sid]} · ${CHANNEL_NAMES[ch]} ${tpl ? `‘${tpl.name}’` : "(no template yet)"}`;
  };
  const earlier = sends.filter(([sid]) => before.has(sid));
  const step = n.step ? sends.find(([sid]) => sid === n.step) : undefined;
  const ch = step?.[2];
  const allowed = ch ? OUTCOMES[ch] : [];
  const pickStep = (sid: string) => {
    const c = sends.find(([x]) => x === sid)?.[2];
    const keep = c && n.outcome && OUTCOMES[c].includes(n.outcome);
    const outcome = keep ? n.outcome : c ? OUTCOMES[c][0] : undefined;
    setN({ step: sid || undefined, outcome, url: outcome === "clicked" ? n.url : undefined });
  };
  return (
    <>
      <Note>Contacts wait here. They take “Yes” the moment their message does what you choose below, or “No” once the time limit passes.</Note>
      <Field label="Which message?" hint="Only messages sent earlier on this path can be watched.">
        <select className="select" value={n.step ?? ""} onChange={(e) => pickStep(e.target.value)}>
          <option value="">Choose a send step</option>
          {earlier.map(([sid, s, c]) => <option key={sid} value={sid}>{name(sid, s, c)}</option>)}
          {step && !before.has(step[0]) && <option value={step[0]}>{name(step[0], step[1], step[2])} (not before this step)</option>}
        </select>
      </Field>
      {earlier.length === 0 && <Note>Add a send step above this decision first.</Note>}
      <Field label="Take “Yes” if the message">
        <select className="select" value={n.outcome ?? ""} disabled={!ch} onChange={(e) => {
          const outcome = (e.target.value || undefined) as Outcome | undefined;
          setN({ outcome, url: outcome === "clicked" ? n.url : undefined });
        }}>
          {!ch && <option value="">Choose a message first</option>}
          {allowed.map((o) => <option key={o} value={o}>{OUTCOME_WORDS[o].long}</option>)}
        </select>
      </Field>
      {ch === "push" && n.outcome === "clicked" && <Note>Clicks are reported by the web SDK&rsquo;s service worker and the app SDKs when the notification is tapped.</Note>}
      {n.outcome === "clicked" && ch !== "push" && (
        <Field label="Link contains (optional)" hint="Leave empty for any link, e.g. /pricing for that page only.">
          <input className="input" value={n.url ?? ""} placeholder="any link" onChange={(e) => setN({ url: e.target.value || undefined })} />
        </Field>
      )}
      {ch === "email" && n.outcome === "opened" && <Note>Some mail apps hide opens, so "a link is clicked" is the surer signal.</Note>}
      {ch === "email" && n.outcome === "replied" && <Note>Needs a reply tracking address under Channels, Delivery rules.</Note>}
      {ch === "rcs" && n.outcome === "replied" && <Note>Any RCS message from the contact (typed, or a tapped suggestion) within 7 days counts as a reply to the latest RCS message they got. Needs the RCS provider's webhook (client token) set up.</Note>}
      <WaitField label="Take “No” if it hasn't happened within" seconds={n.seconds ?? 0} onChange={(seconds) => setN({ seconds })} />
    </>
  );
}

const UNITS = [{ s: 60, label: "minutes" }, { s: 3600, label: "hours" }, { s: 86400, label: "days" }, { s: 604800, label: "weeks" }];

export function WaitField({ seconds, onChange, label = "Wait for" }: { seconds: number; onChange: (s: number) => void; label?: string }) {
  const unit = [...UNITS].reverse().find((u) => seconds > 0 && seconds % u.s === 0) ?? UNITS[2];
  return (
    <Field label={label}>
      <div className="row">
        <input className="input" style={{ width: 90 }} type="number" min={1} value={seconds / unit.s || ""} aria-label={`${label} (amount)`} onChange={(e) => onChange(Math.round(Number(e.target.value) * unit.s))} />
        <select className="select inline" value={unit.s} aria-label={`${label} (unit)`} onChange={(e) => onChange((seconds / unit.s || 1) * Number(e.target.value))}>
          {UNITS.map((u) => <option key={u.s} value={u.s}>{u.label}</option>)}
        </select>
      </div>
    </Field>
  );
}

const ZONES = ["Asia/Kolkata", "Asia/Dubai", "Asia/Singapore", "Europe/London", "America/New_York", "America/Los_Angeles", "UTC"];

function WaitUntilFields({ n, setN }: { n: JourneyNode; setN: SetN }) {
  const mode = n.until ? "date" : "time";
  return (
    <>
      <Field label="Continue">
        <select className="select" value={mode} onChange={(e) => setN(e.target.value === "date" ? { until: new Date(Date.now() + 86400000).toISOString(), time_of_day: undefined, timezone: undefined } : { until: undefined, time_of_day: "09:00", timezone: "Asia/Kolkata" })}>
          <option value="time">at the next time of day</option>
          <option value="date">at a specific date and time</option>
        </select>
      </Field>
      {mode === "time" ? (
        <>
          <Field label="Time of day"><input className="input" type="time" value={n.time_of_day ?? "09:00"} onChange={(e) => setN({ time_of_day: e.target.value })} /></Field>
          <Field label="In this time zone" hint={'A contact with a "timezone" attribute (e.g. Europe/London) gets their own local time.'}>
            <select className="select" value={n.timezone ?? "Asia/Kolkata"} onChange={(e) => setN({ timezone: e.target.value })}>{ZONES.map((z) => <option key={z}>{z}</option>)}</select>
          </Field>
        </>
      ) : (
        <Field label="Date and time" hint="Contacts who arrive after it continue straight away.">
          <input className="input" type="datetime-local" value={toLocalInput(n.until)} onChange={(e) => setN({ until: fromLocalInput(e.target.value) })} />
        </Field>
      )}
    </>
  );
}

// ---- conditions on the contact ----

// CheckFields edits a check step with its preset's editor (tags, stages, points, owner, device), or
// the full rule builder for "Contact field value" or a rule a preset can't show. The rule builder
// keeps the half-typed rule as a draft; the step only stores it once it's valid.
function CheckFields({ n, setN, pickers }: { n: JourneyNode; setN: SetN; pickers: Pickers }) {
  const set = (rule: Rule | undefined) => setN({ rule });
  switch (n.preset) {
    case "tags": {
      const p = parseTags(n.rule);
      if (!p) break;
      return (<>
        <Field label="Yes if the contact has">
          <select className="select" value={p.match} onChange={(e) => set(tagsRule({ ...p, match: e.target.value as "any" | "all" }))}>
            <option value="any">any of these tags</option>
            <option value="all">all of these tags</option>
          </select>
        </Field>
        <Field label="Tags" hint="Separate with commas."><ListInput value={p.tags} placeholder="vip, lead" onChange={(tags) => set(tagsRule({ ...p, tags }))} /></Field>
      </>);
    }
    case "stage": {
      const s = parseStage(n.rule);
      if (!s) break;
      return (<>
        <CheckList label="Yes if the contact's stage is one of" items={pickers.lists.stages.map((x) => ({ value: x, label: x }))} value={s} onChange={(v) => set(stageRule(v))} empty="No stages yet: add them under Settings, Stages." />
      </>);
    }
    case "points": {
      const p = parsePoints(n.rule);
      if (!p) break;
      return (
        <Field label="Yes if the contact's points are">
          <div className="row" style={{ flexWrap: "nowrap" }}>
            <select className="select inline" aria-label="Comparison" value={p.op} onChange={(e) => set(pointsRule({ ...p, op: e.target.value as PointsPreset["op"] }))}>
              {POINT_OPS.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
            </select>
            <input className="input" style={{ width: 110 }} type="number" aria-label="Points" value={p.value} onChange={(e) => set(pointsRule({ ...p, value: Number(e.target.value) }))} />
          </div>
        </Field>
      );
    }
    case "owner": {
      const p = parseOwner(n.rule);
      if (!p) break;
      return (<>
        <Field label="Yes if the contact">
          <select className="select" value={p.mode} onChange={(e) => set(ownerRule({ ...p, mode: e.target.value as "in" | "exists" | "not_exists" }))}>
            <option value="in">is owned by one of</option>
            <option value="exists">has an owner (anyone)</option>
            <option value="not_exists">has no owner</option>
          </select>
        </Field>
        {p.mode === "in" && <CheckList label="Teammates" items={pickers.lists.users.map((u) => ({ value: String(u.id), label: u.name }))} value={p.ids} onChange={(ids) => set(ownerRule({ ...p, ids }))} empty="No teammates to list (or you can't view users)." />}
      </>);
    }
    case "device": {
      const p = parseDevice(n.rule);
      if (!p) break;
      return (<>
        <CheckList label="Device type is one of" items={DEVICE_TYPES.map((d) => ({ value: d as string, label: d }))} value={p.types} onChange={(types) => set(deviceRule({ ...p, types }))} empty="" />
        <CheckList label="and the OS is one of" items={OS_CHOICES.map((o) => ({ value: o, label: o }))} value={p.os} onChange={(os) => set(deviceRule({ ...p, os }))} empty="" />
        <Note>The device the contact was last seen on. Leave a list empty for any.</Note>
      </>);
    }
  }
  return <RuleFields n={n} setN={setN} />;
}

function RuleFields({ n, setN }: { n: JourneyNode; setN: SetN }) {
  const [root, setRoot] = useState(() => rootFromRule(n.rule));
  const issues = ruleProblems(root);
  return (
    <>
      {n.preset && n.preset !== "field" && <Note>This rule was changed elsewhere, so it's shown in full.</Note>}
      <Note>Contacts who match take “Yes”, checked the moment they reach this step. Event counts work too, e.g. did email_opened at least 1 time in 3 days.</Note>
      <GroupEditor group={root} onChange={(g) => { setRoot(g); setN({ rule: ruleProblems(g).length ? undefined : toRule(g) }); }} />
      {issues.length > 0 && <p className="xs" style={{ color: "var(--danger)" }}>{issues[0]}</p>}
    </>
  );
}

// ---- splits ----

function SplitFields({ id, n, setN, graph, decision }: { id: string; n: JourneyNode; setN: SetN; graph: JourneyGraph; decision?: SplitDecision }) {
  const vs = n.variants ?? [];
  const set = (i: number, p: Partial<(typeof vs)[number]>) => setN({ variants: vs.map((v, j) => (j === i ? { ...v, ...p } : v)) });
  const w = n.winner;
  const setW = (p: Partial<SplitWinner>) => setN({ winner: { ...(w ?? { metric: "clicked", after_seconds: 86400, min_per_variant: 100 }), ...p } });
  const problem = winnerProblem(graph, id);
  return (
    <>
      {decision && <SplitDecisionNote decision={decision} />}
      <Note>Each contact takes one path, at random by share, and always the same one. Compare paths in the journey's report.</Note>
      {vs.map((v, i) => (
        <div className="row" key={i} style={{ marginBottom: 6, flexWrap: "nowrap" }}>
          <input className="input" style={{ width: 90 }} value={v.label ?? ""} placeholder={String.fromCharCode(65 + i)} onChange={(e) => set(i, { label: e.target.value })} aria-label={`Path ${i + 1} name`} />
          <input className="input" style={{ width: 70 }} type="number" min={1} value={v.weight} onChange={(e) => set(i, { weight: Number(e.target.value) })} aria-label={`Path ${i + 1} share`} />
          <span className="xs muted" style={{ width: 34 }}>{share(n, i)}%</span>
          {vs.length > 2 && <button type="button" className="btn ghost sm" onClick={() => setN({ variants: vs.filter((_, j) => j !== i) })}>Remove</button>}
        </div>
      ))}
      {vs.length < 5 && <button type="button" className="btn sm" onClick={() => setN({ variants: [...vs, { weight: 1, label: /^Path \d+$/.test(vs[0]?.label ?? "") ? `Path ${vs.length + 1}` : String.fromCharCode(65 + vs.length) }] })}>Add a path</button>}
      <h4 className="small" style={{ margin: "18px 0 8px" }}>Winner</h4>
      <label className="check"><input type="checkbox" checked={!!w} onChange={(e) => setN({ winner: e.target.checked ? { metric: graph.goal ? "converted" : "clicked", after_seconds: 86400, min_per_variant: 100 } : null })} />Pick a winner automatically and send everyone after down it</label>
      {w && (
        <>
          <Field label="Best path by">
            <select className="select" value={w.metric} onChange={(e) => setW({ metric: e.target.value as SplitWinner["metric"] })}>
              <option value="opened">open rate of the emails or inbox messages after the split</option>
              <option value="clicked">click rate of the emails or inbox messages after the split</option>
              <option value="converted">conversion rate (the journey's goal)</option>
            </select>
          </Field>
          <WaitField label="Test for at least" seconds={w.after_seconds} onChange={(after_seconds) => setW({ after_seconds })} />
          <Field label="And until every path has" hint="Contacts who went down the path. The test keeps going until each path has this many.">
            <div className="row"><input className="input" style={{ width: 110 }} type="number" min={1} value={w.min_per_variant || ""} onChange={(e) => setW({ min_per_variant: Math.trunc(Number(e.target.value)) })} /><span className="small muted">contacts</span></div>
          </Field>
          <Note>Until then contacts are split by share. The best rate wins (ties: more hits, then the first path); the decision is final and shows here and in the report.</Note>
          {problem && <p className="xs" style={{ color: "var(--danger)" }}>{problem}</p>}
        </>
      )}
    </>
  );
}

export const METRIC_WORDS: Record<string, string> = { opened: "open rate", clicked: "click rate", converted: "conversion rate" };

export function SplitDecisionNote({ decision }: { decision: SplitDecision }) {
  return (
    <div className="panel" style={{ padding: "10px 12px", marginBottom: 12, borderColor: "var(--ok)" }} role="status">
      <strong className="small">Winner: path {decision.label || String.fromCharCode(65 + decision.path)}</strong>
      <p className="xs muted" style={{ margin: "4px 0 0" }}>
        Picked by {METRIC_WORDS[decision.metric] ?? decision.metric} on {new Date(decision.decided_at).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })}.
        {" "}Everyone reaching this step now takes it.
      </p>
      <ul className="xs" style={{ margin: "6px 0 0", paddingLeft: 16 }}>
        {decision.stats.map((st) => <li key={st.path}>Path {String.fromCharCode(65 + st.path)}: {st.hits} of {st.entrants} ({(st.rate * 100).toFixed(1)}%)</li>)}
      </ul>
    </div>
  );
}

// ---- contact fields ----

function StageField({ value, stages, onChange }: { value: string; stages: string[]; onChange: (stage: string) => void }) {
  return (
    <>
      <Field label="Move the contact to" hint="A contact is in one stage at a time; this replaces their current stage, forwards or back.">
        <select className="select" value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="">Choose a stage</option>
          {value && !stages.includes(value) && <option value={value}>{value} (no longer exists)</option>}
          {stages.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
      </Field>
      {stages.length === 0 && <Note>No stages yet. Add them under Workspace, Stages.</Note>}
    </>
  );
}

function AttrFields({ attrs, onChange }: { attrs: Record<string, unknown>; onChange: (a: Record<string, unknown>) => void }) {
  const rows = Object.entries(attrs);
  const rename = (i: number, k: string) => onChange(Object.fromEntries(rows.map(([kk, v], j) => (j === i ? [k, v] : [kk, v]))));
  return (
    <div className="field">
      <span>Set these fields</span>
      {rows.map(([k, v], i) => (
        <div className="row" key={i} style={{ flexWrap: "nowrap" }}>
          <input className="input" placeholder="field" value={k} onChange={(e) => rename(i, e.target.value.trim())} aria-label="Field" />
          <input className="input" placeholder="value" value={String(v ?? "")} onChange={(e) => onChange({ ...attrs, [k]: e.target.value })} aria-label="Value" />
          <button type="button" className="btn ghost sm" aria-label="Remove field" onClick={() => onChange(Object.fromEntries(rows.filter((_, j) => j !== i)))}>×</button>
        </div>
      ))}
      <div><button type="button" className="btn sm" onClick={() => onChange({ ...attrs, [`field_${rows.length + 1}`]: "" })}>Add field</button></div>
      <small>
        Values can use variables: <code>{"{{contact.first_name}}"}</code>, the trigger event's <code>{"{{event.product_name}}"}</code> (also <code>{"{contactfield=first_name}"}</code> and <code>{"{{.Attrs.first_name}}"}</code>).
        A date: <code>now</code>, <code>now+3hours</code>, <code>now-2days</code>, <code>now+1week</code>, <code>now+1month</code> (minutes, hours, days, weeks, months, years).
        Consent, tags, points, stage, owner and company have their own steps.
      </small>
    </div>
  );
}

/** triggerEvent: the event that starts the journey (its properties are {{event.x}} in messages). */
function triggerEvent(g: JourneyGraph): string | undefined {
  const t = g.trigger;
  return t.type === "event" ? t.event || undefined : t.type === "form_submitted" ? "form_submitted" : undefined;
}

/** journeyChannels lists the channels a journey's send steps use (all channels when it has none yet). */
function journeyChannels(g: JourneyGraph): Channel[] {
  const set = new Set<Channel>();
  for (const n of Object.values(g.nodes)) {
    const ch = nodeChannel(n.type);
    if (ch) set.add(ch);
    for (const r of n.channels ?? []) set.add(r.channel);
  }
  return set.size ? CHANNELS.filter((c) => set.has(c)) : CHANNELS;
}
