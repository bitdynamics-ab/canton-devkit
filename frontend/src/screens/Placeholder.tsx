import { W, R } from "../tokens";

// Placeholder — the route stub for screens whose backend hasn't
// landed yet. Swap the route in App.tsx to the real screen component
// as each one becomes available.
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
      <h2 style={{ color: W.text2, marginTop: 0, marginBottom: 6, fontSize: 15 }}>
        {name}
      </h2>
      <p style={{ margin: 0, fontSize: 13 }}>
        Not implemented yet in this build. Pick another screen from the
        sidebar or press ⌘K.
      </p>
    </div>
  );
}
