import { useEffect, useState } from "react";
import {
  ApiError,
  fetchContractDetail,
  type ContractDetail,
  type ContractRow,
  type Role,
} from "../api";
import { W, wMono, wideCaps, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";
import { MonoId } from "../components/MonoId";
import { IcX } from "../components/icons";

// Right-side overlay showing a contract's deep view (payload, parties,
// archive metadata), falling back to row-level ACS fields while it loads.
// The parent owns J/K row navigation since it holds the filtered table state.
export interface ContractDetailDrawerProps {
  instance: string;
  role: Role;
  /** Row data we already have from the ACS snapshot. */
  row: ContractRow;
  onClose: () => void;
  onPrev?: () => void;
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

  // Esc / J / K on window; skip when an editable element is focused so
  // typing in the search box doesn't trigger navigation.
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

  return (
    <aside
      aria-label="Contract detail"
      style={{
        position: "fixed",
        top: 52,
        right: 0,
        bottom: 0,
        width: "min(480px, 92vw)",
        background: W.surface2,
        borderLeft: `1px solid ${W.borderHi}`,
        // Below the CommandPalette (zIndex 100), above page content.
        zIndex: 40,
        overscrollBehavior: "contain",
        overflowY: "auto",
      }}
    >
      <header
        style={{
          padding: "14px 16px",
          borderBottom: `1px solid ${W.border}`,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Pill color={detail.archived ? W.rose : W.brand}>
            {detail.archived ? "archived" : "active"}
          </Pill>
          <Pill color={W.mag}>
            visible to {detail.signatories.length + detail.observers.length}
          </Pill>
          <span style={{ marginLeft: "auto" }} />
          <Button
            variant="ghost"
            icon={<IcX />}
            aria-label="Close detail drawer"
            title="Close (esc)"
            onClick={onClose}
          />
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
          <span style={{ fontWeight: 600, fontSize: fs.body, color: W.text }}>
            {shortTemplateLabel(detail.template_id)}
          </span>
          {detail.package_name && (
            <a
              href={`#/packages/${encodeURIComponent(detail.package_name)}`}
              style={{
                color: W.dim,
                fontSize: fs.small,
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
          }}
        >
          <MonoId
            value={detail.contract_id}
            head={10}
            tail={8}
            size={fs.small}
            color={W.mag}
          />
        </div>
        {state.kind === "loading" && (
          <div
            style={{
              marginTop: 6,
              color: W.dim,
              fontSize: fs.small,
              fontFamily: wMono,
            }}
          >
            Loading full detail…
          </div>
        )}
        {state.kind === "err" && (
          <div
            style={{
              marginTop: 6,
              color: W.err,
              fontSize: fs.small,
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
        <PayloadNode value={detail.payload ?? {}} depth={0} />
      </Section>
      {detail.created_at && (
        <Section label="Created">
          <div style={{ fontFamily: wMono, fontSize: fs.small, color: W.text }}>
            {detail.created_at}
          </div>
          {detail.created_update_id && (
            <a
              href={`#/transactions/${encodeURIComponent(detail.created_update_id)}`}
              title={detail.created_update_id}
              style={{
                marginTop: 4,
                display: "inline-block",
                color: W.brand,
                fontFamily: wMono,
                fontSize: fs.small,
                fontVariantNumeric: "tabular-nums",
                textDecoration: "none",
              }}
            >
              tx · {truncMid(detail.created_update_id)}
            </a>
          )}
        </Section>
      )}
      {detail.archived && (
        <Section label="Archived">
          {detail.archived_at && (
            <div
              style={{ fontFamily: wMono, fontSize: fs.small, color: W.text }}
            >
              {detail.archived_at}
            </div>
          )}
          {detail.archived_offset !== undefined && (
            <div
              style={{
                color: W.dim,
                fontSize: fs.small,
                marginTop: 3,
                fontVariantNumeric: "tabular-nums",
              }}
            >
              offset {detail.archived_offset.toLocaleString()}
            </div>
          )}
          {detail.archived_update_id && (
            <a
              href={`#/transactions/${encodeURIComponent(detail.archived_update_id)}`}
              title={detail.archived_update_id}
              style={{
                marginTop: 4,
                display: "inline-block",
                color: W.brand,
                fontFamily: wMono,
                fontSize: fs.small,
                fontVariantNumeric: "tabular-nums",
                textDecoration: "none",
              }}
            >
              tx · {truncMid(detail.archived_update_id)}
            </a>
          )}
        </Section>
      )}
      <div
        style={{
          padding: "10px 14px",
          borderTop: `1px solid ${W.border}`,
          color: W.dim,
          fontSize: fs.small,
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

function shortTemplateLabel(tpl: string | undefined): string {
  if (!tpl) return "—";
  const parts = tpl.split(":");
  return parts.length >= 3 ? `${parts[1]}:${parts[2]}` : tpl;
}

// Middle-truncate an id, keeping both ends (the suffix is discriminating).
function truncMid(s: string, head = 8, tail = 6): string {
  if (s.length <= head + tail + 1) return s;
  return `${s.slice(0, head)}…${s.slice(-tail)}`;
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
          fontSize: fs.caption,
          ...wideCaps,
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
        background: tint(color, 13),
        border: `1px solid ${tint(color, 34)}`,
        color,
        padding: "1px 8px",
        borderRadius: R.control,
        fontSize: fs.caption,
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
    <div style={{ color: W.dim, fontSize: fs.small, padding: "2px 0" }}>
      {children}
    </div>
  );
}

function PartyChip({ party, kind }: { party: string; kind: "sig" | "obs" }) {
  const color = kind === "sig" ? W.brand : W.mag;
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
          borderRadius: R.control,
          background: color,
          flexShrink: 0,
        }}
      />
      <MonoId value={party} head={12} tail={8} size={fs.small} color={W.text2} />
    </div>
  );
}

// Recursive JSON-like view of the contract payload; each level indents 12px.
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
                fontSize: fs.small,
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
    fontSize: fs.small,
    color: kind === "dim" ? W.dim : kind === "num" ? W.warn : W.text2,
    fontVariantNumeric: kind === "num" ? "tabular-nums" : undefined,
    wordBreak: "break-word",
  };
}
