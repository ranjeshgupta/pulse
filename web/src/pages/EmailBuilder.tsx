// GrapesJS drag-and-drop email builder (the engine Mautic uses), with the newsletter preset.
// Loaded lazily: it is the heaviest part of the app.
import grapesjs, { type Editor } from "grapesjs";
import "grapesjs/dist/css/grapes.min.css";
// The builder's panel and block icons are Font Awesome 4. GrapesJS fetches it from cdnjs by default,
// which the app's CSP (style-src 'self') blocks; bundling it serves it from Pulse itself.
import "font-awesome/css/font-awesome.min.css";
import newsletter from "grapesjs-preset-newsletter";
import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";

export type EmailBuilderHandle = { read: () => { html: string; project: unknown } };

const STARTER = `
<table style="width:100%;background:#f4f6f8;padding:24px 0;font-family:Arial,sans-serif">
  <tr><td align="center">
    <table style="width:600px;background:#ffffff;border-radius:8px;padding:32px">
      <tr><td><h1 style="margin:0 0 12px;font-size:24px;color:#152238">Hi {{contact.first_name | default:"there"}},</h1>
      <p style="font-size:15px;line-height:1.6;color:#3a4a66">Write your message here. Drag blocks from the right to add images, buttons and columns.</p>
      <a href="https://example.com" style="display:inline-block;margin-top:12px;background:#2747d6;color:#fff;padding:10px 18px;border-radius:5px;text-decoration:none">Call to action</a>
      </td></tr>
    </table>
  </td></tr>
</table>`;

/** onChange: called when the user changes the email in the builder (not while it loads), so the
 * page can ask before unsaved builder edits are lost. */
export default forwardRef<EmailBuilderHandle, { html: string; project?: unknown; onChange?: () => void }>(function EmailBuilder({ html, project, onChange }, ref) {
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<Editor | null>(null);
  const changed = useRef(onChange);
  changed.current = onChange;

  useEffect(() => {
    if (!host.current) return;
    const e = grapesjs.init({
      container: host.current,
      // GrapesJS writes this onto the host inline. It must be a definite height: "100%" of an
      // auto-height parent collapses the editor to 0px.
      height: "max(520px, calc(100vh - 230px))",
      storageManager: false,
      cssIcons: "", // bundled above: no request to cdnjs
      plugins: [newsletter],
      fromElement: false,
      components: project ? undefined : html || STARTER,
      projectData: project ? (project as never) : undefined,
    });
    editor.current = e;
    // Loading the design counts as changes inside GrapesJS: start counting once it has settled.
    let ready = false;
    let settle = 0;
    e.on("load", () => { settle = window.setTimeout(() => { e.clearDirtyCount(); ready = true; }, 0); });
    e.on("update", () => { if (ready && e.getDirtyCount() > 0) changed.current?.(); });
    return () => { window.clearTimeout(settle); e.destroy(); };
    // Initialise once per mounted template; later edits live inside the editor.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useImperativeHandle(ref, () => ({
    read: () => {
      const e = editor.current!;
      // The newsletter preset inlines CSS into the HTML, which is what email clients need.
      const inlined = e.runCommand("gjs-get-inlined-html") as string | undefined;
      return { html: inlined || `${e.getHtml()}<style>${e.getCss()}</style>`, project: e.getProjectData() };
    },
  }));

  return <div className="gjs-host" ref={host} />;
});
