import type { Translator } from "./i18n";
import type { WireCompletionSummary } from "./types";
import { normalizeCompletionReceipt, turnChangeText, turnCheckText, turnResultHasContent } from "./turnResult";

function count(value: number): number {
  return Number.isFinite(value) ? Math.max(0, Math.trunc(value)) : 0;
}

export function normalizeCompletionSummary(summary: WireCompletionSummary): WireCompletionSummary {
  return {
    receipt: normalizeCompletionReceipt(summary.receipt),
    turnId: summary.turnId,
    checkpointTurn: Number.isSafeInteger(summary.checkpointTurn) && summary.checkpointTurn! >= 0 ? summary.checkpointTurn : undefined,
    checking: summary.checking === true,
    liveChecks: Array.isArray(summary.liveChecks) ? summary.liveChecks : undefined,
    verdict: String(summary.verdict ?? "").trim().toLowerCase(),
    mutations: count(summary.mutations),
    changed_files: summary.changed_files === undefined ? undefined : count(summary.changed_files),
    checks_passed: count(summary.checks_passed),
    checks_failed: count(summary.checks_failed),
    checks_suppressed: count(summary.checks_suppressed),
    review: String(summary.review ?? "").trim().toLowerCase(),
    gap_kinds: [...new Set((Array.isArray(summary.gap_kinds) ? summary.gap_kinds : []).map((gap) => String(gap).trim().toLowerCase()).filter(Boolean))].slice(0, 8),
    constraint_degraded: Boolean(summary.constraint_degraded),
  };
}

export function completionSummaryNeedsAttention(
  summary?: WireCompletionSummary,
): boolean {
  if (!summary) return false;
	if (summary.receipt?.assessmentKind === "facts") {
		return Boolean(summary.receipt.verifications?.some(check => !check.passed || check.interrupted));
	}
  const verdict = summary.verdict.trim().toLowerCase();
  const kinds = new Set((summary.gap_kinds ?? []).map((gap) => gap.trim().toLowerCase()).filter(Boolean));
  if (verdict === "blocked" || summary.checks_failed > 0 || summary.checks_suppressed > 0) return true;
  if (kinds.has("unbacked_claim") || kinds.has("failed_verification")) return true;
  return false;
}


export function completionSummaryPresentation(
  summary: WireCompletionSummary,
  t: Translator,
): { level: "info" | "warn"; title: string; body: string } | undefined {
  if (!turnResultHasContent(summary)) return undefined;
  return {
    level: completionSummaryNeedsAttention(summary) ? "warn" : "info",
    title: t("notice.completionChangesTitle"),
    body: `${turnChangeText(summary, t)}\n${turnCheckText(summary, t)}`,
  };
}
