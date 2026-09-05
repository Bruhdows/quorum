import { cx } from '../../lib/cx.js';

export function Card({ className, children }) {
  return <div className={cx('rounded-xl border border-border bg-card', className)}>{children}</div>;
}

export function SectionTitle({ children }) {
  return <h2 className="mb-3 text-sm font-medium text-muted">{children}</h2>;
}
