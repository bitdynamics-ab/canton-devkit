// Act-as identity (role) selection for the Tokens screen. The chosen
// role is threaded through every token API call so the whole screen
// reads/writes the ledger as that identity — the top-level counterpart
// of the per-field PartyPickers inside modals.
//
// Persisted per instance in localStorage so switching identity survives
// a reload and a different instance keeps its own last-used role. Falls
// back to "app-user" (the backend's roleFromQuery / token.DefaultRole)
// when nothing is stored or storage is unavailable.

import { useCallback, useState } from "react";
import type { Role } from "../api";

const DEFAULT_ROLE: Role = "app-user";
const VALID: readonly Role[] = ["app-user", "app-provider", "sv"];

function storageKey(instance: string): string {
  return `cdk-token-role:${instance}`;
}

function read(instance: string): Role {
  if (!instance) return DEFAULT_ROLE;
  try {
    const v = window.localStorage.getItem(storageKey(instance));
    if (v && (VALID as readonly string[]).includes(v)) return v as Role;
  } catch {
    // localStorage may be unavailable (private mode / sandbox).
  }
  return DEFAULT_ROLE;
}

// useIdentityRole returns the current act-as role for `instance` and a
// setter that persists the choice. Re-keys on `instance` so selecting a
// different instance loads that instance's stored role.
export function useIdentityRole(instance: string): [Role, (r: Role) => void] {
  const [role, setRoleState] = useState<Role>(() => read(instance));
  // Instance changed under us (topbar switch): reload its stored role.
  const [seenInstance, setSeenInstance] = useState(instance);
  if (seenInstance !== instance) {
    setSeenInstance(instance);
    setRoleState(read(instance));
  }

  const setRole = useCallback(
    (r: Role) => {
      setRoleState(r);
      try {
        if (instance) window.localStorage.setItem(storageKey(instance), r);
      } catch {
        // Non-fatal: the selection still applies for this session.
      }
    },
    [instance],
  );

  return [role, setRole];
}
