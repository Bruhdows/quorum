import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react';

const H = 240;
const PAD = { top: 10, right: 8, bottom: 22, left: 34 };

// Just enough headroom that the peak does not touch the top edge. Rounding
// up to the next power of ten instead would leave a third of the plot empty
// whenever the peak sat just above a round number.
function axisTop(peak) {
  if (peak <= 0) return 10;
  return peak * 1.08;
}

function formatTime(iso, span) {
  const d = new Date(iso);
  if (span === '1h' || span === '24h') {
    return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  }
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// One series of average latency over time, drawn as an area with a 2px line.
// A crosshair follows the pointer because a line chart without one makes the
// reader guess which x a y belongs to.
export default function LatencyChart({ points, rangeKey }) {
  const [hover, setHover] = useState(null);
  const [width, setWidth] = useState(0);
  const svg = useRef(null);
  const box = useRef(null);
  // A static id would collide if two charts ever shared a page.
  const gradientId = useId().replace(/[^a-zA-Z0-9_-]/g, 'g');

  // The chart is drawn at the container's real pixel width. A fixed viewBox
  // scaled to fit would letterbox. Uniform scaling leaves a gap on both
  // sides whenever the container is wider than the viewBox.
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return undefined;
    const observer = new ResizeObserver(([entry]) => {
      setWidth(Math.round(entry.contentRect.width));
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  // Hover coordinates belong to the series that produced them.
  useEffect(() => setHover(null), [rangeKey, points]);

  const W = width || 720;

  const plotted = useMemo(() => points.filter((p) => p.avg_ms >= 0), [points]);

  const { path, area, max, xy } = useMemo(() => {
    if (plotted.length === 0) return { path: '', area: '', max: 0, xy: [] };
    const top = axisTop(Math.max(...plotted.map((p) => p.avg_ms)));
    const innerW = W - PAD.left - PAD.right;
    const innerH = H - PAD.top - PAD.bottom;
    const step = plotted.length > 1 ? innerW / (plotted.length - 1) : 0;

    const coords = plotted.map((p, i) => ({
      x: PAD.left + (plotted.length > 1 ? i * step : innerW / 2),
      y: PAD.top + innerH - (p.avg_ms / top) * innerH,
      point: p,
    }));

    const line = coords.map((c, i) => `${i === 0 ? 'M' : 'L'}${c.x.toFixed(2)},${c.y.toFixed(2)}`).join(' ');
    const base = PAD.top + innerH;
    const fill = `${line} L${coords[coords.length - 1].x.toFixed(2)},${base} L${coords[0].x.toFixed(2)},${base} Z`;
    return { path: line, area: fill, max: top, xy: coords };
  }, [plotted, W]);

  function onMove(e) {
    if (xy.length === 0) return;
    const rect = svg.current.getBoundingClientRect();
    const x = e.clientX - rect.left;
    let best = xy[0];
    for (const c of xy) {
      if (Math.abs(c.x - x) < Math.abs(best.x - x)) best = c;
    }
    setHover(best);
  }

  const gridLines = [0, 0.5, 1];

  return (
    <div className="relative" ref={box}>
      {plotted.length === 0 ? (
        <p className="flex h-[240px] items-center justify-center text-sm text-muted">
          No successful checks in this range.
        </p>
      ) : (
      <svg
        ref={svg}
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="block h-[240px] w-full touch-none"
        onPointerMove={onMove}
        onPointerLeave={() => setHover(null)}
        role="img"
        aria-label={`Average response time, ${rangeKey}`}
      >
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--chart)" stopOpacity="0.28" />
            <stop offset="100%" stopColor="var(--chart)" stopOpacity="0" />
          </linearGradient>
        </defs>

        {gridLines.map((f) => {
          const y = PAD.top + (H - PAD.top - PAD.bottom) * f;
          return (
            <g key={f}>
              <line x1={PAD.left} x2={W - PAD.right} y1={y} y2={y} stroke="var(--border)" strokeWidth="1" />
              <text x={PAD.left - 8} y={y + 4} textAnchor="end" className="fill-[var(--fg-faint)] text-[11px]">
                {Math.round(max * (1 - f))}
              </text>
            </g>
          );
        })}

        <path d={area} fill={`url(#${gradientId})`} />
        <path d={path} fill="none" stroke="var(--chart)" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />

        {hover && (
          <g>
            <line
              x1={hover.x}
              x2={hover.x}
              y1={PAD.top}
              y2={H - PAD.bottom}
              stroke="var(--fg-faint)"
              strokeWidth="1"
              strokeDasharray="3 3"
            />
            <circle cx={hover.x} cy={hover.y} r="5" fill="var(--chart)" stroke="var(--card)" strokeWidth="2" />
          </g>
        )}

        <text x={PAD.left} y={H - 6} className="fill-[var(--fg-faint)] text-[11px]">
          {formatTime(plotted[0].t, rangeKey)}
        </text>
        <text x={W - PAD.right} y={H - 6} textAnchor="end" className="fill-[var(--fg-faint)] text-[11px]">
          {formatTime(plotted[plotted.length - 1].t, rangeKey)}
        </text>
      </svg>
      )}

      {hover && plotted.length > 0 && (
        <div
          className="pointer-events-none absolute -translate-x-1/2 -translate-y-full rounded-md border border-border bg-card px-2.5 py-1.5 text-xs shadow-popover"
          // Keep the bubble on the chart near the edges instead of letting
          // it hang off the side.
          style={{ left: `${Math.min(Math.max(hover.x, 72), W - 72)}px`, top: `${hover.y - 8}px` }}
        >
          <div className="font-medium">{hover.point.avg_ms} ms</div>
          <div className="text-muted">
            {new Date(hover.point.t).toLocaleString(undefined, {
              month: 'short',
              day: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
            })}
          </div>
        </div>
      )}
    </div>
  );
}
