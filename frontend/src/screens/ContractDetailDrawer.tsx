import { useEffect, useState } from "react";
import {
  ApiError,
  fetchContractDetail,
  type ContractDetail,
  type ContractRow,
  type Role,
} from "../api";
import { W, wMono } from "../tokens";

// ContractDetailDrawer — BIT-231 feature #2.
//
// Slides in from the right of the ACS table when a row is clicked.
// Fetches the deep view from /api/instances/{name}/contracts/{cid}
// (EventQueryService.GetEventsByContractId behind the scenes) so the
// drawer can show the create event's full payload, signatories,
// observers, and — if the contract has been archived — the archive
// offset.
//
// While the deep view is loading the drawer immediately shows the
// row-level fields that came from the ACS snapshot so the user
// always has something to read.
//
// Keyboard behaviour (parent wires the handlers, the drawer renders
// the help line):
//   - Esc    → close the drawer
//   - J / K  → next / previous row in the underlying table
//
// The parent owns row navigation because it owns the filtered table
// state. The drawer itself only owns the deep-view fetch lifecycle.

export interface ContractDetailDrawerProps {
  instance: string;
  role: Role;
  /** Row data we already have from the ACS snapshot. */
  row: ContractRow;
  /** Close the drawer. */
  onClose: () => void;
  /** Move selection to the previous row (K / ArrowUp). */
  onPrev?: () => void;
  /** Move selection to the next row (J / ArrowDown). */
  onNext?: () => void;
}

