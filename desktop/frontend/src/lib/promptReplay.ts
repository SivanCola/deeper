import { app } from "./bridge";

const replayInFlight = new Set<string>();

export function replayPendingPromptsForActiveTab(
  activeTabId: string | undefined,
  replay: (tabId: string) => Promise<void> = (tabId) => app.ReplayPendingPromptsForTab(tabId),
): void {
  if (!activeTabId) return;
  if (replayInFlight.has(activeTabId)) return;
  replayInFlight.add(activeTabId);
  void replay(activeTabId).catch(() => {}).finally(() => replayInFlight.delete(activeTabId));
}
