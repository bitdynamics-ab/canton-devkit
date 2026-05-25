import {
  createContext,
  createElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
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
  }>({ instances: [], loading: true, error: null });

  const load = useCallback(() => {
    setState((prev) => ({ ...prev, loading: true }));
    fetchInstances()
      .then((r) =>
        setState({
          instances: r.instances,
          warning: r.warning,
          loading: false,
          error: null,
        }),
      )
      .catch((e: unknown) =>
        setState({
          instances: [],
          loading: false,
          error: e instanceof ApiError ? e.message : "failed to load instances",
        }),
      );
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const selected = useMemo(() => {
    if (state.instances.length === 0) return null;
    if (urlPick && state.instances.some((i) => i.name === urlPick)) {
      return urlPick;
    }
    return state.instances.find((i) => i.status === "running")?.name ?? null;
  }, [state.instances, urlPick]);

  const select = useCallback(
    (name: string) => {
      const next = new URLSearchParams(params);
      next.set("instance", name);
      setParams(next, { replace: false });
    },
    [params, setParams],
  );

  const value = useMemo<InstanceSelection>(
    () => ({
      instances: state.instances,
      warning: state.warning,
      selected,
      loading: state.loading,
      error: state.error,
      select,
      refresh: load,
    }),
    [state.instances, state.warning, state.loading, state.error, selected, select, load],
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
