import { useEffect, useState } from "react";
import {
  ApiError,
  fetchTxReplay,
  type Role,
  type TxReplayEvent,
  type TxReplayResponse,
} from "../api";
import { W, wMono, R } from "../tokens";
import { Button } from "../components/Button";
import { MonoId } from "../components/MonoId";
import { IcX } from "../components/icons";

// TxReplayDrawer — the Web UI counterpart of `dpm localnet tx replay
// --id <id>`, rendered as a fixed right-side overlay below the topbar
// so the transactions table keeps its full width.
// Fetches one transaction with the LEDGER_EFFECTS shape
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
      aria-label="Transaction replay"
      style={{
        position: "fixed",
        top: 52,
        right: 0,
        bottom: 0,
        width: "min(480px, 92vw)",
        // Raised surface — a fixed overlay sits above the page, and
        // surface-on-page was reading dark-on-dark. One depth technique
        // for a dense-console drawer: hairline border, no shadow.
        background: W.surface2,
        borderLeft: `1px solid ${W.borderHi}`,
        // Below the CommandPalette (zIndex 100) but above page content.
        zIndex: 40,
        overscrollBehavior: "contain",
        overflowY: "auto",
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
          <MonoId value={updateId} head={10} tail={8} size={11} color={W.mag} />
        </div>
        <Button
          variant="ghost"
          icon={<IcX />}
          aria-label="Close replay"
          title="Close (esc)"
          onClick={onClose}
        />
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
            borderRadius: R.control,
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
        <div style={{ padding: 16, color: W.err, fontSize: 13 }}>
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
            <span
              style={{
                color: W.text2,
                fontFamily: wMono,
                fontVariantNumeric: "tabular-nums",
              }}
            >
              {state.data.offset.toLocaleString()}
            </span>{" "}
            · {state.data.event_count}{" "}
            {state.data.event_count === 1 ? "event" : "events"} visible
            {state.data.workflow_id ? ` · ${state.data.workflow_id}` : ""}
          </div>
          <div style={{ padding: "10px 16px" }}>
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
      <MonoId
        value={ev.contract_id}
        head={8}
        tail={6}
        size={10.5}
        color={W.mag}
        style={{ marginLeft: "auto" }}
      />
    </div>
  );
}

function shortParty(p: string): string {
  const [name, hash] = p.split("::");
  if (!hash) return p;
  return `${name}::${hash.slice(0, 6)}…`;
}
