// Pure helpers of the web SDK's form renderer (pulse.ts): pages and file checks.

export interface FormField { key: string; label: string; type: string; required?: boolean; options?: string[]; default?: string; accept?: string[]; max_mb?: number }

/** UPLOAD_TYPES are the file types a form's file field may accept when it names none (server: assets.UploadExtensions). */
export const UPLOAD_TYPES = [".pdf", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".txt", ".csv", ".docx", ".xlsx", ".pptx"];

/** formPages splits a form's fields at its page breaks; each page has the break's label as title. */
export function formPages(fields: FormField[]): { title: string; fields: FormField[] }[] {
  const pages = [{ title: "", fields: [] as FormField[] }];
  for (const f of fields) {
    if (f.type === "page_break") pages.push({ title: f.label, fields: [] });
    else pages[pages.length - 1].fields.push(f);
  }
  return pages;
}

/** fileProblem checks a chosen file against the field's limits ("" when fine); the server checks the content again. */
export function fileProblem(fd: FormField, file: { name: string; size: number }): string {
  const max = (fd.max_mb || 5) * 1048576;
  if (file.size > max) return `The file is too big: ${fd.max_mb || 5} MB at most.`;
  const ext = (/\.[^.]+$/.exec(file.name)?.[0] ?? "").toLowerCase();
  const accept = fd.accept?.length ? fd.accept : UPLOAD_TYPES;
  const ok = accept.some((a) => a === ext || (a === ".jpg" && ext === ".jpeg") || (a === ".jpeg" && ext === ".jpg"));
  return ok ? "" : `This type of file isn't accepted here. Upload ${accept.map((a) => a.slice(1).toUpperCase()).join(", ")}.`;
}

