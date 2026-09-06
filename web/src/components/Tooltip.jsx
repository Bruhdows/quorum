// A styled tooltip to replace the browser's `title` bubble, which can't be
// themed and waits a second before it shows. CSS-only on purpose: the history
// strip renders fifty of these per service, so none of them holds React state.
// Hidden on touch (no hover there) and wraps instead of overflowing.
export default function Tooltip({ label, children, side = 'top', className = '' }) {
  if (!label) return children;

  const place = side === 'bottom'
    ? 'top-full mt-2'
    : 'bottom-full mb-2';

  return (
    <span className={`group/tip relative inline-flex ${className}`}>
      {children}
      <span
        role="tooltip"
        className={`pointer-events-none absolute left-1/2 z-40 max-w-[calc(100vw-2rem)] -translate-x-1/2 scale-95 whitespace-normal rounded-md border border-border bg-card px-2 py-1 text-center text-xs font-normal text-foreground opacity-0 shadow-popover transition-[opacity,transform] duration-150 group-hover/tip:scale-100 group-hover/tip:opacity-100 group-focus-within/tip:scale-100 group-focus-within/tip:opacity-100 sm:whitespace-nowrap [@media(hover:none)]:hidden ${place}`}
      >
        {label}
      </span>
    </span>
  );
}
