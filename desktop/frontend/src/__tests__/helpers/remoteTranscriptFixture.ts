import type { AppBindings } from "../../lib/bridge";
import { publishMockTranscriptEvent } from "../../lib/sessionReaderBridge";
import type { WireEvent } from "../../lib/types";
import { historySliceFromMessages } from "../mockHistorySlice";
import type { HistoryMessage, HistorySliceRequest } from "../../lib/types";

/** Feed fixture history through Follow; metadata never initiates another cut. */
export function installRemoteTranscriptFixture(commands: Record<string, unknown>): void {
  const snapshots = new Map<string, Awaited<ReturnType<AppBindings["RemoteTabSnapshot"]>>>();
  commands.readHistorySlice = async (tabId: string, request: HistorySliceRequest) => {
    const snapshot = await (commands.RemoteTabSnapshot as AppBindings["RemoteTabSnapshot"])(tabId);
    snapshots.set(tabId, snapshot);
    for (const event of snapshot.pendingEvents ?? []) publishMockTranscriptEvent({ ...(event as WireEvent), tabId });
    return historySliceFromMessages(tabId, (snapshot.history ?? []) as HistoryMessage[], request);
  };
  commands.RemoteTabMetadata = async (tabId: string) => snapshots.get(tabId) ?? {};
}
