// Small shared building blocks. Styling lives in styles.css.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { ask, DialogCtx, DISCARD, isCloseButton, useDirtyRef } from "./guard";
import { ApiError, CHANNEL_NAMES, type Channel } from "./api";

export function PageHead({ title, intro, actions, back }: { title: ReactNode; intro?: ReactNode; actions?: ReactNode; back?: ReactNode }) {
  return (
    <header className="page-head">
      <div>
        {back}
        <h1>{title}</h1>
        {intro && <p>{intro}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  );
}

/** Field: a labelled form control. group renders a div (role group) instead of a <label>, for fields
 * holding several controls (a checkbox and buttons), so a click on one doesn't activate another. */
export function Field({ label, hint, group, children }: { label: string; hint?: ReactNode; group?: boolean; children: ReactNode }) {
  const inner = <><span>{label}</span>{children}{hint && <small>{hint}</small>}</>;
  return group ? <div className="field" role="group" aria-label={label}>{inner}</div> : <label className="field">{inner}</label>;
}

export function Empty({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      <h3>{title}</h3>
      {children && <p>{children}</p>}
      {action}
    </div>
  );
}

/** errorText: what to show for a failed request. An address with a malformed id (/templates/abc,
 * from a mistyped or cut-off link) gets the server's "invalid id"; people need "not found". */
export function errorText(error: unknown): string {
  if (error instanceof ApiError && error.status === 400 && /^invalid id$/i.test(error.message.trim())) {
    return "Not found. The address may be mistyped, or the item was deleted.";
  }
  return error instanceof Error ? error.message : String(error);
}

export function ErrorNote({ error }: { error: unknown }) {
  if (!error) return null;
  return <div className="alert error" role="alert">{errorText(error)}</div>;
}

export function ChannelLabel({ channel }: { channel: Channel }) {
  return <span className={`chan chan-${channel}`}>{CHANNEL_NAMES[channel]}</span>;
}

const STATUS_TONE: Record<string, string> = {
  active: "ok", ready: "ok", completed: "ok", delivered: "ok", sent: "ok",
  draft: "", paused: "warn", building: "warn", new: "warn", waiting: "info",
  scheduled: "info", running: "warn", testing: "info", failed: "bad", cancelled: "",
};
// M7: one word per state everywhere (badges, filters): live things say Live, finished ones Finished.
export const STATUS_TEXT: Record<string, string> = {
  new: "Queued", building: "Building", ready: "Live", testing: "A/B test: waiting for winner", active: "Live", running: "Sending",
  completed: "Finished", draft: "Draft", scheduled: "Scheduled", paused: "Paused", cancelled: "Cancelled", archived: "Archived",
};

export function Status({ value }: { value: string }) {
  return <span className={`badge ${STATUS_TONE[value] ?? ""}`}>{STATUS_TEXT[value] ?? value[0].toUpperCase() + value.slice(1)}</span>;
}

// LiveNumber flashes briefly (amber) when its value changes: the app's signal that data is live.
// The first load isn't a change, so nothing flashes for ~1.5s after mount.
export function LiveNumber({ value }: { value: number }) {
  const prev = useRef(value);
  const mounted = useRef(Date.now());
  const [changed, setChanged] = useState(false);
  useEffect(() => {
    if (prev.current !== value) {
      prev.current = value;
      if (Date.now() - mounted.current < 1500) return;
      setChanged(true);
      const t = setTimeout(() => setChanged(false), 1200);
      return () => clearTimeout(t);
    }
  }, [value]);
  return <span className={`live-num${changed ? " changed" : ""}`}>{value.toLocaleString("en-IN")}</span>;
}

/** Modal: a dialog. It notices typing inside it and, while there are unsaved changes, asks before
 * closing by Esc, the backdrop, Close, or a footer Cancel / Close button (guard={false} turns that
 * off, e.g. for dialogs that only show something). Closing from code (after a save) never asks. */
export function Modal({ title, onClose, children, footer, wide, guard = true }: { title: string; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean; guard?: boolean }) {
  const { dirty, track, reset } = useDirtyRef();
  const tryClose = useCallback(() => {
    if (!guard || !dirty.current) return onClose();
    void ask(DISCARD).then((ok) => ok && onClose());
  }, [guard, dirty, onClose]);
  useEscape(tryClose);
  const ctx = useMemo(() => ({ saved: reset }), [reset]);
  const footClick = (e: MouseEvent) => {
    const b = (e.target as HTMLElement).closest("button");
    if (b && guard && dirty.current && isCloseButton(b.textContent ?? "", b.hasAttribute("data-close"))) {
      e.preventDefault();
      e.stopPropagation();
      tryClose();
    }
  };
  return (
    <>
      <div className="modal-backdrop" onClick={tryClose} />
      <div className={wide ? "modal wide" : "modal"} role="dialog" aria-modal="true" aria-label={title} {...(guard ? track : {})}>
        <div className="panel-head"><h3>{title}</h3><button className="btn ghost sm" onClick={tryClose} aria-label="Close">Close</button></div>
        <DialogCtx.Provider value={ctx}>
          <div className="panel-body">{children}</div>
          {footer && <div className="modal-foot" onClickCapture={footClick}>{footer}</div>}
        </DialogCtx.Provider>
      </div>
    </>
  );
}

/** Drawer: a side panel, with the same unsaved-changes question as Modal. */
export function Drawer({ title, subtitle, onClose, children, guard = true }: { title: ReactNode; subtitle?: ReactNode; onClose: () => void; children: ReactNode; guard?: boolean }) {
  const { dirty, track, reset } = useDirtyRef();
  const tryClose = useCallback(() => {
    if (!guard || !dirty.current) return onClose();
    void ask(DISCARD).then((ok) => ok && onClose());
  }, [guard, dirty, onClose]);
  useEscape(tryClose);
  const ctx = useMemo(() => ({ saved: reset }), [reset]);
  return (
    <>
      <div className="drawer-backdrop" onClick={tryClose} />
      <aside className="drawer" role="dialog" aria-modal="true" {...(guard ? track : {})}>
        <div className="drawer-head">
          <div><h2>{title}</h2>{subtitle && <p className="muted small">{subtitle}</p>}</div>
          <button className="btn ghost sm" onClick={tryClose}>Close</button>
        </div>
        <DialogCtx.Provider value={ctx}><div className="drawer-body">{children}</div></DialogCtx.Provider>
      </aside>
    </>
  );
}

function useEscape(fn: () => void) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === "Escape" && fn();
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [fn]);
}

