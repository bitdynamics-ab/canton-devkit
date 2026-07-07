import { W } from "../tokens";

// Placeholder — the route stub for screens whose backend hasn't
// landed yet. Swap the route in App.tsx to the real screen component
// as each one becomes available.
export function Placeholder({ name }: { name: string }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px dashed ${W.border}`,
        borderRadius: 4,
        padding: 32,
        textAlign: "center",
        color: W.dim,
        maxWidth: 480,
        margin: "48px auto",
      }}
    >
      <h2 style={{ color: W.text2, marginTop: 0, fontSize: 18 }}>{name}</h2>
      <p style={{ marginTop: 0 }}>
        Not implemented yet in this build.
      </p>
    </div>
  );
}
