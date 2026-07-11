import { W, R } from "../tokens";

// 404 page for the `path="*"` catch-all — the only place this renders.
export function Placeholder({ name }: { name: string }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: "14px 16px",
        color: W.dim,
        maxWidth: 480,
      }}
    >
      <h2 style={{ color: W.text2, marginTop: 0, marginBottom: 6, fontSize: 16 }}>
        {name}
      </h2>
      <p style={{ margin: 0, fontSize: 16 }}>
        That route doesn’t exist. Pick another screen from the sidebar or
        press ⌘K.
      </p>
    </div>
  );
}
