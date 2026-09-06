import { useEffect, useRef, useState } from 'react';
import { Check, ChevronDown } from 'lucide-react';

// A listbox in the shape shadcn draws one. Hand-rolled because the whole
// need here is two pickers: a button, a popover, arrow keys and Escape.
// className goes to the wrapper, triggerClassName to the button.
export default function Dropdown({ value, options, onChange, icon: LeadIcon, label, heading, align = 'right', className = '', triggerClassName = '' }) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const root = useRef(null);
  const trigger = useRef(null);
  const current = options.find((o) => o.value === value) || options[0];

  useEffect(() => {
    if (!open) return undefined;
    setActive(Math.max(0, options.findIndex((o) => o.value === value)));

    function onPointerDown(e) {
      if (!root.current?.contains(e.target)) setOpen(false);
    }
    // Tabbing past the menu should dismiss it just like clicking away does.
    function onFocusIn(e) {
      if (!root.current?.contains(e.target)) setOpen(false);
    }
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('focusin', onFocusIn);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('focusin', onFocusIn);
    };
  }, [open, options, value]);

  function pick(option) {
    onChange(option.value);
    setOpen(false);
    // The option button unmounts with the menu; park focus back on the
    // trigger so keyboard users don't lose their place.
    trigger.current?.focus();
  }

  function onKeyDown(e) {
    if (e.key === 'Escape') {
      if (open) trigger.current?.focus();
      setOpen(false);
      return;
    }
    if (!open && (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown')) {
      e.preventDefault();
      setOpen(true);
      return;
    }
    if (!open) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => (i + 1) % options.length);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => (i - 1 + options.length) % options.length);
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      pick(options[active]);
    }
  }

  return (
    <div className={`relative ${className}`} ref={root} onKeyDown={onKeyDown}>
      <button
        type="button"
        ref={trigger}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen((v) => !v)}
        className={`flex h-10 items-center gap-2 rounded-lg border border-border bg-card px-3 text-sm text-foreground transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-success/40 sm:h-9 ${triggerClassName}`}
      >
        {LeadIcon && <LeadIcon className="h-4 w-4 text-muted" />}
        <span>{current?.label}</span>
        <ChevronDown className={`h-4 w-4 text-muted transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {open && (
        <ul
          role="listbox"
          aria-label={label}
          className={`absolute z-30 mt-2 max-w-[calc(100vw-2rem)] min-w-[9rem] rounded-lg border border-border bg-card p-1 shadow-popover ${align === 'right' ? 'right-0' : 'left-0'}`}
        >
          {heading && (
            <li className="px-2.5 pb-1 pt-1.5 text-xs text-faint">{heading}</li>
          )}
          {options.map((option, i) => (
            <li key={option.value}>
              <button
                type="button"
                role="option"
                aria-selected={option.value === value}
                onClick={() => pick(option)}
                onMouseEnter={() => setActive(i)}
                className={`flex w-full items-center justify-between gap-3 rounded-md px-2.5 py-2 text-left text-sm transition-colors sm:py-1.5 ${i === active ? 'bg-accent text-foreground' : 'text-muted'}`}
              >
                {option.label}
                {option.value === value && <Check className="h-4 w-4 text-success" />}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