// ---- toasts ----
type Toast = { text: string; error?: boolean };
const ToastCtx = createContext<(t: Toast) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null);
  const show = useCallback((t: Toast) => {
    setToast(t);
    window.setTimeout(() => setToast(null), 3200);
  }, []);
  return (
    <ToastCtx.Provider value={show}>
      {children}
      {toast && <div className={`toast${toast.error ? " error" : ""}`} role="status">{toast.text}</div>}
    </ToastCtx.Provider>
  );
}

export const useToast = () => useContext(ToastCtx);

/** copyText writes to the clipboard; it rejects (never throws) where the browser has no clipboard
 * (a page not on https) or refuses to write (no permission, the tab not focused). */
export function copyText(text: string): Promise<void> {
  try {
    return navigator.clipboard?.writeText ? navigator.clipboard.writeText(text) : Promise.reject(new Error("no clipboard"));
  } catch (e) {
    return Promise.reject(e);
  }
}

/** useCopy copies text to the clipboard and confirms with a toast (or says how to copy it by hand). */
export function useCopy() {
  const toast = useToast();
  return (text: string, what = "Copied") =>
    copyText(text).then(
      () => toast({ text: what }),
      () => toast({ text: `Couldn't copy. Select the text and copy it yourself${text.length <= 80 ? `: ${text}` : "."}`, error: true }),
    );
}

export function timeAgo(iso?: string): string {
  if (!iso) return "never";
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" });
}
