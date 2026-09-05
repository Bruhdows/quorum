import { CircleAlert, CircleCheck, CircleHelp, TriangleAlert } from 'lucide-react';
import { API_BASE } from '../lib/api.js';
import { timeAgo } from '../lib/format.js';
import Tooltip from './Tooltip.jsx';

// Re-exported so old imports keep working. New code imports from
// ../lib/api.js and ../lib/format.js directly.
export { API_BASE, timeAgo };
export { clockTime, dayDate, dayDateShort } from '../lib/format.js';

export const STATUS_LABEL = {
  up: 'Operational',
  down: 'Down',
  degraded: 'Degraded',
  unknown: 'Unknown',
};

export const STATUS_ICON = {
  up: CircleCheck,
  down: CircleAlert,
  degraded: TriangleAlert,
  unknown: CircleHelp,
};

const STATUS_PILL = {
  up: 'bg-success/10 text-success ring-success/20 hover:bg-success/20 hover:ring-success/40',
  down: 'bg-danger/10 text-danger ring-danger/20 hover:bg-danger/20 hover:ring-danger/40',
  degraded: 'bg-warning/10 text-warning ring-warning/20 hover:bg-warning/20 hover:ring-warning/40',
  unknown: 'bg-muted/10 text-muted ring-muted/20 hover:bg-muted/20 hover:ring-muted/40',
};

// Worst first, so a broken service is never buried under healthy ones.
export const SEVERITY = { down: 0, degraded: 1, unknown: 2, up: 3 };

export function StatusPill({ status, lastChecked }) {
  const StatusIcon = STATUS_ICON[status] || CircleHelp;
  return (
    <Tooltip label={`Last checked ${timeAgo(lastChecked)}`}>
      <span
        className={`inline-flex cursor-default items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset transition-colors ${STATUS_PILL[status] || STATUS_PILL.unknown}`}
      >
        <StatusIcon className="h-3.5 w-3.5" strokeWidth={2.25} />
        {STATUS_LABEL[status] || STATUS_LABEL.unknown}
      </span>
    </Tooltip>
  );
}
