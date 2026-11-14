// The subscription topic of a campaign or journey step (Settings → Bounces & unsubscribes): its
// unsubscribers are skipped, and the unsubscribe link in its emails leaves that topic only.
// Hidden when the workspace has no topics.
import { useQuery } from "@tanstack/react-query";
import { api, type UnsubscribeSettings } from "./api";
import { Field } from "./ui";

export function useTopics() {
  return useQuery({ queryKey: ["unsubscribe-topics"], queryFn: () => api.get<UnsubscribeSettings>("/v1/unsubscribe-topics"), staleTime: 60_000, retry: false });
}

export function TopicPick({ value, onChange, compact, disabled }: { value?: string; onChange: (topic: string) => void; compact?: boolean; disabled?: boolean }) {
  const topics = useTopics().data?.topics ?? [];
  if (topics.length === 0 && !value) return null;
  const known = !value || topics.some((t) => t.key === value);
  const select = (
    <select className={compact ? "select j-full" : "select"} aria-label="Topic" value={value ?? ""} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
      <option value="">No topic</option>
      {topics.map((t) => <option key={t.key} value={t.key}>{t.name}</option>)}
      {!known && <option value={value}>{value} (removed)</option>}
    </select>
  );
  if (compact) return <label className="j-fallback"><span className="xs muted">Topic</span>{select}</label>;
  return (
    <Field label="Topic" hint="People who unsubscribed from this topic are skipped, and the unsubscribe link in it leaves this topic only.">
      {select}
    </Field>
  );
}
