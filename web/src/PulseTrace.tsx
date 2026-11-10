// The signature element: event volume drawn as a signal trace.
export function PulseTrace({ values, className = "pulse-trace" }: { values: number[]; className?: string }) {
  const w = 600, h = 120, pad = 6;
  const pts = values.length > 1 ? values : [0, 0];
  const max = Math.max(1, ...pts);
  const step = (w - pad * 2) / (pts.length - 1);
  const coords = pts.map((v, i) => [pad + i * step, h - pad - (v / max) * (h - pad * 2)] as const);
  const line = coords.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  const area = `${line} L${coords[coords.length - 1][0].toFixed(1)},${h} L${pad},${h} Z`;
  const [lx, ly] = coords[coords.length - 1];
  return (
    <svg className={className} viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" role="img" aria-label="Event volume over time">
      <defs>
        <linearGradient id="pulse-fill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#F0A202" stopOpacity="0.28" />
          <stop offset="1" stopColor="#F0A202" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill="url(#pulse-fill)" />
      <path d={line} fill="none" stroke="#F0A202" strokeWidth="2.2" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
      <circle cx={lx} cy={ly} r="4" fill="#F0A202" />
    </svg>
  );
}

// A synthetic heartbeat for the sign-in screen (no data before login).
export const HEARTBEAT = [3, 3, 4, 3, 3, 9, 2, 14, 3, 4, 3, 3, 5, 3, 3, 10, 1, 16, 3, 4, 3, 3, 4, 3, 3];
