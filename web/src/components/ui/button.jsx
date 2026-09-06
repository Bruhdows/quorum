import { cx } from '../../lib/cx.js';

// Shared button looks, shaped like shadcn's variants but without the
// dependency. The app needs two buttons, a square icon one and a quiet
// text one, not a whole design system.

export const iconButtonClass =
  'flex h-10 w-10 items-center justify-center rounded-lg border border-border bg-card text-muted transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-success/40 sm:h-9 sm:w-9';

export const ghostButtonClass =
  'flex items-center rounded-md px-2 py-1 transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-success/40 disabled:cursor-default disabled:opacity-40 disabled:hover:bg-transparent';

export function GhostButton({ className, ...props }) {
  return <button type="button" {...props} className={cx(ghostButtonClass, className)} />;
}
