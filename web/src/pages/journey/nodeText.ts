// nodeText: the one-line summary each canvas card shows under its label, e.g.
// "Submits form ‘Newsletter’ within 3 days?" or "Add to list ‘VIP’, remove from ‘Trial’".
// Pure, so it is tested without a DOM.
import { CHANNEL_NAMES, type Channel, type JourneyGraph, type JourneyNode } from "../../api";
import { decisionText, formatWait, MAX_JUMPS, pathName, rulesText, share, stepNumbers } from "../../journeyGraph";
import { describe, OP_LABELS, type AttrOp, type RefNames } from "../../segmentRules";
import { parseDevice, parseOwner, parsePoints, parseStage, parseTags, POINT_OPS } from "./presets";

/** Names of the things steps point at, by id. */
export type Names = {
  segments?: Record<number, string>; templates?: Record<number, string>; forms?: Record<number, string>;
  assets?: Record<number, string>; pages?: Record<number, string>; focus?: Record<number, string>;
  users?: Record<number, string>; companies?: Record<number, string>; journeys?: Record<number, string>;
  integrations?: Record<number, string>;
  /** Dynamic Web Content slot names by slot key. */
  slots?: Record<string, string>;
  refs?: RefNames;
};

const q = (s: string) => `‘${s}’`;
const or = (xs: string[]) => (xs.length <= 1 ? xs.join("") : `${xs.slice(0, -1).join(", ")} or ${xs[xs.length - 1]}`);
const and = (xs: string[]) => (xs.length <= 1 ? xs.join("") : `${xs.slice(0, -1).join(", ")} and ${xs[xs.length - 1]}`);
const name = (m: Record<number, string> | undefined, id: number | undefined, noun: string) => (id ? q(m?.[id] ?? `${noun} ${id}`) : "");
const within = (n: JourneyNode) => `within ${formatWait(n.seconds)}?`;
const when = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("en-IN", { dateStyle: "medium", timeStyle: "short" });
};
const DNC_WORDS: Record<string, string> = { ...CHANNEL_NAMES, any: "any channel", all: "every channel" };

