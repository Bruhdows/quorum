import { TriangleAlert } from 'lucide-react';
import { cx } from '../../lib/cx.js';

// The full-width error banner and the loading skeleton both pages used to
// define separately.

export function ErrorBanner({ children, className }) {
  return (
    <div className={cx('flex items-center gap-3 rounded-xl border border-danger/25 bg-danger/5 px-5 py-4 text-sm text-danger', className)}>
      <TriangleAlert className="h-5 w-5 shrink-0" />
      <div className="min-w-0">{children}</div>
    </div>
  );
}

export function PageSkeleton({ rows = 3 }) {
  return (
    <div className="space-y-4" aria-hidden="true">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="animate-pulse rounded-xl border border-border bg-card px-5 py-5">
          <div className="h-4 w-40 rounded bg-subtle" />
          <div className="mt-3 h-9 w-full rounded bg-subtle/60" />
        </div>
      ))}
    </div>
  );
}
