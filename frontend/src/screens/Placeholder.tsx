import { W } from "../tokens";

// Placeholder — the route stub for screens whose backend hasn't
// landed yet. Each names the ticket that gates its real
// implementation so a user clicking through the sidebar knows
// when to expect more, and a reviewer scanning the chain knows
// what's intentional vs missing.
//
// As each ticket lands, swap the route in App.tsx from
// <Placeholder/> to the real screen component.
export function Placeholder({ name, ticket }: { name: string; ticket?: string }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px dashed ${W.border}`,
        borderRadius: 8,
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
      {ticket && (
        <p style={{ margin: 0, fontSize: 12 }}>
          tracked by{" "}
          <a
            href={`https://linear.app/bitdynamics/issue/${ticket}`}
            style={{ color: W.brand }}
            target="_blank"
            rel="noreferrer"
          >
            {ticket}
          </a>
        </p>
      )}
    </div>
  );
}
