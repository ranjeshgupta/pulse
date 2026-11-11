// RichText: the rich text editor for HTML fields (TipTap, in RichTextEditor.tsx). The editor is
// loaded on first use so pages without one don't download it; until then the field shows a
// placeholder of the same size.
import { forwardRef, lazy, Suspense, type ReactNode } from "react";

export type RichTextHandle = {
  /** Inserts plain text (a merge tag, a {form:KEY} token) at the cursor. */
  insertText: (text: string) => void;
  /** Inserts an HTML snippet (a link) at the cursor. */
  insertHtml: (html: string) => void;
};

export type RichTextProps = {
  value: string;
  onChange: (html: string) => void;
  placeholder?: string;
  /** Height of the editing area in px (it grows with the content). */
  minHeight?: number;
  /** Offer variables ({{contact.first_name}}, {{event.x}}, {{campaign.name}}). "email" adds the unsubscribe and preference-center links. */
  personalize?: boolean | "email";
  allowImages?: boolean;
  allowTables?: boolean;
  /** A shorter toolbar for small fields. */
  compact?: boolean;
  /** The field's accessible name. */
  label?: string;
  readOnly?: boolean;
  /** What to suggest when the HTML can't be edited visually (e.g. "or use the drag-and-drop builder"). */
  lossHint?: ReactNode;
};

const Editor = lazy(() => import("./RichTextEditor"));

export const RichText = forwardRef<RichTextHandle, RichTextProps>(function RichText(props, ref) {
  return (
    <Suspense fallback={<div className="rt-loading" style={{ minHeight: (props.minHeight ?? 220) + 41 }} aria-busy="true" aria-label={`${props.label ?? "Content"} (loading the editor)`} />}>
      <Editor {...props} ref={ref} />
    </Suspense>
  );
});
