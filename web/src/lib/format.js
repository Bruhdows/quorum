// Pure display formatting, shared by the board and the detail page.

export function timeAgo(iso) {
  if (!iso) return 'never';
  const diff = Math.max(0, Date.now() - new Date(iso).getTime());
  const s = Math.round(diff / 1000);
  if (s < 60) return `${s} second${s === 1 ? '' : 's'} ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} minute${m === 1 ? '' : 's'} ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h} hour${h === 1 ? '' : 's'} ago`;
  const d = Math.round(h / 24);
  return `${d} day${d === 1 ? '' : 's'} ago`;
}

export function clockTime(iso) {
  return new Date(iso).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// Day labels for the 90-day history strip ("23 Jun 2026" and "23 Jun").
export function dayDate(iso) {
  return new Date(iso).toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  });
}

export function dayDateShort(iso) {
  return new Date(iso).toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'short',
  });
}

// Group names come from services.yaml, where they read as slugs like
// "game-servers". Shown as-is they'd shout in caps like ids, so title-case
// them for the heading.
export function groupLabel(name) {
  return (name || '')
    .replace(/[-_]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

export function formatLatency(ms) {
  return ms == null ? null : `${ms} ms`;
}

export function formatUptime(pct) {
  return pct == null ? 'No uptime data' : `${pct.toFixed(2)} % uptime`;
}
