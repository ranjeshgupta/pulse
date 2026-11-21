// Public archive and RSS-to-email settings of a one-time or repeating campaign (O6): list its sends
// on the workspace's public archive page, and fill {{block.feed}} with what is new in a feed.
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { ErrorNote, Field } from "./ui";

export interface FeedItem { title: string; link: string; summary: string; published: string; image?: string }
export interface FeedPreview { title: string; total: number; items: FeedItem[]; html: string; text: string }

/** feedProblem checks the feed address as the server will. */
export function feedProblem(url: string | undefined, channel: string): string | null {
  const u = (url ?? "").trim();
  if (!u) return null;
  if (channel !== "email") return "RSS-to-email is for email campaigns.";
  if (!/^https?:\/\/[^/\s]+/i.test(u)) return "Give the feed's full address, starting with https://";
  return null;
}

/** sinceLabel says how far back a feed campaign's first run looks. */
export function sinceLabel(repeat: string): string {
  return { daily: "the last day", weekly: "the last week", monthly: "the last month" }[repeat] ?? "the feed (its 10 newest items)";
}

export function FeedFields({ channel, repeat, archive, feedURL, onChange }: {
  channel: string; repeat: string; archive: boolean; feedURL: string;
  onChange: (p: { archive_public?: boolean; feed_url?: string }) => void;
}) {
  const base = useQuery({ queryKey: ["landing-pages"], queryFn: () => api.get<{ base_url: string }>("/v1/landing-pages"), staleTime: 300_000, retry: false }).data?.base_url;
  const preview = useMutation({ mutationFn: () => api.post<FeedPreview>("/v1/broadcasts/feed-preview", { url: feedURL.trim(), repeat }) });
  if (channel !== "email") return null;
  const problem = feedProblem(feedURL, channel);
  return (
    <>
      <h3 style={{ margin: "18px 0 12px" }}>Archive and feed</h3>
      <label className="check">
        <input type="checkbox" checked={archive} onChange={(e) => onChange({ archive_public: e.target.checked })} />
        🗂️ Show its sends in the public archive
      </label>
      {archive && (
        <p className="xs muted" style={{ marginTop: -6, maxWidth: "75ch" }}>
          Anyone can read them at {base ? <a href={`${base}archive`} target="_blank" rel="noreferrer">{base}archive</a> : "your pages address /archive"} (with an RSS feed), as they'd look to a contact with no details filled in: write the template so it reads well that way.
        </p>
      )}
      <Field label="Content from an RSS feed (optional)" hint={`Each send lists what's new in the feed since the last one, where the template has {{block.feed}}. Nothing new: that send is skipped. The first send looks back over ${sinceLabel(repeat)}.`}>
        <div className="row" style={{ flexWrap: "nowrap", maxWidth: 640 }}>
          <input className="input" type="url" inputMode="url" placeholder="https://blog.example.com/feed.xml" value={feedURL}
            onChange={(e) => { onChange({ feed_url: e.target.value }); preview.reset(); }} />
          <button type="button" className="btn" disabled={!feedURL.trim() || !!problem || preview.isPending} onClick={() => preview.mutate()}>
            {preview.isPending ? "Reading…" : "Preview"}
          </button>
        </div>
      </Field>
      {problem && <div className="alert error">{problem}</div>}
      <ErrorNote error={preview.error} />
      {preview.data && (
        <div className="panel" style={{ padding: 14, maxWidth: 640 }} aria-live="polite">
          <p className="small" style={{ margin: "0 0 8px" }}>
            <strong>{preview.data.title || "Feed"}</strong> · {preview.data.total} items in the feed, {preview.data.items.length} would go in the next send
          </p>
          {preview.data.items.length === 0 ? <p className="small muted" style={{ margin: 0 }}>Nothing new in {sinceLabel(repeat)}: the next send would be skipped.</p> : (
            <ul className="small" style={{ margin: 0, paddingLeft: 18 }}>
              {preview.data.items.map((it, i) => (
                <li key={i}>{it.link ? <a href={it.link} target="_blank" rel="noreferrer">{it.title}</a> : it.title}{it.published && !it.published.startsWith("0001") ? <span className="faint"> · {new Date(it.published).toLocaleDateString()}</span> : null}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </>
  );
}
