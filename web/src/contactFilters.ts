// The Contacts search box: filters typed as key:value become chips; the rest is free text. The
// server parses the same syntax (internal/api/contactview.go) and compiles filters into a segment
// rule, so this file only splits, joins and suggests.

export type FilterKey = "is" | "segment" | "list" | "tag" | "stage" | "company" | "owner" | "points" | "email" | "name";

export const FILTER_KEYS: { key: FilterKey; hint: string; example: string }[] = [
  { key: "is", hint: "anonymous, identified, unsubscribed, bounced or Do Not Contact", example: "is:anonymous" },
  { key: "segment", hint: "in a segment (dynamic or static)", example: "segment:\"VIP buyers\"" },
  { key: "tag", hint: "has a tag", example: "tag:vip" },
  { key: "stage", hint: "in a lifecycle stage", example: "stage:Customer" },
  { key: "company", hint: "works at a company", example: "company:Acme" },
  { key: "owner", hint: "owned by a teammate, me or nobody", example: "owner:me" },
  { key: "points", hint: "points above, below or equal to a number", example: "points:>50" },
  { key: "email", hint: "email contains", example: "email:@example.com" },
  { key: "name", hint: "name contains", example: "name:asha" },
];

/** Typed keys from the Add filter questions, offered while typing too (they need each other as
 * described there: product: needs event:, active: needs period:). */
export const MORE_KEYS: { key: string; hint: string }[] = [
  { key: "event", hint: "did an event (add period: for dates)" },
  { key: "no_event", hint: "never did an event (add period: for dates)" },
  { key: "period", hint: "between two dates, for event:, campaign:, active: and form:" },
];

/** Filters that other pages put in links (M7: the contacts behind a number). They aren't offered
 * while typing, but they show as chips and can be removed like any other. */
export const LINK_KEYS = ["in", "not_in", "subscribed", "reachable", "under_cap", "journey", "at", "journey_status", "campaign", "run",
  "step", "outcome", "reason", "event", "no_event", "product", "category", "active", "utm", "amp_poll", "answer", "form", "period"];

/** list: is the old name of segment: (static segments used to be called lists). */
const KEYS = new Set<string>([...FILTER_KEYS.map((k) => k.key), "list", ...LINK_KEYS]);

/** isLinkFilter reports whether a chip is one of the LINK_KEYS (a list that can't be a dynamic segment). */
export const isLinkFilter = (chip: string) => LINK_KEYS.includes(chip.slice(0, Math.max(chip.indexOf(":"), 0)).toLowerCase());

export const IS_OPTIONS: { value: string; label: string }[] = [
  { value: "anonymous", label: "Anonymous visitors (no known ID or email yet)" },
  { value: "identified", label: "Identified contacts" },
  { value: "unsubscribed", label: "Unsubscribed from any channel" },
  { value: "bounced", label: "Email or SMS bounced" },
  { value: "dnc", label: "Do Not Contact (unsubscribed or bounced)" },
];

/** tokenize splits like the server: on spaces, with double quotes keeping spaces in a value. */
export function tokenize(q: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quoted = false;
  let has = false;
  for (const ch of q) {
    if (ch === '"') { quoted = !quoted; has = true; continue; }
    if (/\s/.test(ch) && !quoted) {
      if (has) out.push(cur);
      cur = ""; has = false;
      continue;
    }
    cur += ch; has = true;
  }
  if (has) out.push(cur);
  return out;
}

/** parseFilter returns the key and value of a key:value word with a known key, else null. */
export function parseFilter(token: string): { key: FilterKey | string; value: string } | null {
  const i = token.indexOf(":");
  if (i < 0) return null;
  const key = token.slice(0, i).toLowerCase();
  const value = token.slice(i + 1).trim();
  if (!KEYS.has(key) || !value) return null;
  return { key, value };
}

export const quoteValue = (v: string) => (/\s/.test(v) ? `"${v.replace(/"/g, "")}"` : v);
export const chipToken = (key: string, value: string) => `${key}:${quoteValue(value)}`;

