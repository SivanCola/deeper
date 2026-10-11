import { isTurnNotRunning } from "./inboxError";

export type InboxCancelReceipt = {
  discardedItemIds: string[];
  warning?: string;
};

export type CancelOutcome = InboxCancelReceipt & {
  restoredText?: string;
  /** Local failure receipt; decision cards must not treat it as accepted Stop. */
  error?: string;
};

type InboxCancelBridge = {
  CancelSessionForTab(tabId: string): Promise<{ accepted: boolean; alreadyIdle: boolean; recoveryRequired: boolean }>;
  CancelTabWithInboxItemsResult(tabId: string, itemIds: string[]): Promise<InboxCancelReceipt>;
  InterruptTurnWithInboxItemsForTab(tabId: string, turnId: string, itemIds: string[]): Promise<InboxCancelReceipt>;
};

// Stop is a session-level request: a turn-id fence rejection (stale or
// replaced turn) still stops whatever is running now, and an idle backend is
// not a failure. Only the unconditional path's own error reaches the caller.
export async function requestSessionCancel(
  app: InboxCancelBridge,
  tabId: string,
  itemIds: string[],
  turnId?: string,
): Promise<InboxCancelReceipt> {
  try {
    return await requestInboxCancel(app, tabId, itemIds, turnId);
  } catch (error) {
    if (isTurnNotRunning(error)) return { discardedItemIds: [] };
    if (!turnId) throw error;
    return requestInboxCancel(app, tabId, itemIds, undefined);
  }
}

export async function requestInboxCancel(
  app: InboxCancelBridge,
  tabId: string,
  itemIds: string[],
  turnId?: string,
): Promise<InboxCancelReceipt> {
  if (itemIds.length === 0) {
    await app.CancelSessionForTab(tabId);
    return { discardedItemIds: [] };
  }
  if (turnId && itemIds.length > 0) {
    const receipt = await app.InterruptTurnWithInboxItemsForTab(tabId, turnId, itemIds);
    return {
      discardedItemIds: Array.isArray(receipt?.discardedItemIds) ? receipt.discardedItemIds.map(String) : [],
      warning: receipt?.warning?.trim() || undefined,
    };
  }
  if (itemIds.length > 0) {
    const receipt = await app.CancelTabWithInboxItemsResult(tabId, itemIds);
    return {
      discardedItemIds: Array.isArray(receipt?.discardedItemIds) ? receipt.discardedItemIds.map(String) : [],
      warning: receipt?.warning?.trim() || undefined,
    };
  }
  throw new Error("invalid cancellation request");
}
