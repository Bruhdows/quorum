import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchJSON } from '../lib/api.js';

// The polling loop the board and the detail page used to hand-roll. Fetch
// now, then re-fetch every refreshMs. Data resets to null whenever the
// path changes so the caller can render its skeleton for the new resource.
// onData/onError are edge callbacks. onError also hears whether the failing
// fetch was a manual reload or a background poll, so callers can toast
// accordingly. reload() resolves true on success, false on failure.
// A sequence guard drops late responses from a previous path or an
// overlapping poll instead of letting them overwrite newer data.
export function usePoll(path, refreshMs, { onData, onError } = {}) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [updatedAt, setUpdatedAt] = useState(null);

  const seq = useRef(0);
  const callbacks = useRef({ onData, onError });
  callbacks.current = { onData, onError };

  const load = useCallback(async (opts = {}) => {
    const my = ++seq.current;
    try {
      const json = await fetchJSON(path);
      if (my !== seq.current) return false;
      setData(json);
      setUpdatedAt(new Date().toISOString());
      setError(null);
      callbacks.current.onData?.(json);
      return true;
    } catch (err) {
      if (my !== seq.current) return false;
      const message = err?.message || 'request failed';
      setError(message);
      callbacks.current.onError?.(message, !!opts.manual);
      return false;
    }
  }, [path]);

  // Reset only when the resource itself changes. A refreshMs change
  // re-arms the interval without flashing the skeleton.
  const prevPath = useRef(path);
  useEffect(() => {
    if (prevPath.current !== path) {
      prevPath.current = path;
      setData(null);
      setError(null);
      setUpdatedAt(null);
    }
    load();
    const id = setInterval(() => { load(); }, refreshMs);
    return () => clearInterval(id);
  }, [load, refreshMs]);

  return { data, error, updatedAt, reload: load };
}
