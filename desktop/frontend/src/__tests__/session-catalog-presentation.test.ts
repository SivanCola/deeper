import assert from "node:assert/strict";
import { sessionCatalogNotice } from "../lib/sessionCatalogPresentation";
import type { SessionCatalogStatus } from "../lib/sessionCatalogTypes";

const status = (overrides: Partial<SessionCatalogStatus> = {}): SessionCatalogStatus => ({
  state: "ready", revision: 1, indexed: 10, total: 10,
  repairPending: 0, repairActive: 0, repairBlocked: 0, canRebuild: false, ...overrides,
});
assert.equal(sessionCatalogNotice(status()), null, "healthy current metadata needs no notice");
assert.equal(sessionCatalogNotice(status({ state: "opening", indexed: 0, total: 0 })), "indexing");
assert.equal(sessionCatalogNotice(status({ state: "rebuilding", lastError: "earlier failure", canRebuild: false })), "indexing", "current rebuild owns the working state");
assert.equal(sessionCatalogNotice(status({ repairPending: 3 })), "indexing", "pending current metadata remains visible without an old backend fallback");
assert.equal(sessionCatalogNotice(status({ repairPending: 3, repairActive: 1, repairBlocked: 1 })), "repair-active");
assert.equal(sessionCatalogNotice(status({ repairBlocked: 2 })), "repair-blocked");
assert.equal(sessionCatalogNotice(status({ state: "degraded", canRebuild: true, repairActive: 1 })), "rebuild", "failure retains explicit current repair");
assert.equal(sessionCatalogNotice(status({ state: "degraded", canRebuild: false })), "failed");
assert.equal(sessionCatalogNotice(status({ lastError: "private backend detail", canRebuild: true })), "rebuild", "backend detail is not surfaced as product text");
console.log("PASS current catalog metadata status and explicit repair presentation");
