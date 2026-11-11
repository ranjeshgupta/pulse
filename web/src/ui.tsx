// Small shared building blocks. Styling lives in styles.css.
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { CHANNEL_NAMES, type Channel } from "./api";

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

export function ErrorNote({ error }: { error: unknown }) {
  if (!error) return null;
  return <div className="alert error" role="alert">{error instanceof Error ? error.message : String(error)}</div>;
}

export function ChannelLabel({ channel }: { channel: Channel }) {
  return <span className={`chan chan-${channel}`}>{CHANNEL_NAMES[channel]}</span>;
}

const STATUS_TONE: Record<string, string> = {
  active: "ok", ready: "ok", completed: "ok", delivered: "ok", sent: "ok",
  draft: "", paused: "warn", building: "warn", new: "warn", waiting: "info",
  scheduled: "info", running: "warn", testing: "info", failed: "bad", cancelled: "",
};
const STATUS_TEXT: Record<string, string> = { new: "Queued", building: "Building", ready: "Live", testing: "A/B testing" };

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

export function Modal({ title, onClose, children, footer }: { title: string; onClose: () => void; children: ReactNode; footer?: ReactNode }) {
  useEscape(onClose);
  return (
    <>
      <div className="modal-backdrop" onClick={onClose} />
      <div className="modal" role="dialog" aria-modal="true" aria-label={title}>
        <div className="panel-head"><h3>{title}</h3><button className="btn ghost sm" onClick={onClose}>Close</button></div>
        <div className="panel-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </>
  );
}

export function Drawer({ title, subtitle, onClose, children }: { title: ReactNode; subtitle?: ReactNode; onClose: () => void; children: ReactNode }) {
  useEscape(onClose);
  return (
    <>
      <div className="drawer-backdrop" onClick={onClose} />
      <aside className="drawer" role="dialog" aria-modal="true">
        <div className="drawer-head">
          <div><h2>{title}</h2>{subtitle && <p className="muted small">{subtitle}</p>}</div>
          <button className="btn ghost sm" onClick={onClose}>Close</button>
        </div>
        <div className="drawer-body">{children}</div>
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

/** useCopy copies text to the clipboard and confirms with a toast (or says how to copy it by hand). */
export function useCopy() {
  const toast = useToast();
  return (text: string, what = "Copied") =>
    navigator.clipboard.writeText(text).then(
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
