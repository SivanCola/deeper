import { app } from "./bridge";
import type { HistoryWindowPage, HistoryOutlinePage, HistoryOutlineRequest, SearchHistoryPage, Ref, SessionHistoryContentChunk } from "../generated/desktopContract.generated";
import type { HistoryWindowRequestView, HistoryWindowPageView } from "./types";
import { supportsHistoryRead, acquireHistoryRead, currentHistoryReadBinding, releaseHistoryRead } from "./historyReadScope";
export { releaseHistoryRead } from "./historyReadScope";

const stale = (): HistoryWindowPageView => ({ entries: [], status: "stale_cursor", olderCursor: "", newerCursor: "",
  hasOlder: false, hasNewer: false, totalTurns: 0, startTurn: 0, endTurn: 0, revision: 0, revisionKnown: false, digest: "" });

export async function readBoundHistoryWindow(tabId: string, req: HistoryWindowRequestView): Promise<HistoryWindowPage | HistoryWindowPageView | undefined> {
  if (!supportsHistoryRead()) return undefined;
  const binding = acquireHistoryRead(tabId);
  const handle = await binding.promise;
  if (currentHistoryReadBinding(tabId) !== binding) return stale();
  const page = await app.ReadSessionHistoryWindow(handle.id, req);
  if (currentHistoryReadBinding(tabId) !== binding) return stale();
  if (page.status === "stale_cursor") releaseHistoryRead(tabId);
  return page;
}

export async function readBoundHistoryOutline(tabId: string, req: HistoryOutlineRequest): Promise<HistoryOutlinePage | undefined> {
  if (!supportsHistoryRead() || typeof app.ReadSessionHistoryOutline !== "function") return undefined;
  const binding = acquireHistoryRead(tabId);
  const handle = await binding.promise;
  if (currentHistoryReadBinding(tabId) !== binding) return { entries: [], status: "stale_cursor", totalTurns: 0, nextTurn: 0, done: true, snapshotSequence: 0, coverageSequence: 0, generation: "" };
  const page = await app.ReadSessionHistoryOutline(handle.id, req);
  if (currentHistoryReadBinding(tabId) !== binding) return { ...page, entries: [], status: "stale_cursor" };
  if (page.status === "stale_cursor") releaseHistoryRead(tabId);
  return page;
}

export async function searchBoundHistory(tabId: string, text: string, cursor = "", limit = 50): Promise<SearchHistoryPage | undefined> {
  if (!supportsHistoryRead() || typeof app.SearchSessionHistoryRead !== "function") return undefined;
  const empty = (status: string): SearchHistoryPage => ({ hits: [], status, hasMore: false, snapshotSequence: 0, coverageSequence: 0 });
  const binding = acquireHistoryRead(tabId);
  const handle = await binding.promise;
  if (currentHistoryReadBinding(tabId) !== binding) return empty("stale_cursor");
  const page = await app.SearchSessionHistoryRead(handle.id, text, cursor, limit);
  if (currentHistoryReadBinding(tabId) !== binding) return empty("stale_cursor");
  if (page.status === "stale_cursor") releaseHistoryRead(tabId);
  return page;
}

export async function readBoundHistoryContent(tabId: string, ref: Ref, offset: number): Promise<SessionHistoryContentChunk | undefined> {
  if (!supportsHistoryRead()) return undefined;
  const binding = acquireHistoryRead(tabId);
  const handle = await binding.promise;
  if (currentHistoryReadBinding(tabId) !== binding) throw new Error("History read expired");
  const chunk = await app.ReadSessionHistoryContent(handle.id, ref, offset);
  if (currentHistoryReadBinding(tabId) !== binding) throw new Error("History read expired");
  return chunk;
}
