// Spam check and inbox preview for an email template (O6): a content score with what to fix, and
// how the subject and first line look in an inbox list.
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { ErrorNote, Modal } from "../ui";

export interface SpamResult {
  score: number; rating: "good" | "risky" | "spam";
  issues: { rule: string; points: number; title: string; fix: string }[];
  preheader: string; html_bytes: number; links: number; images: number;
}

const RATING = {
  good: { label: "Looks good", icon: "✅", hint: "Nothing here should send it to spam. Sender reputation and authentication matter too: check your sending domain under Channels." },
  risky: { label: "Risky", icon: "⚠️", hint: "Some filters may file it as spam or Promotions. Fix the items below." },
  spam: { label: "Likely spam", icon: "🚫", hint: "Most filters would flag this. Fix the items below before sending." },
} as const;

/** meterPct places a score on the meter (0–10 points). */
export const meterPct = (score: number) => Math.max(2, Math.min(100, (score / 10) * 100));

export function SpamCheckModal({ subject, html, text, onClose }: { subject: string; html: string; text: string; onClose: () => void }) {
  const q = useQuery({ queryKey: ["spam-check", subject, html, text], queryFn: () => api.post<SpamResult>("/v1/templates/spam-check", { subject, html, text }) });
  const r = q.data;
  return (
    <Modal wide title="Spam check and inbox preview" onClose={onClose} guard={false}>
      <ErrorNote error={q.error} />
      {q.isLoading && <p className="muted small">Checking…</p>}
      {r && (
        <>
          <div className="sc-inbox" aria-label="Inbox preview">
            <div className="sc-avatar" aria-hidden="true">A</div>
            <div className="sc-line"><strong>Your brand</strong><span className="sc-subject">{subject || "(no subject)"}</span><span className="sc-pre"> — {r.preheader || "(no text)"}</span></div>
            <span className="xs muted">now</span>
          </div>
          <div className={`sc-score sc-${r.rating}`}>
            <div className="sc-head"><span className="sc-big">{RATING[r.rating].icon} {RATING[r.rating].label}</span><span className="small muted">{r.score} points · under 3 is good</span></div>
            <div className="sc-meter" role="meter" aria-valuemin={0} aria-valuemax={10} aria-valuenow={r.score}><span style={{ width: `${meterPct(r.score)}%` }} /></div>
            <p className="small" style={{ margin: "6px 0 0" }}>{RATING[r.rating].hint}</p>
          </div>
          {r.issues.length > 0 && (
            <ul className="dc-list">
              {r.issues.sort((a, b) => b.points - a.points).map((i) => (
                <li key={i.rule + i.title} className={i.points >= 2 ? "dc-fail" : "dc-warn"}>
                  <div><strong>{i.title}</strong> <span className="xs muted">+{i.points}</span></div>
                  <p className="small" style={{ margin: "4px 0 0" }}>{i.fix}</p>
                </li>
              ))}
            </ul>
          )}
          <p className="xs muted">{(r.html_bytes / 1024).toFixed(1)} KB of HTML (Gmail clips over 102 KB) · {r.links} links · {r.images} images</p>
        </>
      )}
    </Modal>
  );
}
