import { Component, type ErrorInfo, type ReactNode } from "react";
import { W, wMono } from "../tokens";

// ErrorBoundary — catches render-time exceptions from descendants and
// renders a fallback so one crashed screen doesn't take the whole UI
// down. Wrapped per-route so the shell stays interactive and sibling
// routes stay renderable. Class component because React's
// error-boundary API has no hook equivalent.
//
// No automatic recovery: a render that threw once will almost
// certainly throw again on the same state, so auto-retry would
// busy-loop. The "Retry" button instead forces a re-mount via a reset
// key so transient state corruption gets a fresh shot.

interface Props {
  // routeKey lets the parent force a reset when navigating to a
  // new screen — without this, an error from /explorer would
  // stick around when the user clicks /overview because the
  // boundary itself stays mounted across the Routes switch.
  routeKey?: string;
  children: ReactNode;
}

interface State {
  error: Error | null;
  // Bump to force the boundary to drop its error and re-render
  // children. Used by the Retry button.
  resetCount: number;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, resetCount: 0 };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The fallback shows only .name/.message; the full stack goes to
    // the console. Don't ship to a remote telemetry endpoint without
    // explicit user consent.
    // eslint-disable-next-line no-console
    console.error("ErrorBoundary caught a render error:", error, info);
  }

  componentDidUpdate(prev: Props) {
    // Route change → drop the error; otherwise navigating away from a
    // crashed screen keeps showing the fallback.
    if (prev.routeKey !== this.props.routeKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  render() {
    if (this.state.error) {
      return <Fallback error={this.state.error} onRetry={this.handleRetry} />;
    }
    return (
      // The reset key forces a clean remount of children when
      // the user clicks Retry — without remount, a stale closure
      // or torn fetch can re-throw the same error immediately.
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
        background: `${W.err}10`,
        border: `1px solid ${W.err}`,
        borderRadius: 4,
        padding: 20,
        margin: "8px 0",
        color: W.text,
      }}
    >
      <header style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
        <strong style={{ color: W.err, fontSize: 14 }}>
          This screen hit a render error
        </strong>
        <code style={{ color: W.dim, fontSize: 11, fontFamily: wMono }}>
          {error.name}
        </code>
      </header>
      <p style={{ color: W.text2, fontSize: 13, marginTop: 8, marginBottom: 12 }}>
        The rest of the UI is still usable — switch screens via the
        sidebar or ⌘K. If the error keeps coming back, capture this
        block in a screenshot and the full stack from your browser's
        dev-tools console.
      </p>
      <pre
        style={{
          background: W.bg,
          border: `1px solid ${W.border}`,
          borderRadius: 2,
          padding: "10px 12px",
          fontFamily: wMono,
          fontSize: 11.5,
          color: W.text2,
          margin: 0,
          maxHeight: 120,
          overflow: "auto",
          whiteSpace: "pre-wrap",
        }}
      >
        {error.message || "(no message)"}
      </pre>
      <div style={{ marginTop: 12, display: "flex", gap: 8 }}>
        <button
          onClick={onRetry}
          style={{
            background: W.brand,
            color: "#0B0F1A",
            border: `1px solid ${W.brand}`,
            borderRadius: 2,
            padding: "5px 12px",
            fontSize: 12,
            fontWeight: 600,
            cursor: "pointer",
          }}
        >
          Retry
        </button>
      </div>
    </div>
  );
}
