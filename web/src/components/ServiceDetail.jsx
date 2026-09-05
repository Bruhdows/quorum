import { useEffect, useMemo, useState } from 'react';
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  CircleHelp,
  Clock,
  Gauge,
} from 'lucide-react';
import LatencyChart from './LatencyChart.jsx';
import { STATUS_ICON, STATUS_LABEL, StatusPill } from './status.jsx';
import { clockTime, timeAgo } from '../lib/format.js';
import { useNow } from '../hooks/useNow.js';
import { usePoll } from '../hooks/usePoll.js';
import { Card, SectionTitle } from './ui/card.jsx';
import { GhostButton } from './ui/button.jsx';
import { ErrorBanner } from './ui/feedback.jsx';
import { Modal } from './ui/modal.jsx';

const CHECKS_PER_PAGE = 10;

function Stat({ icon: Icon, label, value, tone = '' }) {
  return (
    <Card className="px-4 py-3.5">
      <div className="flex items-center gap-1.5 text-xs text-muted">
        <Icon className="h-3.5 w-3.5" />
        {label}
      </div>
      <div className={`mt-1.5 text-lg font-semibold ${tone}`}>{value}</div>
    </Card>
  );
}

function UptimeBar({ pct }) {
  const value = pct == null ? 0 : pct;
  const tone = value >= 99 ? 'bg-success' : value >= 95 ? 'bg-warning' : 'bg-danger';
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-subtle">
      <div className={`h-full rounded-full ${tone}`} style={{ width: `${value}%` }} />
    </div>
  );
}

function CheckDetails({ check }) {
  return (
    <dl className="space-y-3.5 text-sm">
      <div className="flex items-center gap-2">
        {check.success
          ? <CircleCheck className="h-5 w-5 shrink-0 text-success" />
          : <CircleAlert className="h-5 w-5 shrink-0 text-danger" />}
        <dt className="sr-only">Result</dt>
        <dd className="font-medium">{check.success ? 'Check succeeded' : 'Check failed'}</dd>
      </div>
      <div className="grid grid-cols-[5.5rem_1fr] gap-x-3 gap-y-3">
        <dt className="text-muted">Time</dt>
        <dd>{clockTime(check.t)} · {timeAgo(check.t)}</dd>
        <dt className="text-muted">Agent</dt>
        <dd className="min-w-0 truncate font-mono text-xs leading-5">{check.agent_id}</dd>
        <dt className="text-muted">Latency</dt>
        <dd className="tabular-nums">{check.latency_ms} ms</dd>
      </div>
      {!check.success && (
        <div>
          <dt className="mb-1.5 text-muted">Error</dt>
          <dd className="whitespace-pre-wrap break-words rounded-lg bg-accent px-3 py-2 font-mono text-xs leading-relaxed">
            {check.error || 'failed'}
          </dd>
        </div>
      )}
    </dl>
  );
}

function RecentChecks({ checks }) {
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState(null);
  const pages = Math.max(1, Math.ceil(checks.length / CHECKS_PER_PAGE));
  // A background poll can shrink the list while the user sits on a later
  // page; clamp instead of rendering an empty slice.
  const safePage = Math.min(page, pages - 1);
  const slice = checks.slice(safePage * CHECKS_PER_PAGE, safePage * CHECKS_PER_PAGE + CHECKS_PER_PAGE);

  return (
    <Card>
      <ul className="divide-y divide-border">
        {slice.map((c, i) => (
          <li key={`${c.t}-${c.agent_id}-${i}`}>
            <button
              type="button"
              onClick={() => setSelected(c)}
              aria-haspopup="dialog"
              title={c.success ? 'View check details' : (c.error || 'View check details')}
              className="flex w-full cursor-pointer items-center gap-3 px-4 py-2.5 text-left text-sm transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-success/40"
            >
              {c.success
                ? <CircleCheck className="h-4 w-4 shrink-0 text-success" />
                : <CircleAlert className="h-4 w-4 shrink-0 text-danger" />}
              <span className="w-28 shrink-0 text-muted sm:w-36">{timeAgo(c.t)}</span>
              <span className="hidden w-24 shrink-0 truncate font-mono text-xs text-faint sm:block">{c.agent_id}</span>
              {c.success ? (
                <span className="ml-auto shrink-0 tabular-nums text-muted">{c.latency_ms} ms</span>
              ) : (
                <span className="ml-auto min-w-0 flex-1 truncate text-muted">{c.error || 'failed'}</span>
              )}
            </button>
          </li>
        ))}
        {slice.length === 0 && (
          <li className="px-4 py-10 text-center text-sm text-muted">No checks recorded yet.</li>
        )}
      </ul>

      <Modal open={selected !== null} onClose={() => setSelected(null)} title="Check details">
        {selected && <CheckDetails check={selected} />}
      </Modal>

      {pages > 1 && (
        <div className="flex items-center justify-between border-t border-border px-3 py-2 text-xs text-muted">
          <GhostButton
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            disabled={safePage === 0}
            className="gap-1"
          >
            <ChevronLeft className="h-3.5 w-3.5" /> Previous
          </GhostButton>
          <span>Page {safePage + 1} of {pages}</span>
          <GhostButton
            onClick={() => setPage((p) => Math.min(pages - 1, p + 1))}
            disabled={safePage >= pages - 1}
            className="gap-1"
          >
            Next <ChevronRight className="h-3.5 w-3.5" />
          </GhostButton>
        </div>
      )}
    </Card>
  );
}

