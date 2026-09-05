// One place for talking to the hub. Every poll goes through fetchJSON so
// error mapping (notably 404 -> "no such service") stays consistent.

// Relative path works once the hub serves this page itself. Under `astro dev`
// (import.meta.env.DEV) the page is on Astro's port, so point at the hub.
export const API_BASE = import.meta.env.DEV ? 'http://localhost:8080' : '';

export async function fetchJSON(path, { signal } = {}) {
  const res = await fetch(`${API_BASE}${path}`, { cache: 'no-store', signal });
  if (res.status === 404) {
    const err = new Error('no such service');
    err.status = 404;
    throw err;
  }
  if (!res.ok) {
    const err = new Error(`status ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return res.json();
}
