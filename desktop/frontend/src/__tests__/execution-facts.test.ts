import type { WireCompletionSummary } from "../lib/types";
import assert from "node:assert/strict";
import { normalizeCompletionSummary, completionSummaryNeedsAttention } from "../lib/completionSummary";

const base: WireCompletionSummary = { constraint_degraded: false, verdict: "unknown", mutations: 1, checks_passed: 0, checks_failed: 0, checks_suppressed: 0, review: "", receipt: { assessmentKind: "facts", verdict: "unknown", changes: [{path: "auth.go", reviewed: false}] } };
assert.equal(normalizeCompletionSummary(base).receipt?.assessmentKind, "facts");
assert.equal(completionSummaryNeedsAttention(base), false, "unknown facts do not imply failure");
const failed = structuredClone(base);
failed.receipt!.verifications = [{ command: "go test ./...", passed: false, exitCode: 1 }];
assert.equal(completionSummaryNeedsAttention(failed), true, "actual failure remains visible");
assert.equal(normalizeCompletionSummary(failed).receipt?.verifications?.[0].exitCode, 1);
console.log("current execution facts retain actual verification failures");
