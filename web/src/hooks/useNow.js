import { useEffect, useState } from 'react';

// Rerender the caller every second so relative timestamps ("12 seconds ago")
// keep moving. timeAgo reads Date.now() itself, so the returned value is
// only needed when the component wants it.
export function useNow(active = true) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return undefined;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [active]);
  return now;
}
