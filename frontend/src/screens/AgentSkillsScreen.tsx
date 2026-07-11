import { useEffect, useState } from "react";
import {
  ApiError,
  fetchSkills,
  installSkills,
  type Skill,
} from "../api";
import { W, wMono, tint, FAST, fs } from "../tokens";
import { Button } from "../components/Button";
import { IcAlert, IcCheck, IcX } from "../components/icons";

// Browses the bundled agent skill docs and installs them into
// ~/.claude/skills or ~/.codex/skills.
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
    | {
        kind: "done";
        target: "claude" | "codex";
        dir: string;
        count: number;
        skipped: string[];
      }
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

  async function doInstall(target: "claude" | "codex", force = false) {
    setInstall({ kind: "busy", target });
    try {
      const resp = await installSkills(target, force);
      setInstall({
        kind: "done",
        target,
        dir: resp.dir,
        count: resp.count,
        skipped: resp.skipped ?? [],
      });
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
        <p style={{ color: W.dim, fontSize: fs.body }}>Loading skills…</p>
      </section>
    );
  }
  if (state.kind === "err") {
    return (
      <section style={{ padding: 24 }}>
        <Header />
        <p role="alert" style={{ color: W.err, fontSize: fs.body }}>{state.error}</p>
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

      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          background: W.surface,
          border: `1px solid ${W.border}`,
          borderRadius: 4,
          padding: "10px 14px",
          flexWrap: "wrap",
        }}
      >
        <span style={{ color: W.text2, fontSize: fs.small }}>
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
          <span
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 6,
              color: W.ok,
              fontSize: fs.small,
              fontFamily: wMono,
            }}
          >
            <IcCheck size={12} /> {install.count} installed → {install.dir}
          </span>
        )}
        {install.kind === "done" && install.skipped.length > 0 && (
          <span
            role="status"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 8,
              color: W.warn,
              fontSize: fs.small,
              fontFamily: wMono,
            }}
          >
            <IcAlert size={12} /> {install.skipped.length} preserved (locally
            modified): {install.skipped.join(", ")}
            <Button
              variant="danger"
              onClick={() => doInstall(install.target, true)}
            >
              Overwrite
            </Button>
          </span>
        )}
        {install.kind === "err" && (
          <span
            role="alert"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 6,
              color: W.err,
              fontSize: fs.small,
            }}
          >
            <IcX size={12} /> {install.message}
          </span>
        )}
      </div>

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
            borderRadius: 4,
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
                  background: isActive ? tint(W.brand, 12) : "transparent",
                  border: "none",
                  cursor: "pointer",
                  color: isActive ? W.text : W.text2,
                  transition: `background-color ${FAST}`,
                }}
              >
                <div style={{ fontWeight: 600, fontSize: fs.body }}>{s.name}</div>
                <div style={{ color: W.dim, fontSize: fs.small, marginTop: 2, lineHeight: 1.4 }}>
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
            borderRadius: 4,
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
                fontSize: fs.small,
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
        <h2 style={{ color: W.text, fontSize: fs.h3, margin: 0 }}>Agent Skills</h2>
        <span
          style={{
            color: W.dim,
            border: `1px solid ${W.border}`,
            padding: "1px 7px",
            borderRadius: 2,
            fontSize: fs.caption,
            fontFamily: wMono,
          }}
        >
          editor-agnostic
        </span>
      </div>
      <div style={{ color: W.dim, fontSize: fs.small, marginTop: 3 }}>
        Safe `dpm localnet` workflows for AI agents. Same docs as the CLI
        `localnet skills` command. Install into your agent and let it drive
        DevKit.
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
    <Button
      variant="secondary"
      onClick={onClick}
      disabled={busy}
      style={{ fontFamily: wMono }}
    >
      {busy ? "Installing…" : label}
    </Button>
  );
}
