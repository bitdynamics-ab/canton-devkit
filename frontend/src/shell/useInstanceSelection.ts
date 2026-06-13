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
    // Only flip to a loading state on the FIRST fetch. Background
    // refreshes (the 15s poll below) keep the last-good list on
    // screen — otherwise the whole instance table + topbar switcher
    // flash out every 15 s while the fetch is in flight, and an
    // in-progress CreatingPanel the user is watching unmounts with
    // it. `loaded` is read fresh from prev so the closure doesn't
    // capture a stale value.
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
          // A transient registry-read hiccup (e.g. while a heavyweight
          // `up` saturates the machine) must NOT erase the dashboard.
          // If we already have a good list, keep it and just mark the
          // data stale; only surface a hard error before the first
          // successful load, when there is nothing else to show.
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
    // Backend reconciler (internal/ui/handlers/reconciler.go) probes
    // docker every 15s and rewrites status when the registry diverges
    // from reality (e.g. user killed containers via Docker Desktop).
    // Without a poll here, the Dashboard would render a stale
    // "running" until the user manually refreshes the page — which
    // surprised the first user who hit it. 15 s matches the backend
    // tick so we're never more than two ticks behind truth. Cheap:
    // /api/instances is a pure-registry read with no docker call.
    //
    // Future: replace with an SSE subscription on a `list:changes`
    // topic the reconciler publishes to. Tracked separately.
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

  // setParams is stable across renders, but `params` is a fresh
  // URLSearchParams instance every render (react-router quirk).
  // If we include `params` in the useCallback deps, `select` gets
  // a new identity each render — that cascades into the context
  // `value` memo also being unstable, which causes consumers
  // that pass `sel.select` to child effects' deps to fire those
  // effects on every render. Observed as a tight render loop
  // hammering /api/instances after a successful create flow.
  //
  // Fix: capture the live `params` via ref and dereference inside
  // the callback. useCallback deps are now empty + setParams (a
  // stable function) — select is rock-stable across renders.
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
