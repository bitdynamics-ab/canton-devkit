import { useEffect, useState } from "react";
import {
  ApiError,
  fetchSkills,
  installSkills,
  type Skill,
} from "../api";
import { W, wMono } from "../tokens";

// AgentSkillsScreen — BIT-189.
//
// Browses the bundled AI-agent skill docs (served by /api/skills,
// the SAME embedded markdown the CLI `localnet skills` command
// ships) and offers one-click install into ~/.claude/skills or
// ~/.codex/skills. CLI ↔ UI parity: both surfaces read internal/skills.
export function AgentSkillsScreen() {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; skills: Skill[] }
    | { kind: "err"; error: string }
  >({ kind: "loading" });
  const [selected, setSelected] = useState<string | null>(null);
  const [install, setInstall] = useState<
    | { kind: "idle" }
    | { kind: "busy"; target: string }
    | { kind: "done"; dir: string; count: number }
    | { kind: "err"; message: string }
  >({ kind: "idle" });

  useEffect(() => {
    let cancelled = false;
    fetchSkills()
      .then((r) => {
        if (cancelled) return;
        setState({ kind: "ok", skills: r.skills });
        setSelected(r.skills[0]?.filename ?? null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setState({
          kind: "err",
          error: e instanceof ApiError ? e.message : "failed to load skills",
        });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function doInstall(target: "claude" | "codex") {
    setInstall({ kind: "busy", target });
    try {
      const resp = await installSkills(target);
      setInstall({ kind: "done", dir: resp.dir, count: resp.count });
    } catch (e) {
      setInstall({
        kind: "err",
        message: e instanceof ApiError ? e.message : "install failed",
      });
    }
  }

  if (state.kind === "loading") {
    return (
      <section style={{ padding: 24 }}>
        <Header />
        <p style={{ color: W.dim, fontSize: 13 }}>Loading skills…</p>
      </section>
    );
  }
  if (state.kind === "err") {
    return (
      <section style={{ padding: 24 }}>
        <Header />
        <p role="alert" style={{ color: W.err, fontSize: 13 }}>{state.error}</p>
      </section>
    );
  }

  const active = state.skills.find((s) => s.filename === selected) ?? null;

  return (
    <section
      style={{
        padding: 24,
        height: "calc(100vh - 56px)",
        display: "flex",
        flexDirection: "column",
        gap: 14,
      }}
    >
      <Header />

      {/* Install bar */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 10,
          padding: "10px 14px",
          flexWrap: "wrap",
        }}
      >
        <span style={{ color: W.text2, fontSize: 12.5 }}>
          Install all {state.skills.length} skills into:
        </span>
        <InstallButton
          label="~/.claude/skills"
          busy={install.kind === "busy" && install.target === "claude"}
          onClick={() => doInstall("claude")}
        />
        <InstallButton
          label="~/.codex/skills"
          busy={install.kind === "busy" && install.target === "codex"}
          onClick={() => doInstall("codex")}
        />
        {install.kind === "done" && (
          <span style={{ color: W.ok, fontSize: 12, fontFamily: wMono }}>
            ✓ {install.count} installed → {install.dir}
          </span>
        )}
        {install.kind === "err" && (
          <span role="alert" style={{ color: W.err, fontSize: 12 }}>
            ✗ {install.message}
          </span>
        )}
      </div>

      {/* Two-pane: list | preview */}
      <div
        style={{
          flex: 1,
          minHeight: 0,
          display: "grid",
          gridTemplateColumns: "300px 1fr",
          gap: 14,
        }}
      >
        <div
          style={{
            background: W.surface,
            border: `1px solid ${W.border}`,
            borderRadius: 10,
            overflow: "auto",
          }}
        >
          {state.skills.map((s) => {
            const isActive = s.filename === selected;
            return (
              <button
                key={s.filename}
                onClick={() => setSelected(s.filename)}
                style={{
                  display: "block",
                  width: "100%",
                  textAlign: "left",
                  padding: "10px 14px",
                  background: isActive ? W.surface2 : "transparent",
                  border: "none",
                  borderLeft: `2px solid ${isActive ? W.brand : "transparent"}`,
                  cursor: "pointer",
                  color: isActive ? W.text : W.text2,
                }}
              >
                <div style={{ fontWeight: 600, fontSize: 13 }}>{s.name}</div>
                <div style={{ color: W.dim, fontSize: 11, marginTop: 2, lineHeight: 1.4 }}>
                  {s.description}
                </div>
              </button>
            );
          })}
        </div>

        <div
          style={{
            background: W.surface,
            border: `1px solid ${W.border}`,
            borderRadius: 10,
            overflow: "auto",
            padding: "16px 20px",
          }}
        >
          {active ? (
            <pre
              style={{
                margin: 0,
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
                fontFamily: wMono,
                fontSize: 12.5,
                lineHeight: 1.6,
                color: W.text2,
              }}
            >
              {active.body}
            </pre>
          ) : (
            <p style={{ color: W.dim }}>Select a skill to preview.</p>
          )}
        </div>
      </div>
    </section>
  );
}

function Header() {
  return (
    <header>
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <h2 style={{ color: W.text, fontSize: 18, margin: 0 }}>Agent Skills</h2>
        <span
          style={{
            color: W.dim,
            border: `1px solid ${W.border}`,
            padding: "1px 7px",
            borderRadius: 4,
            fontSize: 10.5,
            fontFamily: wMono,
          }}
        >
          editor-agnostic
        </span>
      </div>
      <div style={{ color: W.dim, fontSize: 12.5, marginTop: 3 }}>
        Safe `dpm localnet` workflows for AI agents. Same docs as the CLI
        `localnet skills` command — install into your agent and let it
        drive DevKit.
      </div>
    </header>
  );
}

function InstallButton({
  label,
  busy,
  onClick,
}: {
  label: string;
  busy: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      style={{
        background: busy ? W.surface2 : W.brand,
        color: busy ? W.dim : "#0B0E13",
        border: "none",
        borderRadius: 6,
        padding: "6px 12px",
        fontSize: 12,
        fontWeight: 600,
        fontFamily: wMono,
        cursor: busy ? "wait" : "pointer",
      }}
    >
      {busy ? "Installing…" : label}
    </button>
  );
}
