import type { AppBindings } from "./bridge";
import { resolveActiveTurnId } from "./inboxSubmit";
import { confirmFollowup, followupNotSubmitted, followupSessionKey, pendingFollowups, type PendingFollowup } from "./pendingFollowup";

// Keep request construction with the lazy submission owner while callers
// capture the session target before crossing the module-loading boundary.
export function enqueueGuidanceForTarget(binding: AppBindings, target: PendingFollowup["target"], tabId: string, text: string, turnId?: string) {
  return enqueueTrackedGuidance(binding, {
    tabId, target, key: `guidance-${crypto.randomUUID()}`, display: text, submit: text, draft: text,
  }, turnId);
}

// Non-Composer callers share its unresolved-request owner so a lost receipt
// cannot cause a second POST when the user retries guidance.
export async function enqueueTrackedGuidance(binding: AppBindings, request: PendingFollowup, turnId?: string) {
  const { target } = request;
  const pendingKey = followupSessionKey(target?.sessionPath, target?.hostId, target?.workspace);
  const unresolved = pendingKey ? pendingFollowups.get(pendingKey) : undefined;
  if (unresolved) {
    const receipt = await confirmFollowup(binding, unresolved);
    pendingFollowups.clear(pendingKey, unresolved);
    if (unresolved.submit === request.submit) return receipt;
    // A different instruction still needs its own receipt; acknowledging the
    // previous request must never clear a newer draft as if it was delivered.
  }
  if (pendingKey) pendingFollowups.set(pendingKey, request);
  try {
    const receipt = await enqueueComposerGuidance(binding, request, false, turnId);
    if (receipt?.error) throw new Error(receipt.error);
    if (!receipt?.itemId) throw new Error("Follow-up receipt unconfirmed");
    if (pendingKey) pendingFollowups.clear(pendingKey, request);
    return receipt;
  } catch (error) {
    if (pendingKey && followupNotSubmitted(error)) pendingFollowups.clear(pendingKey, request);
    throw error;
  }
}

export async function enqueueComposerGuidance(binding: AppBindings, request: PendingFollowup, queueOnly: boolean, turnId?: string) {
  const { target, structured, tabId, display, submit, key } = request;
  if (structured?.attachments?.length) {
    if (!structured.attachmentTarget || !binding.EnqueueForAttachmentTarget) throw new Error("unsupported: attachments-v2");
    return binding.EnqueueForAttachmentTarget(
      structured.attachmentTarget,
      structured.attachmentSubmissionId || key,
      structured.input.trim(),
      structured.display.trim() || display,
      structured.invocations,
      structured.attachments,
    );
  }
  if (!target) throw new Error("deepzora_error:inbox_not_submitted — target submission unavailable");
  if (queueOnly || structured) {
    return binding.EnqueueInboxFollowupForTarget(target, display, structured?.input ?? submit, structured?.invocations ?? [], key);
  }
  const followup = () => binding.EnqueueInboxFollowupForTarget(target, display, submit, [], key);
  const activeTurnId = await resolveActiveTurnId(binding, tabId, turnId).catch(() => undefined);
  if (!activeTurnId) return followup();
  const result = await binding.InboxQueueForTarget(target, { kind: "enqueue_steer", text: submit, display, turnId: activeTurnId, idempotencyKey: key });
  if (result.outcome === "unavailable" && result.reason === "session_changed") {
    // Remote selection can change after the POST committed. Retain the
    // pending request for receipt recovery instead of declaring it unsent.
    throw new Error("Follow-up receipt unconfirmed — session_changed");
  }
  if (result.receipt?.error) throw new Error(result.receipt.error);
  if (!result.receipt?.itemId) throw new Error("Follow-up receipt unconfirmed");
  return result.receipt;
}