/** splitQuery turns a query (from the URL) into chips and free text. */
export function splitQuery(q: string): { chips: string[]; text: string } {
  const chips: string[] = [];
  const text: string[] = [];
  for (const t of tokenize(q)) {
    const f = parseFilter(t);
    if (f) chips.push(chipToken(f.key, f.value));
    else text.push(t);
  }
  return { chips, text: text.join(" ") };
}

/** freeText drops filter words still being typed (they become chips on Enter), so a half-typed
 * "segment:vi" doesn't reach the server. */
export function freeText(text: string): string {
  return tokenize(text).filter((t) => !KEYS.has(t.slice(0, Math.max(t.indexOf(":"), 0)).toLowerCase())).map(quoteValue).join(" ");
}

export function joinQuery(chips: string[], text: string): string {
  return [...chips, text.trim()].filter(Boolean).join(" ");
}

/** takeChip moves a finished key:value word at the end of the input into a chip. With force (Enter),
 * the last word is taken even without a trailing space. */
export function takeChip(input: string, force = false): { chip: string; rest: string } | null {
  const quotes = (input.match(/"/g) ?? []).length;
  if (quotes % 2 === 1) return null; // still typing a quoted value
  if (!force && !/\s$/.test(input)) return null;
  const m = input.trimEnd().match(/(^|\s)((?:[a-zA-Z_]+):(?:"[^"]*"|\S+))$/);
  if (!m) return null;
  const word = tokenize(m[2])[0] ?? "";
  const f = parseFilter(word);
  if (!f) return null;
  return { chip: chipToken(f.key, f.value), rest: input.trimEnd().slice(0, m.index! + m[1].length) };
}

export type SuggestData = {
  // The workspace's event names (for event: and no_event:), and today (UTC) for period:; optional.
  events?: string[];
  today?: string;
  segments: { id: number; name: string; kind: string }[];
  tags: string[];
  stages: string[];
  companies: string[];
  users: { name: string; email: string }[];
};

/** A suggestion either fills the input with a key ("segment:") or completes a chip. */
/** A suggestion adds a chip, fills the box, or (menu) opens Add filter on one of its questions. */
export type Suggestion = { label: string; hint?: string; fill?: string; chip?: string; menu?: string };

/** The Add filter questions offered in the search box's list (ids of contactFilterMenu items), with
 * the words that find them. The empty box lists them first, so event and date filters are in plain
 * sight there too. */
export const MENU_SUGGESTIONS: { id: string; label: string; hint: string; words: string }[] = [
  { id: "event", label: "Did an event…", hint: "between two dates, e.g. purchase in the last 30 days", words: "event did date dates period when between purchase bought" },
  { id: "no_event", label: "Didn't do an event…", hint: "between two dates", words: "event didnt didn't not never date dates period" },
  { id: "campaign", label: "Got a campaign…", hint: "and opened, clicked, bounced…", words: "campaign email message sent opened clicked bounced got" },
  { id: "journey", label: "In a triggered or automation campaign…", hint: "entered it, finished it…", words: "journey campaign triggered automation entered" },
  { id: "active", label: "Was active…", hint: "on the website or app, between two dates", words: "active visit visited website app date dates period new returning" },
  { id: "form", label: "Submitted a form…", hint: "between two dates", words: "form submitted date" },
];

/** periodPresets: quick date ranges for period: (UTC days, today included). */
export function periodPresets(today: string): { value: string; label: string }[] {
  const day = (n: number) => new Date(Date.parse(today) - n * 86_400_000).toISOString().slice(0, 10);
  const monthStart = `${today.slice(0, 8)}01`;
  return [
    { value: `${day(6)}..${today}`, label: "Last 7 days" },
    { value: `${day(29)}..${today}`, label: "Last 30 days" },
    { value: `${day(89)}..${today}`, label: "Last 90 days" },
    { value: `${monthStart}..${today}`, label: "This month" },
  ];
}

/** currentWord is the word being typed at the end of the input (a quoted value counts as one). */
export function currentWord(input: string): { before: string; word: string } {
  let start: number;
  if ((input.match(/"/g) ?? []).length % 2 === 1) {
    const q = input.lastIndexOf('"');
    start = Math.max(input.slice(0, q).search(/\S*$/), 0);
  } else {
    start = input.search(/\S*$/);
  }
  return { before: input.slice(0, start), word: input.slice(start) };
}

/** suggest offers keys while a key is being typed, then values for that key. */
export function suggest(input: string, d: SuggestData, max = 8): Suggestion[] {
  const { before, word } = currentWord(input);
  const colon = word.indexOf(":");
  const menu = (m: (typeof MENU_SUGGESTIONS)[number]): Suggestion => ({ label: m.label, hint: m.hint, menu: m.id });
  // The empty box: the questions first, then the keys you can type.
  if (!input.trim()) return [...MENU_SUGGESTIONS.map(menu), ...FILTER_KEYS.map((f) => ({ label: `${f.key}:`, hint: f.hint, fill: `${f.key}:` }))];
  if (colon < 0) {
    if (!/^[a-zA-Z']*$/.test(word)) return [];
    const k = word.toLowerCase();
    const keys = [...FILTER_KEYS, ...MORE_KEYS].filter((f) => f.key.startsWith(k)).map((f) => ({ label: `${f.key}:`, hint: f.hint, fill: `${before}${f.key}:` }));
    const questions = k.length >= 3 ? MENU_SUGGESTIONS.filter((m) => m.words.split(" ").some((w) => w.startsWith(k))).map(menu) : [];
    return [...questions, ...keys].slice(0, max);
  }
  const key = word.slice(0, colon).toLowerCase();
  if (!KEYS.has(key)) return [];
  const partial = word.slice(colon + 1).replace(/"/g, "");
  const p = partial.toLowerCase();
  const match = (s: string) => s.toLowerCase().includes(p);
  const chip = (value: string, label: string, hint?: string): Suggestion => ({ label, hint, chip: chipToken(key === "list" ? "segment" : key, value) });
  switch (key) {
    case "is":
      return IS_OPTIONS.filter((o) => match(o.value) || match(o.label)).map((o) => chip(o.value, `is:${o.value}`, o.label));
    case "segment":
    case "list":
      return d.segments.filter((s) => match(s.name) || String(s.id) === p)
        .map((s) => chip(s.name, s.name, s.kind === "static" ? "Static segment" : "Dynamic segment")).slice(0, max);
    case "tag":
      return d.tags.filter(match).map((t) => chip(t, t)).slice(0, max);
    case "stage":
      return d.stages.filter(match).map((s) => chip(s, s)).slice(0, max);
    case "company":
      return d.companies.filter(match).map((c) => chip(c, c)).slice(0, max);
    case "owner":
      return [
        ...[{ v: "me", l: "Me" }, { v: "none", l: "Nobody (no owner)" }].filter((o) => match(o.v) || match(o.l)).map((o) => chip(o.v, `owner:${o.v}`, o.l)),
        ...d.users.filter((u) => match(u.name) || match(u.email)).map((u) => chip(u.email, u.name || u.email, u.name ? u.email : undefined)),
      ].slice(0, max);
    case "points":
      if (/^(>=|<=|>|<|=)?\d+(\.\d+)?$/.test(partial)) return [chip(partial, `points:${partial}`, "Press Enter to add")];
      return [">0", ">=50", ">=100", "<10", "=0"].filter((v) => v.startsWith(partial)).map((v) => ({ label: `points:${v}`, fill: `${before}points:${v}` }));
    case "email":
    case "name":
      return partial ? [chip(partial, `${key}:${partial}`, key === "email" ? "Email contains this" : "Name contains this")] : [];
    case "event":
    case "no_event":
      return (d.events ?? []).filter(match).map((e) => chip(e, `${key}:${e}`, key === "event" ? "Did it" : "Never did it")).slice(0, max);
    case "period":
      return periodPresets(d.today ?? new Date().toISOString().slice(0, 10)).filter((o) => match(o.label) || o.value.startsWith(p))
        .map((o) => chip(o.value, o.label, o.value.replace("..", " to ")));
  }
  return [];
}
