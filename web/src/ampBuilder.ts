// The AMP email block builder's pure logic: new blocks with starter content, ordering, duplicating,
// plain-language checks that mirror the server's rules (internal/amp/build.go), and switching the
// template's AMP part between off, blocks and code. No React here (tested in ampBuilder.test.ts).
import type { AmpBlock, AmpBlockType, AmpDoc, AmpFormField, AmpImage, ChannelContent } from "./api";

export const AMP_MAX_BLOCKS = 40;
export const AMP_MAX_BYTES = 204800;

/** The block types, in the add-block menu's order, with marketer-facing names. */
export const BLOCK_TYPES: { type: AmpBlockType; label: string; hint: string }[] = [
  { type: "heading", label: "Heading", hint: "A big title" },
  { type: "text", label: "Text", hint: "Paragraphs of text" },
  { type: "image", label: "Image", hint: "One picture, optionally linked" },
  { type: "button", label: "Button", hint: "A link styled as a button" },
  { type: "carousel", label: "Carousel", hint: "Slides people swipe through" },
  { type: "gallery", label: "Gallery", hint: "A scrolling row of pictures" },
  { type: "accordion", label: "Accordion", hint: "Sections that open on tap, e.g. FAQs" },
  { type: "poll", label: "Poll", hint: "One question, people vote inside the email" },
  { type: "form", label: "Form", hint: "Fields people fill in and send inside the email" },
];
export const blockLabel = (t: AmpBlockType | string) => BLOCK_TYPES.find((b) => b.type === t)?.label ?? t;

export const FIELD_TYPES: { type: AmpFormField["type"]; label: string }[] = [
  { type: "text", label: "Short text" },
  { type: "textarea", label: "Long text" },
  { type: "email", label: "Email address" },
  { type: "number", label: "Number" },
  { type: "select", label: "Drop-down list" },
  { type: "checkbox", label: "Tick box" },
];

const BLOCK_ID = /^[a-z0-9][a-z0-9_-]{0,39}$/;
const FIELD_KEY = /^[a-z][a-z0-9_]{0,39}$/;
const COLOR = /^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$/;
export const RESERVED_FIELD_KEYS = ["block", "message_id", "template_id", "journey_id", "broadcast_id", "run", "variant", "answer", "question", "form"];

// Contact fields a form can't copy answers into (the server's eventschema.ListTargetAllowed).
const PROTECTED_FIELDS = new Set(["email", "phone", "name", "first_name", "last_name", "external_id", "anonymous_id", "user_id", "timezone",
  "tags", "points", "first_seen", "last_seen", "locale", "merged_into", "stage", "stage_changed_at", "date_added", "date_identified",
  "date_modified", "email_domain", "phone_country", "owner_id", "owner_name", "browser", "app_version", "email_bounced", "sms_bounced"]);

/** canSaveTo: whether a form answer may be copied into this contact field (custom fields only). */
export function canSaveTo(key: string): boolean {
  if (!/^[a-z][a-z0-9_]*$/.test(key)) return false;
  if (PROTECTED_FIELDS.has(key)) return false;
  return !(key.startsWith("optout_") || key.startsWith("company_") || key.startsWith("device_"));
}

/** nextId: "<type>-<n>" with the smallest n not used yet. */
export function nextId(type: string, existing: Iterable<string>): string {
  const used = new Set(existing);
  for (let n = 1; ; n++) if (!used.has(`${type}-${n}`)) return `${type}-${n}`;
}

const img = (): AmpImage => ({ src: "", alt: "" });

/** newBlock: a block of this type with starter content and a unique id. */
export function newBlock(type: AmpBlockType, existingIds: Iterable<string> = []): AmpBlock {
  const id = nextId(type, existingIds);
  switch (type) {
    case "heading": return { id, type, text: "A headline that gets attention" };
    case "text": return { id, type, text: 'Hi {{contact.first_name | default:"there"}},\n\nWrite your message here.' };
    case "image": return { id, type, image: img() };
    case "button": return { id, type, text: "Shop now", url: "" };
    case "carousel": return { id, type, items: [img(), img()], height: 300, autoplay: 0 };
    case "gallery": return { id, type, items: [img(), img()], height: 200 };
    case "accordion": return { id, type, sections: [{ title: "How does delivery work?", body: "Write the answer here." }, { title: "Can I return it?", body: "Write the answer here." }] };
    case "poll": return { id, type, text: "How did we do?", options: ["Great", "Okay", "Not good"], show_results: true, submit_text: "Vote", success_text: "Thanks for voting!" };
    case "form": return { id, type, text: "Tell us what you think", fields: [{ key: "feedback", label: "Your feedback", type: "textarea", required: true }], submit_text: "Send", success_text: "Thanks! We got your answer." };
  }
}

