// The rich text editor's TipTap schema. Shared by the editor (RichText.tsx) and the round-trip
// check, so "what the editor keeps" is decided by one list. Images and tables are always in the
// schema, so HTML that has them is never dropped; the props only hide the toolbar buttons.
import { generateHTML, generateJSON, type Extensions } from "@tiptap/core";
import Image from "@tiptap/extension-image";
import { TableKit } from "@tiptap/extension-table";
import TextAlign from "@tiptap/extension-text-align";
import { BackgroundColor, Color, TextStyle } from "@tiptap/extension-text-style";
import { Placeholder } from "@tiptap/extensions";
import StarterKit from "@tiptap/starter-kit";
import { isSafeUrl } from "./richTextHtml";

export function richTextExtensions(placeholder = ""): Extensions {
  return [
    StarterKit.configure({
      heading: { levels: [1, 2, 3, 4, 5, 6] },
      // It appends a paragraph after a final table or rule: an edit the author didn't make.
      trailingNode: false,
      link: {
        openOnClick: false,
        autolink: true,
        linkOnPaste: true,
        // Links open where the reader is unless the author asks for a new tab.
        HTMLAttributes: { target: null, rel: null },
        // Template tags ({{.UnsubscribeURL}}) pass the default check; javascript: and friends don't,
        // so they're dropped when HTML is read and blanked when it's written.
        isAllowedUri: (url, ctx) => ctx.defaultValidate(url) && isSafeUrl(url),
      },
    }),
    TextStyle,
    Color,
    BackgroundColor,
    TextAlign.configure({ types: ["heading", "paragraph"] }),
    Image.configure({ inline: false, allowBase64: false, HTMLAttributes: { style: "max-width:100%;height:auto" } }),
    // No column resizing: it writes pixel widths into the HTML whenever a border is nudged.
    TableKit.configure({ table: { resizable: false } }),
    Placeholder.configure({ placeholder }),
  ];
}

/** roundTrip is what the editor makes of some HTML without any edit: its parse, then its output. */
export function roundTrip(html: string): string {
  const ext = richTextExtensions();
  return generateHTML(generateJSON(html, ext), ext);
}
