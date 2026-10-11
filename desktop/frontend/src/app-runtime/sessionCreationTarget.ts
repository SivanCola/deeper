export type SessionCreationTarget = { scope: "global" | "project"; workspaceRoot: string };

type SurfaceTab = { scope?: string; workspaceRoot?: string; remote?: unknown };

/** Select the local workspace scope for a newly created session. */
export function sessionCreationTargetForTab(tab?: SurfaceTab | null): SessionCreationTarget {
  if (!tab || tab.remote) return { scope: "global", workspaceRoot: "" };
  const workspaceRoot = tab.workspaceRoot || "";
  return { scope: tab.scope === "project" && workspaceRoot ? "project" : "global", workspaceRoot };
}
