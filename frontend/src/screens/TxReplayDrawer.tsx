import { useEffect, useState } from "react";
import {
  ApiError,
  fetchTxReplay,
  type Role,
  type TxReplayEvent,
  type TxReplayResponse,
} from "../api";
import { W, wMono } from "../tokens";

// TxReplayDrawer — the Web UI counterpart of `dpm localnet tx replay
// --id <id>`. Fetches one transaction with the LEDGER_EFFECTS shape
// (exercised choices, not just the ACS delta) projected through a
// party set and renders the event tree. The party selector answers
// "what did party P see in this transaction?" — the same id queried
// as different parties returns different event sets.

const EVENT_COLOR: Record<TxReplayEvent["kind"], string> = {
  created: "#7CC89A",
  archived: "#7BD2C6",
  exercised: "#8FA3EE",
};

export function TxReplayDrawer({
  instance,
  role,
  updateId,
  partyOptions,
  onClose,
}: {
  instance: string;
  role: Role;
  updateId: string;
  /** Party ids the user can project through (the ACS facet parties). */
  partyOptions: string[];
  onClose: () => void;
}) {
  // "" = project through the JWT's own parties (the default the
  // backend uses when no ?party is passed).
  const [party, setParty] = useState<string>("");
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: TxReplayResponse }
    | { kind: "needs-jwt"; remediation: string }
    | { kind: "not-visible" }
    | { kind: "err"; error: string }
  >({ kind: "loading" });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchTxReplay(instance, updateId, role, party ? [party] : undefined)
      .then((data) => {
        if (!cancelled) setState({ kind: "ok", data });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.code === "NOT_FOUND") {
          setState({ kind: "not-visible" });
          return;
        }
        if (e instanceof ApiError && e.code === "EXPLORER_NEEDS_PARTY_JWT") {
          setState({
            kind: "needs-jwt",
            remediation: e.remediation?.[0] ?? "Grant actAs/readAs rights.",
          });
          return;
        }
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to replay",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [instance, updateId, role, party]);

  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 4,
        overflow: "hidden",
      }}
    >
      <header
        style={{
          padding: "14px 16px",
          borderBottom: `1px solid ${W.border}`,
          display: "flex",
          alignItems: "center",
          gap: 10,
        }}
      >
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ color: W.text, fontSize: 13.5, fontWeight: 600 }}>
            Replay · per-party projection
          </div>
          <code
            style={{
              color: "#93A7F0",
              fontFamily: wMono,
              fontSize: 11,
              wordBreak: "break-all",
            }}
            title={updateId}
          >
            {updateId}
          </code>
        </div>
        <button
          onClick={onClose}
          aria-label="Close replay"
          style={{
            background: "transparent",
            border: `1px solid ${W.border}`,
            color: W.dim,
            borderRadius: 2,
            padding: "3px 8px",
            cursor: "pointer",
            fontSize: 12,
          }}
        >
          esc
        </button>
      </header>

      <div
        style={{
          padding: "10px 16px",
          borderBottom: `1px solid ${W.border}`,
          display: "flex",
          alignItems: "center",
          gap: 8,
        }}
      >
        <span style={{ color: W.dim, fontSize: 11.5 }}>visible to</span>
        <select
          value={party}
          onChange={(e) => setParty(e.target.value)}
          aria-label="Project replay through party"
          style={{
            background: W.border,
            border: `1px solid ${W.border}`,
            color: W.text,
            fontFamily: wMono,
            fontSize: 11.5,
            padding: "4px 8px",
            borderRadius: 2,
            cursor: "pointer",
            maxWidth: 240,
          }}
        >
          <option value="">my parties (JWT default)</option>
          {partyOptions.map((p) => (
            <option key={p} value={p}>
              {shortParty(p)}
            </option>
          ))}
        </select>
      </div>

      {state.kind === "loading" && (
        <div style={{ padding: 16, color: W.dim, fontSize: 13 }}>
          Replaying transaction…
        </div>
      )}
      {state.kind === "err" && (
        <div style={{ padding: 16, color: "#7BD2C6", fontSize: 13 }}>
          {state.error}
        </div>
      )}
      {state.kind === "not-visible" && (
        <div style={{ padding: 16, color: W.dim, fontSize: 13 }}>
          This transaction is not visible to the selected party.
        </div>
      )}
      {state.kind === "needs-jwt" && (
        <div style={{ padding: 16, color: W.dim, fontSize: 13 }}>
          {state.remediation}
        </div>
      )}
      {state.kind === "ok" && (
        <>
          <div
            style={{
              padding: "10px 16px",
              color: W.dim,
              fontSize: 11.5,
              borderBottom: `1px solid ${W.border}`,
            }}
          >
            offset{" "}
            <span style={{ color: W.text2, fontFamily: wMono }}>
              {state.data.offset.toLocaleString()}
            </span>{" "}
            · {state.data.event_count}{" "}
            {state.data.event_count === 1 ? "event" : "events"} visible
            {state.data.workflow_id ? ` · ${state.data.workflow_id}` : ""}
          </div>
          <div style={{ padding: "10px 16px", maxHeight: "55vh", overflowY: "auto" }}>
            {state.data.events.length === 0 ? (
              <div style={{ color: W.dim, fontSize: 12.5 }}>
                No events in this transaction are visible to the selected
                party.
              </div>
            ) : (
              state.data.events.map((ev, i) => (
                <ReplayNode
                  key={`${ev.node_id}-${i}`}
                  ev={ev}
                  last={i === state.data.events.length - 1}
                />
              ))
            )}
          </div>
        </>
      )}
    </div>
  );
}

function ReplayNode({ ev, last }: { ev: TxReplayEvent; last: boolean }) {
  const detail =
    ev.kind === "exercised"
      ? `${ev.choice ?? ""}${ev.consuming ? " (consuming)" : ""}${
          ev.acting_parties && ev.acting_parties.length > 0
            ? ` by ${ev.acting_parties.map(shortParty).join(", ")}`
            : ""
        }`
      : ev.kind === "created" && ev.signatories && ev.signatories.length > 0
        ? ev.signatories.map(shortParty).join(", ")
        : "";
  return (
    <div
      style={{
        display: "flex",
        gap: 10,
        alignItems: "baseline",
        marginBottom: 5,
        fontSize: 11.5,
      }}
    >
      <span style={{ color: W.dim, fontFamily: wMono, fontSize: 11, width: 14 }}>
        {last ? "└─" : "├─"}
      </span>
      <span
        style={{ color: EVENT_COLOR[ev.kind], fontWeight: 600, fontFamily: wMono }}
      >
        {ev.kind}
      </span>
      <span
        style={{
          color: W.text,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          maxWidth: 160,
        }}
        title={ev.template_id}
      >
        {ev.template_id ? ev.template_id.split(":").slice(1).join(":") : "—"}
      </span>
      {detail && (
        <span style={{ color: W.text2, fontSize: 11 }}>{detail}</span>
      )}
      <code
        style={{
          fontFamily: wMono,
          color: "#93A7F0",
          fontSize: 10.5,
          marginLeft: "auto",
        }}
        title={ev.contract_id}
      >
        {ev.contract_id.slice(0, 16)}…
      </code>
    </div>
  );
}

function shortParty(p: string): string {
  const [name, hash] = p.split("::");
  if (!hash) return p;
  return `${name}::${hash.slice(0, 6)}…`;
}
