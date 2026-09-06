import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowDownWideNarrow,
  Check,
  ChevronRight,
  CircleCheck,
  CircleHelp,
  RefreshCw,
  Search,
  TriangleAlert,
  Zap,
} from 'lucide-react';
import { toast } from 'sonner';
import Dropdown from './Dropdown.jsx';
import Tooltip from './Tooltip.jsx';
import { SEVERITY, STATUS_LABEL, StatusPill } from './status.jsx';
import { dayDate, dayDateShort, formatLatency, formatUptime, groupLabel, timeAgo } from '../lib/format.js';
import { useNow } from '../hooks/useNow.js';
import { usePoll } from '../hooks/usePoll.js';
import { useMediaQuery } from '../hooks/useMediaQuery.js';
import { GhostButton } from './ui/button.jsx';
import { ErrorBanner, PageSkeleton } from './ui/feedback.jsx';

const BAR_BG = {
  up: 'bg-success',
  down: 'bg-danger',
  degraded: 'bg-warning',
};

// A local fetch can finish in 30ms, which reads as a flicker rather than as
// feedback. Rather than hold the button hostage for a fixed delay, wait a
// moment before showing a spinner at all. A fast refresh jumps straight to
// the confirmation, and only a genuinely slow one spins.
const SPINNER_DELAY_MS = 180;
const CONFIRM_MS = 900;
const MOBILE_DAYS = 30;

const CHANGE_TOAST = {
  down: { notify: toast.error, verb: 'went down', detail: 'Every checker that reported failed to reach it.' },
  up: { notify: toast.success, verb: 'recovered', detail: 'A checker reached it again.' },
  unknown: { notify: toast.warning, verb: 'stopped reporting', detail: 'No agent has checked in recently.' },
};

const SORT_OPTIONS = [
  { value: 'default', label: 'Default' },
  { value: 'name', label: 'Name' },
  { value: 'status', label: 'Status' },
  { value: 'uptime', label: 'Uptime' },
];

// One bar per day over the trailing window, 90 days by default to match
// retention_days. Days with no checks at all stay blank. A day where some
// checks failed but others succeeded reads as degraded rather than down.
// A grid of divs rather than SVG rects keeps the corner radius in real
// pixels at any card width.
function HistoryBars({ history, days }) {
  const bars = new Array(days).fill(null);
  const start = Math.max(0, days - history.length);
  history.slice(-days).forEach((p, i) => { bars[start + i] = p; });

  return (
    <div
      className="grid h-9 gap-[2px]"
      style={{ gridTemplateColumns: `repeat(${days}, minmax(0, 1fr))` }}
    >
      {bars.map((point, i) => (
        <Tooltip
          key={i}
          className="h-full w-full"
          label={point ? `${dayDate(point.t)} · ${STATUS_LABEL[point.status] || 'Unknown'}` : 'No data'}
        >
          <span
            className={`h-full w-full rounded-[3px] transition-opacity hover:opacity-60 ${BAR_BG[point?.status] || 'bg-subtle'}`}
          />
        </Tooltip>
      ))}
    </div>
  );
}

// A hairline that fills the space between the timeline captions, like the
// rules under a status page's uptime strip.
function Rule() {
  return <span className="h-px flex-1 bg-border" />;
}

