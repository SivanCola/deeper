import { useEffect } from "react";
import { app } from "../lib/bridge";
import { useOverlayStore } from "../store/overlays";

export async function probeProviderSetupState(): Promise<boolean> {
  const needs = await app.NeedsOnboarding();
  useOverlayStore.getState().setProviderSetupNeeded(needs);
  return needs;
}

/** Setup status is advisory. The welcome action owns navigation to settings. */
export function StartupGateLifecycle() {
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const needs = await app.NeedsOnboarding();
        if (cancelled) return;
        useOverlayStore.getState().setProviderSetupNeeded(needs);
      } catch {
        // Setup status is advisory; bridge failures must not block startup.
      }
    })();
    return () => { cancelled = true; };
  }, []);
  return null;
}
