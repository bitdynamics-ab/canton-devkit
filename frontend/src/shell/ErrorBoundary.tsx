import { Component, type ErrorInfo, type ReactNode } from "react";
import { W, wMono, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";

// Catches render-time exceptions per-route so one crashed screen doesn't
// take the whole UI down. Class component because the error-boundary API
// has no hook equivalent. No auto-retry (would busy-loop on the same
// state); Retry forces a re-mount via resetCount.

interface Props {
  // Force a reset on navigation; the boundary stays mounted across the
  // Routes switch, so without this a crashed screen's error persists.
  routeKey?: string;
  children: ReactNode;
}

interface State {
  error: Error | null;
  resetCount: number;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, resetCount: 0 };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Full stack to the console only; no remote telemetry without consent.
    // eslint-disable-next-line no-console
    console.error("ErrorBoundary caught a render error:", error, info);
  }

  componentDidUpdate(prev: Props) {
    // Route change → drop the error, else the fallback persists after nav.
    if (prev.routeKey !== this.props.routeKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  render() {
    if (this.state.error) {
      return <Fallback error={this.state.error} onRetry={this.handleRetry} />;
    }
    return (
      // resetCount key forces a clean remount on Retry; without it a stale
      // closure or torn fetch re-throws the same error immediately.
      <div key={this.state.resetCount}>{this.props.children}</div>
    );
  }

  private handleRetry = () => {
    this.setState((s) => ({ error: null, resetCount: s.resetCount + 1 }));
  };
}

interface FallbackProps {
  error: Error;
  onRetry: () => void;
}

function Fallback({ error, onRetry }: FallbackProps) {
  return (
    <div
      role="alert"
      style={{
        background: `${tint(W.err, 6)}`,
        border: `1px solid ${W.err}`,
        borderRadius: R.control,
        padding: 16,
        margin: "8px 0",
        color: W.text,
      }}
    >
      <header style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
        <strong style={{ color: W.err, fontSize: fs.strong }}>
          This screen hit a render error
        </strong>
        <code style={{ color: W.dim, fontSize: fs.label, fontFamily: wMono }}>
          {error.name}
        </code>
      </header>
      <p style={{ color: W.text2, fontSize: fs.lead, marginTop: 8, marginBottom: 12 }}>
        The rest of the console still works. Switch screens from the
        sidebar or ⌘K. Retry re-mounts this screen. If it throws again,
        open the browser dev-tools console for the full stack.
      </p>
      <div style={{ display: "flex", gap: 8, marginBottom: 4 }}>
        <Button variant="secondary" size="md" onClick={onRetry}>
          Retry
        </Button>
      </div>
      <details style={{ marginTop: 8 }}>
        <summary
          style={{ cursor: "pointer", color: W.dim, fontSize: fs.label }}
        >
          Error details
        </summary>
        <pre
          style={{
            background: W.bg,
            border: `1px solid ${W.border}`,
            borderRadius: R.control,
            padding: "10px 12px",
            fontFamily: wMono,
            fontSize: fs.label,
            color: W.text2,
            margin: "8px 0 0",
            maxHeight: 120,
            overflow: "auto",
            whiteSpace: "pre-wrap",
          }}
        >
          {error.message || "(no message)"}
        </pre>
      </details>
    </div>
  );
}
