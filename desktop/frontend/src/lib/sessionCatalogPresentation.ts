import type { SessionCatalogStatus } from "./sessionCatalogTypes";

export type SessionCatalogNotice = "indexing" | "repair-active" | "repair-blocked" | "failed" | "rebuild";

export function sessionCatalogNotice(s: SessionCatalogStatus): SessionCatalogNotice | null {
  if (s.state === "opening" || s.state === "rebuilding") return "indexing";
  if (s.state === "degraded" || s.lastError) return s.canRebuild ? "rebuild" : "failed";
  if (s.repairActive > 0) return "repair-active";
  if (s.repairBlocked > 0) return "repair-blocked";
  if (s.repairPending > 0) return "indexing";
  return null;
}
