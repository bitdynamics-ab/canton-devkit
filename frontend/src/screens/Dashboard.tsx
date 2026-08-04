import { W, wMono, tint, R, fs } from "../tokens";
import { Button } from "../components/Button";
import { IcPlus } from "../components/icons";
import { useLoadingDelay, SkeletonBar } from "../components/Skeleton";
import { useInstanceSelection } from "../shell/useInstanceSelection";
import { useCreateInstance } from "../shell/useCreateInstance";
import { CreatingPanel } from "./CreatingPanel";
import { InstanceOverview } from "./InstanceOverview";

// The Overview ("/") is detail-only: it shows exactly one instance — the
// one the topbar switcher has selected. Fleet-level management (the list,
// census, New instance) lives in the All-instances view, reached from the
// switcher. Selection is URL-backed (?instance=), so the switcher and this
// page share one source of truth.
export function Dashboard() {
  const sel = useInstanceSelection();
  const create = useCreateInstance();
  const showSkeleton = useLoadingDelay(sel.loading);

  if (sel.loading) {
    return showSkeleton ? <DetailLoading /> : null;
  }
  if (sel.error) {
    return <ErrorPanel error={sel.error} />;
  }
  if (sel.instances.length === 0) {
    return <EmptyState onCreate={create.open} />;
  }
  if (!sel.selected) {
    // Instances exist but none is running and none is pinned in the URL —
    // auto-pick returns null. Nudge the user to choose from the switcher.
    return <NoSelection />;
  }

  const selectedRow = sel.instances.find((i) => i.name === sel.selected);
  const isCreating = selectedRow?.status === "creating";
  // While creating, show ONLY the bring-up progress — the full Overview
  // (throughput, endpoints, containers, JWT…) is meaningless until the
  // instance is up. Once status flips off "creating" the Overview takes over.
  return isCreating ? (
    <CreatingPanel
      name={sel.selected}
      splice={selectedRow?.splice_version}
      ports={selectedRow?.ports}
      onRefresh={sel.refresh}
    />
  ) : (
    <InstanceOverview
      name={sel.selected}
      statusHint={selectedRow?.status}
      ports={selectedRow?.ports}
      onChanged={sel.refresh}
    />
  );
}

function DetailLoading() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
      <SkeletonBar height={40} width="40%" style={{ display: "block" }} />
      <SkeletonBar height={220} style={{ display: "block" }} />
      <SkeletonBar height={220} style={{ display: "block" }} />
    </div>
  );
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: 16,
        color: W.dim,
      }}
    >
      <p style={{ marginTop: 0, fontSize: fs.body, color: W.text }}>
        No LocalNet instances yet.
      </p>
      <Button
        variant="primary"
        size="md"
        icon={<IcPlus />}
        onClick={onCreate}
        style={{ margin: "8px 0 12px" }}
      >
        Create your first instance
      </Button>
      <p style={{ marginBottom: 0, fontSize: fs.label }}>
        Or run{" "}
        <code style={{ fontFamily: wMono, color: W.text2 }}>
          dpm localnet up --name demo
        </code>{" "}
        in your terminal.
      </p>
    </div>
  );
}

function NoSelection() {
  return (
    <div
      style={{
        background: W.surface,
        border: `1px solid ${W.border}`,
        borderRadius: R.card,
        padding: 16,
        color: W.dim,
        fontSize: fs.body,
      }}
    >
      No instance selected. Pick one from the instance switcher in the top bar.
    </div>
  );
}

function ErrorPanel({ error }: { error: string }) {
  return (
    <div
      role="alert"
      style={{
        background: `${tint(W.err, 10)}`,
        border: `1px solid ${W.err}`,
        borderRadius: R.card,
        padding: 16,
        color: W.err,
      }}
    >
      <strong>Failed to load instances</strong>
      <div style={{ color: W.text2, marginTop: 6 }}>{error}</div>
    </div>
  );
}