/** starterDoc: what a new AMP version starts from (valid as it is, so the preview shows at once). */
export function starterDoc(): AmpDoc {
  const blocks: AmpBlock[] = [];
  for (const t of ["heading", "text", "poll"] as AmpBlockType[]) blocks.push(newBlock(t, blocks.map((b) => b.id)));
  blocks[0].text = 'Hi {{contact.first_name | default:"there"}}, quick question';
  blocks[1].text = "It takes one tap, right here in the email.";
  return { blocks, style: { font: "sans" } };
}

/** A minimal valid AMP email, for writing code by hand. */
export const STARTER_CODE = `<!doctype html>
<html ⚡4email data-css-strict>
<head>
<meta charset="utf-8">
<script async src="https://cdn.ampproject.org/v0.js"></script>
<style amp4email-boilerplate>body{visibility:hidden}</style>
<style amp-custom>
body{margin:0;padding:24px;font-family:Arial,Helvetica,sans-serif;color:#152238}
</style>
</head>
<body>
<p>Hi {{contact.first_name | default:"there"}},</p>
</body>
</html>
`;

export function addBlock(doc: AmpDoc, type: AmpBlockType, at = doc.blocks.length): AmpDoc {
  const b = newBlock(type, doc.blocks.map((x) => x.id));
  const blocks = [...doc.blocks];
  blocks.splice(Math.max(0, Math.min(at, blocks.length)), 0, b);
  return { ...doc, blocks };
}

/** moveBlock moves block index by delta (-1 up, +1 down); out of range leaves the doc as it is. */
export function moveBlock(doc: AmpDoc, index: number, delta: number): AmpDoc {
  const to = index + delta;
  if (index < 0 || index >= doc.blocks.length || to < 0 || to >= doc.blocks.length || delta === 0) return doc;
  const blocks = [...doc.blocks];
  const [b] = blocks.splice(index, 1);
  blocks.splice(to, 0, b);
  return { ...doc, blocks };
}

export function removeBlock(doc: AmpDoc, index: number): AmpDoc {
  if (index < 0 || index >= doc.blocks.length) return doc;
  return { ...doc, blocks: doc.blocks.filter((_, i) => i !== index) };
}

/** duplicateBlock inserts a deep copy right after the block, under a new id. */
export function duplicateBlock(doc: AmpDoc, index: number): AmpDoc {
  const src = doc.blocks[index];
  if (!src) return doc;
  const copy: AmpBlock = { ...JSON.parse(JSON.stringify(src)), id: nextId(src.type, doc.blocks.map((b) => b.id)) };
  const blocks = [...doc.blocks];
  blocks.splice(index + 1, 0, copy);
  return { ...doc, blocks };
}

export function updateBlock(doc: AmpDoc, index: number, patch: Partial<AmpBlock>): AmpDoc {
  return { ...doc, blocks: doc.blocks.map((b, i) => (i === index ? { ...b, ...patch } : b)) };
}

/** fieldKeyFrom turns a label into a field name: "Your city?" → "your_city". */
export function fieldKeyFrom(label: string, taken: Iterable<string> = []): string {
  let k = label.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^[^a-z]+|_+$/g, "").slice(0, 36) || "field";
  if (RESERVED_FIELD_KEYS.includes(k)) k = `${k}_1`;
  const used = new Set(taken);
  if (!used.has(k)) return k;
  for (let n = 2; ; n++) if (!used.has(`${k}_${n}`)) return `${k}_${n}`;
}

