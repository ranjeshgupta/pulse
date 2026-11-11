// Multi-page forms and file fields in the form builder: the same rules the server applies
// (internal/forms), so mistakes show while editing.
import type { FormField } from "../api";

export const MAX_PAGES = 10;
export const MAX_FILE_FIELDS = 5;
export const MAX_FILE_MB = 10;
export const DEFAULT_FILE_MB = 5;
/** Types a file field may accept (server: assets.UploadExtensions). */
export const UPLOAD_KINDS: { label: string; exts: string[] }[] = [
  { label: "PDF", exts: [".pdf"] },
  { label: "Images (PNG, JPEG, GIF, WebP)", exts: [".png", ".jpg", ".jpeg", ".gif", ".webp"] },
  { label: "Word, Excel, PowerPoint", exts: [".docx", ".xlsx", ".pptx"] },
  { label: "Text and CSV", exts: [".txt", ".csv"] },
];

/** formPages splits fields at page breaks; a page's title is its break's label (the first page has none). */
export function formPages(fields: FormField[]): { title: string; fields: FormField[] }[] {
  const pages = [{ title: "", fields: [] as FormField[] }];
  for (const f of fields) {
    if (f.type === "page_break") pages.push({ title: f.label, fields: [] });
    else pages[pages.length - 1].fields.push(f);
  }
  return pages;
}

/** layoutProblems lists what's wrong with the form's pages and files as a whole. */
export function layoutProblems(fields: FormField[]): { at: Map<number, string>; problems: string[] } {
  const at = new Map<number, string>();
  const problems: string[] = [];
  let pages = 1;
  let files = 0;
  fields.forEach((f, i) => {
    if (f.type === "page_break") {
      pages++;
      if (i === 0 || i === fields.length - 1) at.set(i, "A page break goes between fields, not first or last.");
      else if (fields[i - 1].type === "page_break") at.set(i, "Two page breaks in a row make an empty page.");
    }
    if (f.type === "file") {
      files++;
      if (f.key === "email" || f.key === "phone") at.set(i, `${f.key} can't be a file field.`);
      else if ((f.max_mb ?? 0) < 0 || (f.max_mb ?? 0) > MAX_FILE_MB) at.set(i, `The size limit must be 1 to ${MAX_FILE_MB} MB.`);
    }
  });
  if (pages > MAX_PAGES) problems.push(`A form can have at most ${MAX_PAGES} pages.`);
  if (files > MAX_FILE_FIELDS) problems.push(`A form can have at most ${MAX_FILE_FIELDS} file fields.`);
  return { at, problems };
}

/** uploadOf reads a file field's value in a submission ({upload_id, filename, size}), or null. */
export function uploadOf(v: unknown): { id: number; filename: string; size: number } | null {
  if (!v || typeof v !== "object") return null;
  const o = v as Record<string, unknown>;
  return typeof o.upload_id === "number" ? { id: o.upload_id, filename: String(o.filename ?? "file"), size: Number(o.size ?? 0) } : null;
}