export function ContractDetailDrawer({
  instance,
  role,
  row,
  onClose,
  onPrev,
  onNext,
}: ContractDetailDrawerProps) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; detail: ContractDetail }
    | { kind: "err"; message: string }
  >({ kind: "loading" });
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    fetchContractDetail(instance, row.contract_id, role)
      .then((resp) => {
        if (cancelled) return;
        setState({ kind: "ok", detail: resp.contract });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        const msg =
          e instanceof ApiError
            ? e.message
            : "failed to load contract detail";
        setState({ kind: "err", message: msg });
      });
    return () => {
      cancelled = true;
    };
  }, [instance, role, row.contract_id]);

  // Keyboard: Esc / J / K. We add to window so the drawer responds
  // to keystrokes from anywhere on the page, mirroring the
  // ExplorerScreen's "/" shortcut. INPUT/TEXTAREA/contenteditable
  // are ignored so users typing in the search box don't trigger
  // navigation.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const active = document.activeElement as HTMLElement | null;
      const inEditable =
        active &&
        (active.tagName === "INPUT" ||
          active.tagName === "TEXTAREA" ||
          active.isContentEditable);
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
        return;
      }
      if (inEditable) return;
      if ((e.key === "j" || e.key === "ArrowDown") && onNext) {
        e.preventDefault();
        onNext();
      } else if ((e.key === "k" || e.key === "ArrowUp") && onPrev) {
        e.preventDefault();
        onPrev();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, onNext, onPrev]);

  // Prefer deep-view fields when present; fall back to the row.
  const detail: ContractDetail =
    state.kind === "ok"
      ? state.detail
      : {
          contract_id: row.contract_id,
          template_id: row.template_id,
          package_name: row.package_name,
          payload: row.payload ?? {},
          signatories: row.signatories,
          observers: row.observers,
          created_at: row.created_at,
          archived: false,
        };

  const copyCid = async () => {
    try {
      await navigator.clipboard.writeText(detail.contract_id);
      setCopied(true);
      setTimeout(() => setCopied(false), 1100);
    } catch {
      // clipboard may be unavailable (http on non-localhost); silent
    }
  };

  return (
    <aside
      aria-label="Contract detail"
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: 10,
        overflow: "hidden",
        position: "relative",
      }}
    >
      <header
        style={{
          padding: "14px 16px",
          borderBottom: `1px solid ${W.border}`,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Pill color={detail.archived ? "#F08FB5" : W.brand}>
            {detail.archived ? "archived" : "active"}
          </Pill>
          <Pill color="#7CB5F7">
            visible to {detail.signatories.length + detail.observers.length}
          </Pill>
          <span style={{ marginLeft: "auto" }} />
          <button
            type="button"
            onClick={onClose}
            aria-label="Close detail drawer"
            style={{
              background: "transparent",
              border: `1px solid ${W.border}`,
              color: W.dim,
              fontFamily: wMono,
              fontSize: 11,
              padding: "2px 6px",
              borderRadius: 4,
              cursor: "pointer",
            }}
          >
            esc
          </button>
        </div>
        <div
          style={{
            marginTop: 8,
            display: "flex",
            alignItems: "baseline",
            gap: 8,
            flexWrap: "wrap",
          }}
        >
          <span style={{ fontWeight: 600, fontSize: 14, color: W.text }}>
            {shortTemplateLabel(detail.template_id)}
          </span>
          {detail.package_name && (
            <a
              href={`#/packages/${encodeURIComponent(detail.package_name)}`}
              style={{
                color: W.dim,
                fontSize: 11.5,
                fontFamily: wMono,
                textDecoration: "none",
              }}
              title={`Package ${detail.package_name}`}
            >
              {detail.package_name}
            </a>
          )}
        </div>
        <div
          style={{
            marginTop: 6,
            display: "flex",
            alignItems: "center",
            gap: 6,
            color: "#C4A8F5",
            fontFamily: wMono,
            fontSize: 11.5,
            wordBreak: "break-all",
          }}
        >
          <span>{detail.contract_id}</span>
          <button
            type="button"
            onClick={copyCid}
            aria-label="Copy contract id"
            style={{
              background: "transparent",
              border: `1px solid ${W.border}`,
              color: W.dim,
              fontFamily: wMono,
              fontSize: 10,
              padding: "1px 5px",
              borderRadius: 3,
              cursor: "pointer",
            }}
          >
            {copied ? "copied" : "copy"}
          </button>
        </div>
        {state.kind === "loading" && (
          <div
            style={{
              marginTop: 6,
              color: W.dim,
              fontSize: 11,
              fontFamily: wMono,
            }}
          >
            loading full detail…
          </div>
        )}
        {state.kind === "err" && (
          <div
            style={{
              marginTop: 6,
              color: "#F08FB5",
              fontSize: 11,
            }}
          >
            {state.message}
          </div>
        )}
      </header>

      <Section label="Signatories">
        {detail.signatories.length === 0 ? (
          <Hint>None</Hint>
        ) : (
          detail.signatories.map((p) => <PartyChip key={p} party={p} kind="sig" />)
        )}
      </Section>
      {detail.observers.length > 0 && (
        <Section label="Observers">
          {detail.observers.map((p) => (
            <PartyChip key={p} party={p} kind="obs" />
          ))}
        </Section>
      )}
      <Section label="Payload">
        <PayloadRenderer value={detail.payload ?? {}} />
      </Section>
      {detail.created_at && (
        <Section label="Created">
          <div style={{ fontFamily: wMono, fontSize: 11.5, color: W.text }}>
            {detail.created_at}
          </div>
          {detail.created_update_id && (
            <a
              href={`#/transactions/${encodeURIComponent(detail.created_update_id)}`}
              style={{
                marginTop: 4,
                display: "inline-block",
                color: W.brand,
                fontFamily: wMono,
                fontSize: 11,
                textDecoration: "none",
              }}
            >
              tx · {detail.created_update_id.slice(0, 16)}…
            </a>
          )}
        </Section>
      )}
      {detail.archived && (
        <Section label="Archived">
          {detail.archived_at && (
            <div
              style={{ fontFamily: wMono, fontSize: 11.5, color: W.text }}
            >
              {detail.archived_at}
            </div>
          )}
          {detail.archived_offset !== undefined && (
            <div style={{ color: W.dim, fontSize: 11, marginTop: 3 }}>
              offset {detail.archived_offset.toLocaleString()}
            </div>
          )}
          {detail.archived_update_id && (
            <a
              href={`#/transactions/${encodeURIComponent(detail.archived_update_id)}`}
              style={{
                marginTop: 4,
                display: "inline-block",
                color: W.brand,
                fontFamily: wMono,
                fontSize: 11,
                textDecoration: "none",
              }}
            >
              tx · {detail.archived_update_id.slice(0, 16)}…
            </a>
          )}
        </Section>
      )}
      <div
        style={{
          padding: "10px 14px",
          borderTop: `1px solid ${W.border}`,
          color: W.dim,
          fontSize: 11,
          fontFamily: wMono,
          display: "flex",
          justifyContent: "space-between",
        }}
      >
        <span>esc · close</span>
        <span>j/k · next/prev</span>
      </div>
    </aside>
  );
}