function ServiceRow({ service, days, fullDays, onOpen, first, last }) {
  const status = service.status || 'unknown';
  const latency = formatLatency(service.latency_ms);
  const uptime = formatUptime(service.uptime_pct);
  // `days` is the visible window (narrowed on phones), so the caption
  // tracks what the strip shows. The uptime value still covers the full
  // window, hence the separate fullDays.
  const visible = (service.history || []).slice(-days);
  const oldest = visible.length ? visible[0].t : null;

  return (
    <a
      href={`?service=${encodeURIComponent(service.id)}`}
      onClick={(e) => {
        // Leave modified clicks to the browser so "open in new tab" still works.
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
        e.preventDefault();
        onOpen(service.id);
      }}
      className={`group/row block px-4 py-4 transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-success/40 sm:px-5 sm:py-5 ${first ? 'rounded-t-xl' : ''} ${last ? 'rounded-b-xl' : ''}`}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h4 className="flex items-center gap-1 truncate font-medium">
            {service.name}
            <ChevronRight className="h-4 w-4 shrink-0 text-faint transition-transform group-hover/row:translate-x-0.5" />
          </h4>
          <p className="mt-0.5 truncate font-mono text-xs text-faint">{service.target}</p>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          {latency && (
            <Tooltip label="Fastest agent's latest check">
              <span className="hidden items-center gap-1 text-xs tabular-nums text-muted sm:inline-flex">
                <Zap className="h-3.5 w-3.5" />
                {latency}
              </span>
            </Tooltip>
          )}
          <StatusPill status={status} lastChecked={service.last_checked} />
        </div>
      </div>

      <div className="mt-4">
        <HistoryBars history={service.history || []} days={days} />
      </div>

      <div className="mt-2 flex items-center gap-3 text-[11px] text-faint">
        <span className="whitespace-nowrap">{oldest ? dayDateShort(oldest) : 'no history'}</span>
        <Rule />
        <Tooltip label={`Share of checks that succeeded, last ${fullDays ?? days} days`}>
          <span className="whitespace-nowrap tabular-nums text-muted">{uptime}</span>
        </Tooltip>
        <Rule />
        <span className="whitespace-nowrap">Today</span>
      </div>
    </a>
  );
}

function OverallBanner({ services }) {
  const total = services.length;
  const down = services.filter((s) => s.status === 'down').length;
  const degraded = services.filter((s) => s.status === 'degraded').length;
  const unknown = services.filter((s) => s.status === 'unknown').length;
  const up = total - down - degraded - unknown;

  let tone = 'border-success/25 bg-success/5 text-success';
  let title = total === 1 ? 'The one service is up' : `All ${total} services are up`;
  let detail = 'Every checker is reporting success.';
  let BannerIcon = CircleCheck;

  if (down > 0) {
    tone = 'border-danger/25 bg-danger/5 text-danger';
    title = `${down} of ${total} services ${down === 1 ? 'is' : 'are'} down`;
    detail = 'Every checker that reported recently failed to reach it.';
    BannerIcon = TriangleAlert;
  } else if (degraded > 0) {
    tone = 'border-warning/25 bg-warning/5 text-warning';
    title = `${degraded} of ${total} services ${degraded === 1 ? 'is' : 'are'} degraded`;
    detail = 'Some checks failed recently, but at least one checker is getting through.';
    BannerIcon = TriangleAlert;
  } else if (unknown > 0) {
    tone = 'border-warning/25 bg-warning/5 text-warning';
    title = `${unknown} of ${total} services ${unknown === 1 ? 'has' : 'have'} no recent checks`;
    detail = 'No agent has reported in a while, so the state is unknown rather than down.';
    BannerIcon = CircleHelp;
  }

  return (
    <div className={`flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border px-4 py-4 sm:px-5 ${tone}`}>
      <BannerIcon className="h-5 w-5 shrink-0" />
      <div className="min-w-0">
        <p className="text-sm font-semibold">{title}</p>
        <p className="mt-0.5 text-xs text-muted">{detail}</p>
      </div>
      <div className="ml-auto flex items-center gap-3 text-xs text-muted">
        <span className="flex items-center gap-1.5 whitespace-nowrap"><span className="h-2 w-2 rounded-full bg-success" />{up} up</span>
        <span className="flex items-center gap-1.5 whitespace-nowrap"><span className="h-2 w-2 rounded-full bg-danger" />{down} down</span>
        <span className="flex items-center gap-1.5 whitespace-nowrap"><span className="h-2 w-2 rounded-full bg-warning" />{degraded} degraded</span>
        <span className="flex items-center gap-1.5 whitespace-nowrap"><span className="h-2 w-2 rounded-full bg-unknown" />{unknown} unknown</span>
      </div>
    </div>
  );
}

