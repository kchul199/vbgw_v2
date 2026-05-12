import { useEffect, useRef, useCallback, useState } from 'react';

/**
 * Polling hook — calls `fn` every `intervalMs`, with automatic cleanup.
 * Returns { data, error, loading, refresh }.
 */
export function usePolling(fn, intervalMs = 5000, deps = []) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(true);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  const refresh = useCallback(async () => {
    try {
      const result = await fnRef.current();
      setData(result);
      setError(null);
    } catch (err) {
      setError(err.message || 'Unknown error');
    } finally {
      setLoading(false);
    }
  }, deps);

  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, intervalMs);
    return () => clearInterval(timer);
  }, [refresh, intervalMs]);

  return { data, error, loading, refresh };
}
