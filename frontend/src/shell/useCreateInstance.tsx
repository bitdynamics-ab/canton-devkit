import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useInstanceSelection } from "./useInstanceSelection";
import { CreateLocalNetModal } from "../screens/CreateLocalNetModal";

// The "New instance" affordance now lives in two places — the topbar
// switcher and the All-instances view — so the create modal is owned
// here, once, and opened from anywhere via useCreateInstance().open().
// On success it refreshes the list and selects the new instance, so the
// user lands on the detail-only Overview watching it come up.

interface CreateInstanceApi {
  open: () => void;
}

const Ctx = createContext<CreateInstanceApi | null>(null);

export function useCreateInstance(): CreateInstanceApi {
  const ctx = useContext(Ctx);
  if (!ctx) {
    throw new Error(
      "useCreateInstance must be used inside <CreateInstanceProvider>",
    );
  }
  return ctx;
}

export function CreateInstanceProvider({ children }: { children: ReactNode }) {
  const sel = useInstanceSelection();
  const [open, setOpen] = useState(false);
  const api = useMemo<CreateInstanceApi>(
    () => ({ open: () => setOpen(true) }),
    [],
  );
  const onClose = useCallback(() => setOpen(false), []);
  const onCreated = useCallback(
    (name: string) => {
      sel.refresh();
      sel.select(name);
    },
    [sel.refresh, sel.select],
  );
  return (
    <Ctx.Provider value={api}>
      {children}
      <CreateLocalNetModal open={open} onClose={onClose} onCreated={onCreated} />
    </Ctx.Provider>
  );
}
