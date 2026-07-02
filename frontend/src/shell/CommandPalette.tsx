import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { W, wMono, wSans } from "../tokens";
import { useInstanceSelection } from "./useInstanceSelection";
import { NAV, isInstanceScoped, linkTo } from "./routes";

// CommandPalette — ⌘K (Ctrl+K on non-Mac) launches a centred search
// modal with two action groups: route navigation and instance
// switching. Keyboard model is intentionally simple: ↑/↓ to move,
// Enter to activate, Esc to dismiss — every press has one obvious
// meaning.

interface Action {
  id: string;
  // Group label rendered as a subtle section header in the list.
  group: "Navigate" | "Switch instance";
  label: string;
  // Secondary line shown beneath the label (path, instance status).
  hint?: string;
  // perform is the side-effect — navigate, mutate, etc.
  perform: () => void;
}

// Derive the palette's nav rows from the shared NAV table so the
// sidebar and the palette can never drift on routes / labels /
// instance-scoping. Adding a tab to ./routes wires both surfaces.
const NAV_ACTIONS: Array<Omit<Action, "perform"> & { path: string }> = NAV.map(
  (n) => ({
    id: `nav-${n.to === "/" ? "overview" : n.to.replace(/^\//, "")}`,
    group: "Navigate",
    label: n.label,
    hint: n.to,
    path: n.to,
  }),
);

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const sel = useInstanceSelection();
  // Carry `?instance=` into instance-scoped routes (same as the Shell
  // sidebar); otherwise ⌘K → "Wallet" lands on the empty state even
  // though the header still shows an instance.
  const [searchParams] = useSearchParams();
  const instance = searchParams.get("instance");

  // Global hotkey. ⌘K on Mac, Ctrl+K elsewhere — same as VS Code,
  // Slack, GitHub. We use the e.metaKey || e.ctrlKey gate rather
  // than UA-sniffing because either is acceptable on any platform
  // (some keyboard layouts swap them).
  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((v) => !v);
      } else if (e.key === "Escape" && open) {
        setOpen(false);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  // Auto-focus the input when the palette opens; reset query +
  // cursor so each open is a fresh search.
  useEffect(() => {
    if (open) {
      setQuery("");
      setCursor(0);
      // Defer to next frame — the input isn't in the DOM until
      // the modal renders, and React batches the state update.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  const actions = useMemo<Action[]>(() => {
    const nav = NAV_ACTIONS.map<Action>((a) => ({
      ...a,
      perform: () => navigate(linkTo(a.path, isInstanceScoped(a.path), instance)),
    }));
    const instances = sel.instances.map<Action>((i) => ({
      id: `inst-${i.name}`,
      group: "Switch instance",
      label: i.name,
      hint: `${i.status} · ${i.splice_version}`,
      perform: () => sel.select(i.name),
    }));
    return [...nav, ...instances];
  }, [navigate, sel, instance]);

  const filtered = useMemo(() => filter(actions, query), [actions, query]);

  // Keep cursor in range as the filter narrows.
  useEffect(() => {
    if (cursor >= filtered.length) {
      setCursor(Math.max(0, filtered.length - 1));
    }
  }, [filtered.length, cursor]);

  const activate = useCallback(
    (a: Action) => {
      a.perform();
      setOpen(false);
    },
    [],
  );

  function onInputKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setCursor((c) => Math.min(filtered.length - 1, c + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setCursor((c) => Math.max(0, c - 1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      const a = filtered[cursor];
      if (a) activate(a);
    }
  }

  if (!open) return null;
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Command palette"
      onClick={() => setOpen(false)}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.5)",
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "center",
        paddingTop: "12vh",
        zIndex: 100,
        fontFamily: wSans,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: "min(560px, 92vw)",
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 12,
          boxShadow: "0 24px 64px rgba(0,0,0,0.6)",
          overflow: "hidden",
        }}
      >
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setCursor(0);
          }}
          onKeyDown={onInputKey}
          placeholder="Type a command, route, or instance name…"
          aria-label="Command palette search"
          style={{
            width: "100%",
            background: "transparent",
            border: "none",
            borderBottom: `1px solid ${W.border}`,
            color: W.text,
            fontSize: 14,
            padding: "14px 18px",
            outline: "none",
            fontFamily: wSans,
          }}
        />
        <ul
          role="listbox"
          aria-label="Matching commands"
          style={{
            margin: 0,
            padding: 6,
            listStyle: "none",
            maxHeight: "50vh",
            overflowY: "auto",
          }}
        >
          {filtered.length === 0 && (
            <li style={{ padding: "16px 12px", color: W.dim, fontSize: 13 }}>
              No matches for <code style={{ color: W.text2, fontFamily: wMono }}>{query}</code>
            </li>
          )}
          {renderGroups(filtered, cursor, activate)}
        </ul>
        <footer
          style={{
            borderTop: `1px solid ${W.border}`,
            padding: "8px 14px",
            color: W.dim,
            fontSize: 11,
            display: "flex",
            gap: 16,
          }}
        >
          <Hotkey label="↑↓" hint="navigate" />
          <Hotkey label="↵" hint="select" />
          <Hotkey label="esc" hint="dismiss" />
        </footer>
      </div>
    </div>
  );
}

// renderGroups groups consecutive actions sharing a `group` label
// and inserts a subtle section header before each block. Keeps the
// list scannable: nav actions cluster, instance actions cluster.
function renderGroups(
  filtered: Action[],
  cursor: number,
  activate: (a: Action) => void,
) {
  const out: JSX.Element[] = [];
  let lastGroup: string | null = null;
  filtered.forEach((a, i) => {
    if (a.group !== lastGroup) {
      out.push(
        <li
          key={`group-${a.group}`}
          aria-hidden
          style={{
            padding: "10px 12px 4px",
            color: W.dim,
            fontSize: 10.5,
            textTransform: "uppercase",
            letterSpacing: 1.1,
          }}
        >
          {a.group}
        </li>,
      );
      lastGroup = a.group;
    }
    const isCursor = i === cursor;
    out.push(
      <li key={a.id}>
        <button
          role="option"
          aria-selected={isCursor}
          onClick={() => activate(a)}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 12,
            width: "100%",
            padding: "8px 12px",
            background: isCursor ? `${W.brand}1A` : "transparent",
            border: "none",
            borderRadius: 6,
            color: W.text,
            textAlign: "left",
            cursor: "pointer",
            fontFamily: wSans,
            fontSize: 13,
          }}
        >
          <span style={{ flex: 1 }}>{a.label}</span>
          {a.hint && (
            <span style={{ color: W.dim, fontFamily: wMono, fontSize: 11 }}>
              {a.hint}
            </span>
          )}
        </button>
      </li>,
    );
  });
  return out;
}

function Hotkey({ label, hint }: { label: string; hint: string }) {
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
      <kbd
        style={{
          background: W.surface2,
          border: `1px solid ${W.border}`,
          borderRadius: 4,
          padding: "1px 5px",
          fontFamily: wMono,
          fontSize: 10,
          color: W.text2,
        }}
      >
        {label}
      </kbd>
      {hint}
    </span>
  );
}

// filter — case-insensitive substring match across label + hint.
// Deliberately not a fuzzy matcher: the palette has ~10–20 items and
// exact substring is easier to reason about. Results keep input order
// (NAV first, instances after) so the visual grouping stays stable.
export function filter(actions: Action[], query: string): Action[] {
  const q = query.trim().toLowerCase();
  if (!q) return actions;
  return actions.filter((a) => {
    const hay = `${a.label} ${a.hint ?? ""}`.toLowerCase();
    return hay.includes(q);
  });
}