export default function ServiceDetail({ serviceId, refreshMs, onBack }) {
  const [rangeKey, setRangeKey] = useState('24h');
  useNow();

  const { data, error } = usePoll(`/api/services/${encodeURIComponent(serviceId)}`, refreshMs);

  // A fresh service starts on the default range, so don't carry the last
  // one over. Remounting RecentChecks resets its page.
  useEffect(() => {
    setRangeKey('24h');
  }, [serviceId]);

  const range = useMemo(
    () => data?.ranges.find((r) => r.key === rangeKey)
      || data?.ranges.find((r) => r.key === '24h')
      || data?.ranges[0],
    [data, rangeKey],
  );

  const back = (
    <button
      type="button"
      onClick={onBack}
      className="mb-6 flex items-center gap-1.5 text-sm text-muted transition-colors hover:text-foreground"
    >
      <ArrowLeft className="h-4 w-4" /> Back to dashboard
    </button>
  );

  if (error) {
    return (
      <div>
        {back}
        <ErrorBanner>Cannot load this service ({error}).</ErrorBanner>
      </div>
    );
  }

  if (!data) {
    return (
      <div>
        {back}
        <div className="h-64 animate-pulse rounded-xl border border-border bg-card" />
      </div>
    );
  }

  const StatusIcon = STATUS_ICON[data.status] || CircleHelp;
  const statusTone = data.status === 'up' ? 'text-success' : data.status === 'down' ? 'text-danger' : 'text-muted';
  const lat = data.latency_24h;

  return (
    <div>
      {back}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{data.name}</h1>
          <p className="mt-1 flex flex-wrap items-center gap-2 text-sm text-muted">
            <span>{data.group}</span>
            <span className="text-faint">•</span>
            <span className="font-mono text-xs">{data.target}</span>
            <span className="text-faint">•</span>
            <span className="uppercase">{data.type}</span>
          </p>
        </div>
        <StatusPill status={data.status} lastChecked={data.last_checked} />
      </div>

      <div className="mt-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat icon={StatusIcon} label="Current status" value={STATUS_LABEL[data.status] || 'Unknown'} tone={statusTone} />
        <Stat
          icon={Gauge}
          label="Avg response (24h)"
          value={lat.samples > 0 ? `${lat.avg_ms} ms` : '—'}
        />
        <Stat
          icon={Gauge}
          label="Response range (24h)"
          value={lat.samples > 0 ? `${lat.min_ms}–${lat.max_ms} ms` : '—'}
        />
        <Stat icon={Clock} label="Last check" value={timeAgo(data.last_checked)} />
      </div>

      <section className="mt-8">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <SectionTitle>Response time</SectionTitle>
          <div className="flex gap-1 rounded-lg border border-border bg-card p-1">
            {data.ranges.map((r) => (
              <button
                key={r.key}
                type="button"
                onClick={() => setRangeKey(r.key)}
                className={`rounded-md px-2.5 py-1 text-xs transition-colors ${
                  r.key === rangeKey ? 'bg-accent text-foreground' : 'text-muted hover:text-foreground'
                }`}
              >
                {r.key}
              </button>
            ))}
          </div>
        </div>
        <Card className="px-2 py-3">
          <LatencyChart points={range?.trend || []} rangeKey={rangeKey} />
        </Card>
      </section>

      <section className="mt-8">
        <SectionTitle>Uptime</SectionTitle>
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          {data.ranges.map((r) => (
            <Card key={r.key} className="px-4 py-3.5">
              <div className="flex items-baseline justify-between">
                <span className="text-xs text-muted">{r.label}</span>
                <span className="text-sm font-semibold tabular-nums">
                  {r.uptime_pct == null ? '—' : `${r.uptime_pct.toFixed(2)} %`}
                </span>
              </div>
              <div className="mt-2.5">
                <UptimeBar pct={r.uptime_pct} />
              </div>
              <p className="mt-2 text-xs text-faint">
                {r.avg_latency_ms == null ? 'no successful checks' : `${r.avg_latency_ms} ms average`}
              </p>
            </Card>
          ))}
        </div>
      </section>

      <div className="mt-8 grid gap-6 lg:grid-cols-2">
        <section>
          <SectionTitle>Recent checks</SectionTitle>
          <RecentChecks key={data.id} checks={data.checks} />
        </section>

        <section>
          <SectionTitle>Events</SectionTitle>
          <Card>
            <ul className="divide-y divide-border">
              {data.events.map((e, i) => {
                const isStart = i === data.events.length - 1;
                const Icon = isStart ? Clock : (STATUS_ICON[e.status] || CircleHelp);
                const tone = isStart
                  ? 'text-muted'
                  : e.status === 'up' ? 'text-success' : 'text-danger';
                const label = isStart
                  ? 'Monitoring started'
                  : e.status === 'up' ? 'Service recovered' : 'Service went down';
                return (
                  <li key={`${e.t}-${i}`} className="flex items-start gap-3 px-4 py-3">
                    <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${tone}`} />
                    <div className="min-w-0">
                      <p className="text-sm">{label}</p>
                      <p className="mt-0.5 text-xs text-faint">
                        {clockTime(e.t)} • {timeAgo(e.t)}
                      </p>
                    </div>
                  </li>
                );
              })}
              {data.events.length === 0 && (
                <li className="px-4 py-10 text-center text-sm text-muted">Nothing recorded yet.</li>
              )}
            </ul>
          </Card>
        </section>
      </div>
    </div>
  );
}
