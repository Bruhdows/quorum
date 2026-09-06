import { useCallback, useEffect, useState } from 'react';
import { Moon, Sun, Timer } from 'lucide-react';
import Dropdown from './Dropdown.jsx';
import StatusBoard from './StatusBoard.jsx';
import ServiceDetail from './ServiceDetail.jsx';
import { fetchJSON } from '../lib/api.js';
import { iconButtonClass } from './ui/button.jsx';
import Tooltip from './Tooltip.jsx';
import { Toaster } from 'sonner';
import { useMediaQuery } from '../hooks/useMediaQuery.js';

const REFRESH_OPTIONS = [
  { value: 10000, label: '10s' },
  { value: 30000, label: '30s' },
  { value: 60000, label: '1m' },
  { value: 120000, label: '2m' },
  { value: 300000, label: '5m' },
  { value: 600000, label: '10m' },
];

const REFRESH_KEY = 'quorum:refresh-ms';
const THEME_KEY = 'quorum:theme';

// Matches the token transition in global.css, plus a frame of slack.
const THEME_SWAP_MS = 300;

// The official GitHub mark. Lucide dropped brand icons, so this one stays inline.
function GithubMark(props) {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" {...props}>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  );
}

// The theme lives in App rather than in the button because the toast layer
// needs it too, and a toast in the wrong theme is very obvious.
function ThemeToggle({ dark, onToggle }) {
  function toggle() {
    const next = !dark;
    const root = document.documentElement;

    // Let the token animation own the swap. Without this, every control with
    // its own colour transition arrives late and the page changes in stages.
    root.classList.add('theme-switching');
    onToggle(next);
    root.classList.toggle('dark', next);
    window.setTimeout(() => {
      root.classList.remove('theme-switching');
      // Native chrome (scrollbar, form controls) cannot fade, so it changes
      // once the rest of the page has finished arriving.
      if (next) root.setAttribute('data-scheme', 'dark');
      else root.removeAttribute('data-scheme');
    }, THEME_SWAP_MS);

    try {
      localStorage.setItem(THEME_KEY, next ? 'dark' : 'light');
    } catch {
      // Private mode or blocked storage just means the theme won't persist.
    }
  }

  return (
    <Tooltip label={dark ? 'Light mode' : 'Dark mode'} side="bottom">
      <button
        type="button"
        onClick={toggle}
        aria-label={dark ? 'Switch to light theme' : 'Switch to dark theme'}
        className={iconButtonClass}
      >
        {dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
      </button>
    </Tooltip>
  );
}

function serviceFromURL() {
  if (typeof window === 'undefined') return null;
  return new URLSearchParams(window.location.search).get('service');
}

export default function App() {
  const [refreshMs, setRefreshMs] = useState(REFRESH_OPTIONS[0].value);
  const [serviceId, setServiceId] = useState(null);
  const [dark, setDark] = useState(true);
  const [site, setSite] = useState({ title: 'quorum', github: '' });
  // Bottom-right toasts cover content on a phone; centered ones don't.
  const toastPosition = useMediaQuery('(min-width: 640px)') ? 'bottom-right' : 'bottom-center';

  // Branding lives in services.yaml, so it can change without a rebuild.
  useEffect(() => {
    let cancelled = false;
    fetchJSON('/api/site')
      .then((json) => {
        if (!cancelled && json?.title) setSite({ title: json.title, github: json.github || '' });
      })
      .catch(() => {
        // Keep the default title rather than showing an empty header.
      });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    setServiceId(serviceFromURL());
    // The inline script in index.astro settles the class before paint, so the
    // document is the source of truth rather than a second copy of the rule.
    setDark(document.documentElement.classList.contains('dark'));
    try {
      const saved = Number(localStorage.getItem(REFRESH_KEY));
      if (REFRESH_OPTIONS.some((o) => o.value === saved)) setRefreshMs(saved);
    } catch {
      // Ignore unreadable storage and keep the default.
    }
  }, []);

  // The Back button has to work, so the two views are real history entries
  // rather than a mode flag.
  useEffect(() => {
    const onPop = () => setServiceId(serviceFromURL());
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);

  const open = useCallback((id) => {
    window.history.pushState({}, '', `?service=${encodeURIComponent(id)}`);
    setServiceId(id);
    window.scrollTo({ top: 0 });
  }, []);

  const back = useCallback(() => {
    window.history.pushState({}, '', window.location.pathname);
    setServiceId(null);
  }, []);

  function changeRefresh(ms) {
    setRefreshMs(ms);
    try {
      localStorage.setItem(REFRESH_KEY, String(ms));
    } catch {
      // Ignore unwritable storage.
    }
  }

  return (
    <>
      <Toaster
        theme={dark ? 'dark' : 'light'}
        position={toastPosition}
        offset={24}
        richColors
        closeButton
        toastOptions={{
          classNames: {
            toast: 'rounded-xl',
            description: 'opacity-80',
          },
        }}
      />

      <header className="sticky top-0 z-20 border-b border-border bg-background/80 backdrop-blur">
        <div className="mx-auto flex max-w-4xl items-center gap-2 px-4 py-3 sm:px-6">
          <a href="/" className="font-semibold tracking-tight transition-opacity hover:opacity-80">{site.title}</a>

          <div className="ml-auto flex items-center gap-2">
            {/* No tooltip here on purpose. It would sit on top of the menu
                this button opens, and the button already shows its value. */}
            <Dropdown
              value={refreshMs}
              options={REFRESH_OPTIONS}
              onChange={changeRefresh}
              icon={Timer}
              label="Refresh rate"
              heading="Refresh every"
            />
            <ThemeToggle dark={dark} onToggle={setDark} />
            {site.github && (
              <Tooltip label="Source on GitHub" side="bottom">
                <a
                  href={site.github}
                  target="_blank"
                  rel="noopener"
                  aria-label={`${site.title} on GitHub`}
                  className={iconButtonClass}
                >
                  <GithubMark className="h-4 w-4" />
                </a>
              </Tooltip>
            )}
          </div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-4xl flex-1 px-4 py-6 sm:px-6 sm:py-10">
        {serviceId ? (
          <ServiceDetail serviceId={serviceId} refreshMs={refreshMs} onBack={back} />
        ) : (
          <StatusBoard refreshMs={refreshMs} onOpen={open} />
        )}
      </main>
    </>
  );
}