/** summary describes step id for its canvas card. */
export function summary(g: JourneyGraph, id: string, names: Names = {}): string {
  const n = g.nodes[id];
  if (!n) return "";
  const tpl = (tid?: number) => (tid ? q(names.templates?.[tid] ?? `template ${tid}`) : "");
  switch (n.type) {
    case "wait": return `Wait ${formatWait(n.seconds)}`;
    case "wait_until":
      return n.until ? `Wait until ${when(n.until)}` : n.time_of_day ? `Wait until ${n.time_of_day} in their time zone` : "Choose when to continue";
    case "wait_for_event": return n.event ? `Does ${q(n.event)} ${within(n)}` : "Choose an event";
    case "message_decision": return decisionText(g, n, (tid) => names.templates?.[tid]);
    case "on_form": return `Submits ${n.form_id ? `form ${name(names.forms, n.form_id, "form")}` : "any form"} ${within(n)}`;
    case "on_asset": return `Downloads ${n.asset_id ? name(names.assets, n.asset_id, "asset") : "any asset"} ${within(n)}`;
    case "on_page":
      return n.page_id ? `Visits landing page ${name(names.pages, n.page_id, "page")} ${within(n)}`
        : n.url ? `Visits a page containing ${q(n.url)} ${within(n)}` : `Visits any page ${within(n)}`;
    case "on_device": {
      const what = [n.device_type, n.os].filter(Boolean).join(" on ");
      return what ? `Visits from ${what} ${within(n)}` : "Choose a device";
    }
    case "on_dwc": return n.slot ? `Page requests ${q(names.slots?.[n.slot] ?? n.slot)} ${within(n)}` : "Choose a slot";
    case "condition": return n.segment_id ? `In segment ${name(names.segments, n.segment_id, "segment")}?` : "Choose a segment";
    case "check": return checkText(n, names);
    case "if_journey": {
      const js = (n.journey_ids ?? []).map((j) => name(names.journeys, j, "journey"));
      if (js.length === 0) return "Choose journeys";
      return n.journey_state === "completed" ? `Completed ${or(js)}?` : n.journey_state === "entered" ? `Ever entered ${or(js)}?` : `Active in ${or(js)}?`;
    }
    case "if_form_field": {
      if (!n.field) return "Choose a form field";
      const op = OP_LABELS[n.op as AttrOp] ?? n.op ?? "";
      const val = n.op === "exists" || n.op === "not_exists" ? "" : ` ${n.value ?? ""}`;
      return `${n.form_id ? `Form ${name(names.forms, n.form_id, "form")}` : "Latest form"}: ${n.field} ${op}${val}?`;
    }
    case "if_notification":
      return n.notify === "push" ? "Has a push device?" : n.notify === "inbox" ? "Has an unread app inbox message?" : "Has push or an unread inbox message?";
    case "if_valid_email": return `Email address is valid${n.reject_disposable ? " (not disposable)" : ""}?`;
    case "if_dnc": return `Do Not Contact for ${DNC_WORDS[n.channel ?? "any"] ?? n.channel}?`;
    case "if_before": return n.until ? `Before ${when(n.until)}?` : "Choose a date";
    case "if_visited": {
      const where = n.url ? `a page containing ${q(n.url)}` : "any page";
      return `Visited ${where} ${n.days ? `in the last ${n.days} day${n.days === 1 ? "" : "s"}` : "ever"}?`;
    }
    case "send_email": case "send_sms": case "send_whatsapp": case "send_rcs": case "send_push":
      return n.template_id ? `Send ${CHANNEL_NAMES[n.type.slice(5) as Channel]} ${tpl(n.template_id)}`.replace("Send Email", "Send email").replace("Send Push", "Send push") : "Choose a template";
    case "send_inbox": return n.template_id ? `Send to app inbox ${tpl(n.template_id)}` : "Choose a template";
    case "notify_user": {
      const who = [
        ...(n.to_owner ? ["the owner"] : []),
        ...(n.user_ids ?? []).map((u) => names.users?.[u] ?? `user ${u}`),
        ...(n.emails ?? []),
      ];
      if (!n.template_id) return "Choose an email template";
      return `Email ${tpl(n.template_id)} to ${who.length > 2 ? `${who.slice(0, 2).join(", ")} +${who.length - 2}` : and(who) || "…"}`;
    }
    case "send_message": return rulesText(n.channels ?? [], (c: string) => CHANNEL_NAMES[c as Channel] ?? c);
    case "show_focus": return n.focus_id ? `Show ${name(names.focus, n.focus_id, "focus item")} on their next page view` : "Choose a focus item";
    case "push_integration": return n.integration_id ? `Push to ${name(names.integrations, n.integration_id, "integration")}` : "Choose a CRM";
    case "push_dwc": return n.slot ? `Set ${q(names.slots?.[n.slot] ?? n.slot)} for ${n.days ?? 30} days` : "Choose a slot";
    case "update_attr": return Object.entries(n.attrs ?? {}).map(([k, v]) => `${k} = ${String(v)}`).join(", ") || "Set fields";
    case "update_owner": return n.owner_id === undefined ? "Choose an owner" : n.owner_id === 0 ? "Remove the owner" : `Assign to ${names.users?.[n.owner_id] ?? `user ${n.owner_id}`}`;
    case "update_company": return Object.entries(n.company_attrs ?? {}).map(([k, v]) => `${k} = ${String(v)}`).join(", ") || "Set company fields";
    case "add_company": return n.company_id ? `Link to ${name(names.companies, n.company_id, "company")}` : "Choose a company";
    case "company_points": return n.points ? `Company score ${n.points > 0 ? "+" : ""}${n.points}` : "Set points";
    case "adjust_points": return n.points ? `${n.points > 0 ? "+" : ""}${n.points} points` : "Set points";
    case "change_stage": return n.stage ? `Move to ${q(n.stage)}` : "Choose a stage";
    case "update_tags": {
      const parts = [n.add_tags?.length ? `Add ${n.add_tags.join(", ")}` : "", n.remove_tags?.length ? `remove ${n.remove_tags.join(", ")}` : ""].filter(Boolean);
      return parts.length ? capital(parts.join("; ")) : "Choose tags";
    }
    case "update_list": {
      const add = n.list_id && n.list_action !== "remove" ? [n.list_id] : n.add_lists ?? [];
      const rm = n.list_id && n.list_action === "remove" ? [n.list_id] : n.remove_lists ?? [];
      const parts = [
        add.length ? `add to ${add.map((l) => name(names.segments, l, "segment")).join(", ")}` : "",
        rm.length ? `remove from ${rm.map((l) => name(names.segments, l, "segment")).join(", ")}` : "",
      ].filter(Boolean);
      return parts.length ? capital(parts.join(", ")) : "Choose static segments";
    }
    case "change_journey": {
      const parts = [
        n.add_journeys?.length ? `add to ${n.add_journeys.map((j) => name(names.journeys, j, "journey")).join(", ")}` : "",
        n.remove_journeys?.length ? `remove from ${n.remove_journeys.map((j) => name(names.journeys, j, "journey")).join(", ")}` : "",
      ].filter(Boolean);
      return parts.length ? capital(parts.join(", ")) : "Choose journeys";
    }
    case "webhook": {
      if (!n.url) return "Enter a URL";
      try { return `POST to ${new URL(n.url).host}`; } catch { return "Enter a URL"; }
    }
    case "jump": {
      const num = n.target && g.nodes[n.target] ? stepNumbers(g)[n.target] : undefined;
      return num ? `Jump to step ${num} (max ${MAX_JUMPS} times)` : "Choose a step";
    }
    case "dnc_add": return `Do Not Contact: ${DNC_WORDS[n.channel ?? ""] ?? "choose a channel"}`;
    case "dnc_remove": return `Allow ${DNC_WORDS[n.channel ?? ""] ?? "a channel"} again`;
    case "delete_contact": return "Delete contact (permanent)";
    case "exit": return "Journey ends";
    case "split": {
      const vs = n.variants ?? [];
      return `${vs.map((_, i) => `${pathName(n, i)} ${share(n, i)}%`).join(" · ")}${n.winner ? " · picks a winner" : ""}`;
    }
  }
  return n.type;
}

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

