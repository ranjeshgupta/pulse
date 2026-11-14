// The provider a campaign, step or test send goes through: the channel's default unless another
// of its providers is chosen. Hidden when the channel has only one provider (nothing to choose).
import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { api, CHANNEL_NAMES, type Channel, type Provider } from "./api";
import { Field } from "./ui";

export function useChannelProviders(channel: Channel) {
  const q = useQuery({ queryKey: ["providers"], queryFn: () => api.get<{ providers: Provider[] }>("/v1/channels/providers"), staleTime: 60_000, retry: false });
  return { list: (q.data?.providers ?? []).filter((p) => p.channel === channel), loaded: q.isSuccess };
}

/** providerOptions: the default first (as value 0), then the others by name. */
export function providerOptions(list: Pick<Provider, "id" | "name" | "is_default">[]): { value: number; label: string }[] {
  const def = list.find((p) => p.is_default);
  return [
    { value: 0, label: def ? `${def.name} (default)` : "The channel's default" },
    ...list.filter((p) => !p.is_default).sort((a, b) => a.name.localeCompare(b.name)).map((p) => ({ value: p.id, label: p.name })),
  ];
}

export function ProviderPick({ channel, value, onChange, compact }: { channel: Channel; value?: number; onChange: (id: number) => void; compact?: boolean }) {
  const { list, loaded } = useChannelProviders(channel);
  const opts = providerOptions(list);
  // A provider of another channel (the channel changed) or removed since: back to the default.
  const stale = loaded && !!value && !opts.some((o) => o.value === value);
  useEffect(() => { if (stale) onChange(0); }, [stale]); // eslint-disable-line react-hooks/exhaustive-deps
  if (list.length < 2 && !value) return null;
  const v = stale ? 0 : value ?? 0;
  const select = (
    <select className={compact ? "select j-full" : "select"} aria-label={`${CHANNEL_NAMES[channel]} provider`} value={v} onChange={(e) => onChange(Number(e.target.value))}>
      {opts.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  );
  if (compact) return <label className="j-fallback"><span className="xs muted">Send through</span>{select}</label>;
  return <Field label="Send through" hint={`Which ${CHANNEL_NAMES[channel]} provider delivers it. The default is preselected.`}>{select}</Field>;
}
