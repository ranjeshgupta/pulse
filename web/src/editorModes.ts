// Which editor an email template or landing page opens in, and when switching editors needs a
// warning. The drag-and-drop builder (GrapesJS) keeps its layout in the `builder` field; saving from
// the rich text or HTML editor replaces it with plain HTML.
import { unwrapRichText } from "./richTextHtml";

/** Email template editors: drag and drop, rich text, raw HTML, and the plain-text part. */
export type EmailMode = "visual" | "richtext" | "html" | "text";
/** Landing page editors. */
export type PageMode = "builder" | "richtext" | "html";

/** The builder's design, if any, wins; then rich text (recognised by its wrapper); then raw HTML. */
export function initialEmailMode(t: { html: string; builder?: unknown }): EmailMode {
  if (t.builder) return "visual";
  if (unwrapRichText(t.html) !== null) return "richtext";
  return t.html.trim() ? "html" : "visual";
}

export function initialPageMode(p: { html: string; builder?: unknown }): PageMode {
  if (p.builder) return "builder";
  return unwrapRichText(p.html) !== null ? "richtext" : "html";
}

/** True for the editors whose save throws the drag-and-drop layout away. */
export const replacesLayout = (m: EmailMode | PageMode) => m === "html" || m === "richtext";

/**
 * switchWarning is the confirm() text when a switch would lose the drag-and-drop layout, or null.
 * Leaving the builder (or the plain-text tab, which keeps it) for rich text or HTML warns once;
 * moving between rich text and HTML doesn't warn again.
 */
export function switchWarning(from: EmailMode | PageMode, to: EmailMode | PageMode, hasBuilderLayout: boolean): string | null {
  if (!hasBuilderLayout || from === to || !replacesLayout(to) || replacesLayout(from)) return null;
  const name = to === "html" ? "HTML" : "Rich text";
  return `This was designed in the drag-and-drop builder. ${name} edits the builder's HTML, and saving from ${name} replaces the drag-and-drop layout for good. Switch to ${name}?`;
}