const trim = (s?: string) => (s ?? "").trim();
const httpsOrVar = (u?: string) => trim(u).startsWith("https://") || trim(u).startsWith("{{");
const linkOK = (u?: string) => { const s = trim(u); return s === "" || /^(https?:\/\/|mailto:|\{\{)/.test(s); };

export type DocProblem = { block: number | null; message: string };

/** checkDoc: the problems that stop the AMP version saving, in plain words (the server's rules). */
export function checkDoc(doc: AmpDoc): DocProblem[] {
  const out: DocProblem[] = [];
  const top = (message: string) => out.push({ block: null, message });
  if (!doc.blocks.length) top("Add at least one block.");
  if (doc.blocks.length > AMP_MAX_BLOCKS) top(`Use at most ${AMP_MAX_BLOCKS} blocks.`);
  const st = doc.style ?? {};
  for (const [name, c] of [["Background", st.background], ["Text", st.text], ["Accent", st.accent]] as const)
    if (c && !COLOR.test(c)) top(`${name} colour must look like #1a2b3c.`);
  const ids = new Set<string>();
  doc.blocks.forEach((b, i) => {
    const add = (message: string) => out.push({ block: i, message });
    if (!BLOCK_ID.test(b.id)) add("Its name can only use lowercase letters, digits, - and _ (up to 40).");
    else if (ids.has(b.id)) add(`The name "${b.id}" is used by another block.`);
    ids.add(b.id);
    const images = (list: AmpImage[] | undefined, min: number) => {
      const l = list ?? [];
      if (l.length < min || l.length > 10) add(min === 1 ? "Choose an image." : `Add ${min} to 10 images.`);
      if (l.some((im) => !httpsOrVar(im.src))) add(l.length > 1 ? "Every image needs an address starting with https://." : "The image needs an address starting with https://.");
      if (l.some((im) => !linkOK(im.link))) add("Image links must be web addresses (https://…).");
    };
    switch (b.type) {
      case "heading": case "text":
        if (!trim(b.text)) add("Write some text.");
        break;
      case "image":
        images(b.image ? [b.image] : [], 1);
        break;
      case "button":
        if (!trim(b.text)) add("Give the button a label.");
        if (!trim(b.url)) add("Add the web address the button opens.");
        else if (!linkOK(b.url)) add("The button's address must start with https://.");
        break;
      case "carousel":
        images(b.items, 2);
        if ((b.autoplay ?? 0) < 0 || (b.autoplay ?? 0) > 30) add("Autoplay is 0 (off) to 30 seconds.");
        break;
      case "gallery":
        images(b.items, 2);
        break;
      case "accordion": {
        const s = b.sections ?? [];
        if (!s.length || s.length > 10) add("Add 1 to 10 sections.");
        if (s.some((x) => !trim(x.title))) add("Every section needs a title.");
        break;
      }
      case "poll": {
        if (!trim(b.text)) add("Write the question.");
        const o = b.options ?? [];
        if (o.length < 2 || o.length > 8) add("A poll has 2 to 8 answers.");
        const seen = new Set<string>();
        for (const a of o) {
          const k = a.trim().toLowerCase();
          if (!k) { add("Fill in every answer (or remove the empty one)."); break; }
          if (a.trim().length > 100) { add("Answers can be at most 100 characters."); break; }
          if (seen.has(k)) { add(`The answer "${a.trim()}" is there twice.`); break; }
          seen.add(k);
        }
        break;
      }
      case "form": {
        const f = b.fields ?? [];
        if (!f.length || f.length > 10) add("A form has 1 to 10 fields.");
        const keys = new Set<string>();
        f.forEach((x, j) => {
          const n = `Field ${j + 1}${trim(x.label) ? ` ("${trim(x.label)}")` : ""}`;
          if (!FIELD_KEY.test(x.key)) add(`${n}: its saved name must start with a letter and use lowercase letters, digits and _.`);
          else if (RESERVED_FIELD_KEYS.includes(x.key)) add(`${n}: "${x.key}" is reserved; choose another saved name.`);
          else if (keys.has(x.key)) add(`${n}: the saved name "${x.key}" is used twice.`);
          else if (!trim(x.label)) add(`${n}: add a label.`);
          keys.add(x.key);
          if (!FIELD_TYPES.some((t) => t.type === x.type)) add(`${n}: choose a field type.`);
          if (x.type === "select" && (!(x.options ?? []).filter((o) => o.trim()).length || (x.options ?? []).length > 20)) add(`${n}: a drop-down list needs 1 to 20 choices.`);
          if (x.save_to && !canSaveTo(x.save_to)) add(`${n}: answers can't be saved into "${x.save_to}". Choose one of your own contact fields.`);
        });
        break;
      }
      default:
        add("Unknown block type: remove it.");
    }
  });
  return out;
}

/** cleanDoc trims what the UI keeps while editing (empty choice lines) before it's sent. */
export function cleanDoc(doc: AmpDoc): AmpDoc {
  return {
    ...doc,
    blocks: doc.blocks.map((b) => (b.type === "form" && b.fields
      ? { ...b, fields: b.fields.map((f) => (f.type === "select" ? { ...f, options: (f.options ?? []).map((o) => o.trim()).filter(Boolean) } : { ...f, options: undefined })) }
      : b)),
  };
}

// ---- modes ----

export type AmpMode = "off" | "blocks" | "code";

/** ampMode: how the template's AMP part is edited now. */
export function ampMode(c: ChannelContent): AmpMode {
  if (c.amp_blocks) return "blocks";
  if (trim(c.amp)) return "code";
  return "off";
}

/** The content patches the editor applies to ChannelContent. */
export const turnOn = (mode: "blocks" | "code"): Partial<ChannelContent> =>
  mode === "blocks" ? { amp_blocks: starterDoc(), amp: undefined } : { amp: STARTER_CODE, amp_blocks: undefined };
export const turnOff = (): Partial<ChannelContent> => ({ amp: undefined, amp_blocks: undefined });
/** toCode keeps the code the blocks generated and stops building from blocks. */
export const toCode = (generated: string): Partial<ChannelContent> => ({ amp: generated, amp_blocks: undefined });
/** toBlocks starts the blocks fresh: code can't be turned back into blocks. */
export const toBlocks = (): Partial<ChannelContent> => ({ amp: undefined, amp_blocks: starterDoc() });

/** pollIds: the poll blocks of a doc (whose results the editor shows). */
export const pollIds = (doc?: AmpDoc | null) => (doc?.blocks ?? []).filter((b) => b.type === "poll").map((b) => b.id);
