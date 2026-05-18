var __defProp = Object.defineProperty;
var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
var __getOwnPropNames = Object.getOwnPropertyNames;
var __hasOwnProp = Object.prototype.hasOwnProperty;
var __export = (target, all) => {
  for (var name in all)
    __defProp(target, name, { get: all[name], enumerable: true });
};
var __copyProps = (to, from, except, desc) => {
  if (from && typeof from === "object" || typeof from === "function") {
    for (let key of __getOwnPropNames(from))
      if (!__hasOwnProp.call(to, key) && key !== except)
        __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
  }
  return to;
};
var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);

// src/index.ts
var index_exports = {};
__export(index_exports, {
  LuxoProvider: () => LuxoProvider,
  useLuxoClient: () => useLuxoClient,
  useLuxoMutation: () => useLuxoMutation,
  useLuxoQuery: () => useLuxoQuery
});
module.exports = __toCommonJS(index_exports);

// src/use-query.ts
var import_react = require("react");
function useLuxoQuery(queryFn, deps = []) {
  const [data, setData] = (0, import_react.useState)(null);
  const [loading, setLoading] = (0, import_react.useState)(true);
  const [error, setError] = (0, import_react.useState)(null);
  const mountedRef = (0, import_react.useRef)(true);
  const versionRef = (0, import_react.useRef)(0);
  const execute = (0, import_react.useCallback)(async () => {
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
  (0, import_react.useEffect)(() => {
    mountedRef.current = true;
    execute();
    return () => {
      mountedRef.current = false;
    };
  }, [execute]);
  return { data, loading, error, refetch: execute };
}

// src/use-mutation.ts
var import_react2 = require("react");
function useLuxoMutation(mutationFn) {
  const [data, setData] = (0, import_react2.useState)(null);
  const [loading, setLoading] = (0, import_react2.useState)(false);
  const [error, setError] = (0, import_react2.useState)(null);
  const mutate = (0, import_react2.useCallback)(async (variables) => {
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
  const reset = (0, import_react2.useCallback)(() => {
    setData(null);
    setError(null);
    setLoading(false);
  }, []);
  return { data, loading, error, mutate, reset };
}

// src/provider.ts
var import_react3 = require("react");
var LuxoContext = (0, import_react3.createContext)(null);
function LuxoProvider({ transport, children }) {
  return (0, import_react3.createElement)(LuxoContext.Provider, { value: transport }, children);
}
function useLuxoClient() {
  const ctx = (0, import_react3.useContext)(LuxoContext);
  if (!ctx) throw new Error("useLuxoClient must be used within <LuxoProvider>");
  return ctx;
}
// Annotate the CommonJS export names for ESM import in node:
0 && (module.exports = {
  LuxoProvider,
  useLuxoClient,
  useLuxoMutation,
  useLuxoQuery
});
