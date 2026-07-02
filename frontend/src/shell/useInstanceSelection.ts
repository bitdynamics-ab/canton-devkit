import {
  createContext,
  createElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useSearchParams } from "react-router-dom";
import { ApiError, type InstanceSummary, fetchInstances } from "../api";

// Instance selection — the single source of truth for "which
// instance is the user looking at" across every screen.
//
// State lives in the URL (?instance=<name>) so:
//   - links shared with a teammate carry the selection
//   - browser back/forward navigates between selections naturally
//   - a hard refresh preserves the user's pick
//
// Wrapped in Context so the TopBar's switcher and the Dashboard
// don't double-fetch /api/instances. The Provider owns the fetch;
// every consumer reads from the same shared state.
//
// Auto-pick rule: prefer the URL value if it still maps to a
// known instance; otherwise the first running instance; otherwise
// null. Derived from instances+URL — keeps the URL authoritative.

export interface InstanceSelection {
  instances: InstanceSummary[];
  // warning surfaces the same `ListResponse.warning` the CLI's
  // `dpm localnet list` displays (e.g. registry parse drift).
  // The Dashboard renders it as an amber strip; the topbar
  // doesn't surface it (would be noisy in chrome).
  warning?: string;
  selected: string | null;
  loading: boolean;
  error: string | null;
  // stale is true when the most recent background refresh failed but
  // we are still showing the last good instance list. Consumers can
  // render a non-destructive "couldn't refresh" hint without tearing
  // down the table (see the error vs. stale split below).
  stale: boolean;
  select: (name: string) => void;
  refresh: () => void;
}

const InstanceSelectionContext = createContext<InstanceSelection | null>(null);

export function InstanceSelectionProvider({ children }: { children: ReactNode }) {
  const [params, setParams] = useSearchParams();
  const urlPick = params.get("instance");

  const [state, setState] = useState<{
    instances: InstanceSummary[];
    warning?: string;
    loading: boolean;
    error: string | null;
    stale: boolean;
    // True once a fetch has resolved (success OR failure) at least
    // once. Distinguishes the very first load (show the spinner,
    // gate the table) from background refreshes (keep prior data).
    loaded: boolean;
  }>({ instances: [], loading: true, error: null, stale: false, loaded: false });

  const load = useCallback(() => {
    // Only flip to loading on the FIRST fetch. Background refreshes
    // keep the last-good list on screen — otherwise the instance table
    // and topbar switcher flash out on every poll. `loaded` is read
    // fresh from prev so the closure doesn't capture a stale value.
    setState((prev) =>
      prev.loaded ? prev : { ...prev, loading: true },
    );
    fetchInstances()
      .then((r) =>
        setState({
          instances: r.instances,
          warning: r.warning,
          loading: false,
          error: null,
          stale: false,
          loaded: true,
        }),
      )
      .catch((e: unknown) =>
        setState((prev) => {
          const message =
            e instanceof ApiError ? e.message : "failed to load instances";
          // A transient registry-read hiccup must NOT erase the
          // dashboard: keep an existing good list and mark it stale;
          // only surface a hard error before the first successful
          // load, when there is nothing else to show.
          if (prev.loaded && prev.instances.length > 0) {
            return {
              ...prev,
              loading: false,
              error: null,
              stale: true,
            };
          }
          return {
            instances: [],
            loading: false,
            error: message,
            stale: false,
            loaded: true,
          };
        }),
      );
  }, []);

  useEffect(() => {
    load();
    // The backend reconciler probes docker every 15s and rewrites
    // status when the registry diverges from reality (e.g. containers
    // killed via Docker Desktop). Without a matching poll here the
    // Dashboard would show a stale "running" until a manual page
    // refresh. Cheap: /api/instances is a pure-registry read.
    const t = setInterval(load, 15_000);
    return () => clearInterval(t);
  }, [load]);

  const selected = useMemo(() => {
    if (state.instances.length === 0) return null;
    if (urlPick && state.instances.some((i) => i.name === urlPick)) {
      return urlPick;
    }
    return state.instances.find((i) => i.status === "running")?.name ?? null;
  }, [state.instances, urlPick]);

  // `params` is a fresh URLSearchParams instance every render
  // (react-router quirk), so listing it in useCallback deps would give
  // `select` a new identity each render, destabilise the context value
  // memo, and re-fire every consumer effect that depends on it — a
  // tight render loop hammering /api/instances. Read the live params
  // through a ref instead so `select` stays stable.
  const paramsRef = useRef(params);
  useEffect(() => {
    paramsRef.current = params;
  }, [params]);
  const select = useCallback(
    (name: string) => {
      const next = new URLSearchParams(paramsRef.current);
      next.set("instance", name);
      setParams(next, { replace: false });
    },
    [setParams],
  );

  const value = useMemo<InstanceSelection>(
    () => ({
      instances: state.instances,
      warning: state.warning,
      selected,
      loading: state.loading,
      error: state.error,
      stale: state.stale,
      select,
      refresh: load,
    }),
    [state.instances, state.warning, state.loading, state.error, state.stale, selected, select, load],
  );

  return createElement(InstanceSelectionContext.Provider, { value }, children);
}

export function useInstanceSelection(): InstanceSelection {
  const ctx = useContext(InstanceSelectionContext);
  if (!ctx) {
    throw new Error(
      "useInstanceSelection must be used inside <InstanceSelectionProvider>",
    );
  }
  return ctx;
}