// ── helpers ──────────────────────────────────────────────────────

function shortTemplateLabel(tpl: string | undefined): string {
  if (!tpl) return "—";
  const parts = tpl.split(":");
  return parts.length >= 3 ? `${parts[1]}:${parts[2]}` : tpl;
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div style={{ padding: "12px 16px", borderBottom: `1px solid ${W.border}` }}>
      <div
        style={{
          color: W.dim,
          fontSize: 10.5,
          letterSpacing: 1.4,
          textTransform: "uppercase",
          fontWeight: 600,
          marginBottom: 6,
        }}
      >
        {label}
      </div>
      {children}
    </div>
  );
}

function Pill({
  color,
  children,
}: {
  color: string;
  children: React.ReactNode;
}) {
  return (
    <span
      style={{
        background: `${color}1A`,
        border: `1px solid ${color}44`,
        color,
        padding: "1px 8px",
        borderRadius: 4,
        fontSize: 10.5,
        fontWeight: 600,
        fontFamily: wMono,
      }}
    >
      {children}
    </span>
  );
}

function Hint({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ color: W.dim, fontSize: 11.5, padding: "2px 0" }}>
      {children}
    </div>
  );
}

function PartyChip({ party, kind }: { party: string; kind: "sig" | "obs" }) {
  const color = kind === "sig" ? "#5BD7C5" : "#7CB5F7";
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 6,
        marginBottom: 3,
      }}
    >
      <span
        title={kind === "sig" ? "signatory" : "observer"}
        style={{
          width: 6,
          height: 6,
          borderRadius: 2,
          background: color,
          flexShrink: 0,
        }}
      />
      <code
        style={{
          fontFamily: wMono,
          fontSize: 11,
          color: W.text2,
          wordBreak: "break-all",
        }}
      >
        {party}
      </code>
    </div>
  );
}

// PayloadRenderer — recursive JSON-like view for the contract's
// payload. Renders objects as label : value pairs, arrays as
// bracketed lists, primitives in place. Each level indents by 12 px
// — enough to see structure without burning horizontal space in
// the 380 px drawer.
function PayloadRenderer({ value }: { value: unknown }): JSX.Element {
  return <PayloadNode value={value} depth={0} />;
}

function PayloadNode({
  value,
  depth,
}: {
  value: unknown;
  depth: number;
}): JSX.Element {
  if (value === null || value === undefined) {
    return <span style={primStyle("dim")}>null</span>;
  }
  if (typeof value === "string") {
    return <span style={primStyle("text")}>&quot;{value}&quot;</span>;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return <span style={primStyle("num")}>{String(value)}</span>;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) return <span style={primStyle("dim")}>[]</span>;
    return (
      <div style={{ marginLeft: depth === 0 ? 0 : 12 }}>
        {value.map((v, i) => (
          <div key={i} style={{ display: "flex", gap: 6 }}>
            <span style={primStyle("dim")}>{i}.</span>
            <PayloadNode value={v} depth={depth + 1} />
          </div>
        ))}
      </div>
    );
  }
  if (typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>);
    if (entries.length === 0) return <span style={primStyle("dim")}>{`{}`}</span>;
    return (
      <div style={{ marginLeft: depth === 0 ? 0 : 12 }}>
        {entries.map(([k, v]) => (
          <div
            key={k}
            style={{ display: "flex", gap: 6, marginBottom: 2, flexWrap: "wrap" }}
          >
            <span
              style={{
                color: W.dim,
                fontFamily: wMono,
                fontSize: 11,
              }}
            >
              {k}:
            </span>
            <PayloadNode value={v} depth={depth + 1} />
          </div>
        ))}
      </div>
    );
  }
  return <span style={primStyle("text")}>{String(value)}</span>;
}

function primStyle(kind: "text" | "num" | "dim"): React.CSSProperties {
  return {
    fontFamily: wMono,
    fontSize: 11,
    color: kind === "dim" ? W.dim : kind === "num" ? "#F5BF55" : W.text2,
    wordBreak: "break-all",
  };
}
