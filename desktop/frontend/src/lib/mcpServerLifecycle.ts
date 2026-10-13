import type { ServerView } from "./types";

export function mcpServerLifecycleActions(s: ServerView): {
  enabled: boolean;
  showRetryInRow: boolean;
  canConnectNow: boolean;
  canReconnect: boolean;
} {
  const enabled = s.enabled;
  const state = s.runtimeState;
  return {
    enabled: state === "ready" || enabled,
    showRetryInRow: state === "issue" || s.action === "retry",
    canConnectNow: !enabled && state !== "ready",
    canReconnect: state === "ready" || state === "issue",
  };
}

export function mcpServerRetryableFromAvailableList(s: ServerView): boolean {
	if (s.status === "connected" || s.status === "disabled" || s.status === "failed") return false;
	if (!s.enabled) return false;
	return s.runtimeState === "connecting" || s.action === "retry";
}