export default function StatusBoard({ refreshMs, onOpen }) {
  const [search, setSearch] = useState('');
  const [sortBy, setSortBy] = useState('default');
  const [refreshing, setRefreshing] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  // Relative timestamps ("12 seconds ago") only move if something rerenders.
  useNow();

  const lastStatuses = useRef(null);
  const wasFailing = useRef(false);
  const spinnerTimer = useRef(null);
  const confirmTimer = useRef(null);
  useEffect(() => () => {
    clearTimeout(spinnerTimer.current);
    clearTimeout(confirmTimer.current);
  }, []);

  // Toasts fire on the edges only. Raising one every poll while the API is
  // down would bury the page in copies of the same news.
  const reportChanges = useCallback((json) => {
    const current = new Map();
    for (const g of json.groups || []) {
      for (const s of g.services || []) current.set(s.id, { name: s.name, status: s.status });
    }

    const previous = lastStatuses.current;
    lastStatuses.current = current;
    if (!previous) return; // First load is the baseline, not news.

    for (const [id, now] of current) {
      const before = previous.get(id);
      if (!before || before.status === now.status) continue;
      const change = CHANGE_TOAST[now.status];
      if (!change) continue;
      change.notify(`${now.name} ${change.verb}`, { description: change.detail });
    }
  }, []);

  const handleData = useCallback((json) => {
    reportChanges(json);
    if (wasFailing.current) {
      wasFailing.current = false;
      toast.success('Status API is reachable again', { description: 'Live updates have resumed.' });
    }
  }, [reportChanges]);

  const handleError = useCallback((message, manual) => {
    // An explicit click always gets an answer. Background polls only
    // announce the first failure of a run.
    if (manual || !wasFailing.current) {
      toast.error(manual ? 'Refresh failed' : 'Cannot reach the status API', {
        description: `${message}. Retrying every ${Math.round(refreshMs / 1000)} seconds.`,
      });
    }
    wasFailing.current = true;
  }, [refreshMs]);

  const { data, error, updatedAt, reload } = usePoll('/api/status', refreshMs, {
    onData: handleData,
    onError: handleError,
  });

  async function manualRefresh() {
    // Only a click drives the button's state. A background poll that flipped
    // it to "Refreshing…" every few seconds would be its own kind of glitchy.
    setConfirmed(false);
    clearTimeout(spinnerTimer.current);
    spinnerTimer.current = setTimeout(() => setRefreshing(true), SPINNER_DELAY_MS);
    try {
      const ok = await reload({ manual: true });
      if (ok) {
        setConfirmed(true);
        clearTimeout(confirmTimer.current);
        confirmTimer.current = setTimeout(() => setConfirmed(false), CONFIRM_MS);
      }
    } finally {
      clearTimeout(spinnerTimer.current);
      setRefreshing(false);
    }
  }

  const allServices = useMemo(
    () => (data ? data.groups.flatMap((g) => g.services || []) : []),
    [data],
  );

  const groups = useMemo(() => {
    if (!data) return [];
    const term = search.trim().toLowerCase();
    return data.groups
      .map((g) => {
        // The index keeps services.yaml's hand-written order as the tiebreak,
        // so equal rows never shuffle between polls.
        const services = (g.services || [])
          .filter((s) => s.name.toLowerCase().includes(term) || (s.target || '').toLowerCase().includes(term))
          .map((s, i) => ({ s, i }))
          .sort((a, b) => {
            if (sortBy === 'name') return a.s.name.localeCompare(b.s.name);
            if (sortBy === 'uptime') return (a.s.uptime_pct ?? 101) - (b.s.uptime_pct ?? 101) || a.i - b.i;
            // Default and status both put problems first. Default then falls
            // back to config order, status sorts healthy services by name.
            const bySeverity = (SEVERITY[a.s.status] ?? 2) - (SEVERITY[b.s.status] ?? 2);
            if (bySeverity !== 0) return bySeverity;
            return sortBy === 'status' ? a.s.name.localeCompare(b.s.name) : a.i - b.i;
          })
          .map(({ s }) => s);
        return { name: g.name, services };
      })
      .filter((g) => g.services.length > 0);
  }, [data, search, sortBy]);

  // The API reports how many days the strip covers, since it follows
  // retention_days. Until the first poll lands, assume 90. Phones show
  // the trailing 30 instead; 90 bars won't fit in 300px.
  const days = data?.uptime_days || 90;
  const isDesktop = useMediaQuery('(min-width: 640px)');
  const visibleDays = isDesktop ? days : Math.min(days, MOBILE_DAYS);

  return (
    <div className="space-y-6">
      {error && (
        <ErrorBanner>
          Cannot reach the status API ({error}). Retrying every {Math.round(refreshMs / 1000)} seconds.
        </ErrorBanner>
      )}

      {data && <OverallBanner services={allServices} />}

      <div className="flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-faint" />
          <input
            type="search"
            placeholder="Search services"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            // 16px on phones: iOS Safari zooms into anything smaller.
            className="h-10 w-full rounded-lg border border-border bg-card pl-10 pr-3.5 text-base text-foreground placeholder:text-faint focus:outline-none focus-visible:ring-2 focus-visible:ring-success/40 sm:h-9 sm:text-sm"
          />
        </div>
        <Dropdown
          value={sortBy}
          options={SORT_OPTIONS}
          onChange={setSortBy}
          icon={ArrowDownWideNarrow}
          label="Sort services"
          className="w-full sm:w-auto"
          triggerClassName="w-full justify-between sm:w-auto sm:justify-start"
        />
      </div>

      {!data && !error && <PageSkeleton />}
      {data && groups.length === 0 && (
        <p className="rounded-xl border border-border bg-card py-16 text-center text-sm text-muted">
          No services match "{search}".
        </p>
      )}

      {groups.map((g) => (
        <section key={g.name}>
          <h3 className="mb-2 px-1 text-sm font-medium text-muted">{groupLabel(g.name)}</h3>
          <div className="divide-y divide-border rounded-xl border border-border bg-card">
            {g.services.map((s, i) => (
              <ServiceRow
                service={s}
                days={visibleDays}
                fullDays={days}
                key={s.id}
                onOpen={onOpen}
                first={i === 0}
                last={i === g.services.length - 1}
              />
            ))}
          </div>
        </section>
      ))}

      <div className="flex flex-wrap items-center justify-between gap-3 pt-2 text-xs text-muted">
        <div className="flex flex-wrap items-center gap-4">
          <span className="flex items-center gap-1.5"><span className="h-2 w-2 rounded-[3px] bg-success" /> Operational</span>
          <span className="flex items-center gap-1.5"><span className="h-2 w-2 rounded-[3px] bg-danger" /> Down</span>
          <span className="flex items-center gap-1.5"><span className="h-2 w-2 rounded-[3px] bg-warning" /> Degraded</span>
          <span className="flex items-center gap-1.5"><span className="h-2 w-2 rounded-[3px] bg-unknown" /> No data</span>
        </div>
        <Tooltip label="Fetch the latest results now" side="top">
          <GhostButton
            onClick={manualRefresh}
            disabled={refreshing}
            className="gap-1.5 text-xs text-muted disabled:cursor-wait"
          >
            {confirmed
              ? <Check className="h-3.5 w-3.5 text-success" />
              : <RefreshCw className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`} />}
            {refreshing && 'Refreshing…'}
            {!refreshing && confirmed && 'Up to date'}
            {!refreshing && !confirmed && (updatedAt ? `Updated ${timeAgo(updatedAt)}` : 'Waiting for first update')}
          </GhostButton>
        </Tooltip>
      </div>
    </div>
  );
}
