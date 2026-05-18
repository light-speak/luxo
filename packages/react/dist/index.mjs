// src/use-query.ts
import { useState, useEffect, useCallback, useRef } from "react";
function useLuxoQuery(queryFn, deps = []) {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const mountedRef = useRef(true);
  const versionRef = useRef(0);
  const execute = useCallback(async () => {
    const version = ++versionRef.current;
    setLoading(true);
    setError(null);
    try {
      const result = await queryFn();
      if (mountedRef.current && version === versionRef.current) {
        setData(result);
      }
    } catch (e) {
      if (mountedRef.current && version === versionRef.current) {
        setError(e instanceof Error ? e : new Error(String(e)));
      }
    } finally {
      if (mountedRef.current && version === versionRef.current) {
        setLoading(false);
      }
    }
  }, deps);
  useEffect(() => {
    mountedRef.current = true;
    execute();
    return () => {
      mountedRef.current = false;
    };
  }, [execute]);
  return { data, loading, error, refetch: execute };
}

// src/use-mutation.ts
import { useState as useState2, useCallback as useCallback2 } from "react";
function useLuxoMutation(mutationFn) {
  const [data, setData] = useState2(null);
  const [loading, setLoading] = useState2(false);
  const [error, setError] = useState2(null);
  const mutate = useCallback2(async (variables) => {
    setLoading(true);
    setError(null);
    try {
      const result = await mutationFn(variables);
      setData(result);
      return result;
    } catch (e) {
      const err = e instanceof Error ? e : new Error(String(e));
      setError(err);
      throw err;
    } finally {
      setLoading(false);
    }
  }, [mutationFn]);
  const reset = useCallback2(() => {
    setData(null);
    setError(null);
    setLoading(false);
  }, []);
  return { data, loading, error, mutate, reset };
}

// src/provider.ts
import { createContext, useContext, createElement } from "react";
var LuxoContext = createContext(null);
function LuxoProvider({ transport, children }) {
  return createElement(LuxoContext.Provider, { value: transport }, children);
}
function useLuxoClient() {
  const ctx = useContext(LuxoContext);
  if (!ctx) throw new Error("useLuxoClient must be used within <LuxoProvider>");
  return ctx;
}
export {
  LuxoProvider,
  useLuxoClient,
  useLuxoMutation,
  useLuxoQuery
};