function checkText(n: JourneyNode, names: Names): string {
  if (!n.rule) return "Describe who takes Yes";
  switch (n.preset) {
    case "tags": {
      const p = parseTags(n.rule);
      if (p) return p.tags.length === 1 ? `Has tag ${p.tags[0]}?` : p.match === "all" ? `Has tags ${and(p.tags)}?` : `Has tag ${or(p.tags)}?`;
      break;
    }
    case "stage": { const s = parseStage(n.rule); if (s) return `Stage is ${or(s)}?`; break; }
    case "points": {
      const p = parsePoints(n.rule);
      if (p) return `Points ${POINT_OPS.find((o) => o.op === p.op)?.label} ${p.value}?`;
      break;
    }
    case "owner": {
      const p = parseOwner(n.rule);
      if (p) return p.mode === "exists" ? "Has an owner?" : p.mode === "not_exists" ? "Has no owner?" : `Owner is ${or(p.ids.map((i) => names.users?.[Number(i)] ?? `user ${i}`))}?`;
      break;
    }
    case "device": {
      const p = parseDevice(n.rule);
      if (p) return `${[p.types.length ? `Device is ${or(p.types)}` : "", p.os.length ? `OS is ${or(p.os)}` : ""].filter(Boolean).join(" and ")}?`;
      break;
    }
  }
  return `${capital(describe(n.rule, names.segments ?? {}, names.refs ?? {}))}?`;
}
