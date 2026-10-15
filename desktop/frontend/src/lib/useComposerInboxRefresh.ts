import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { InboxSnapshotLike } from "./composerInboxQueue";
import { app } from "./bridge";
import {
  guidanceFromInboxSnapshot,
  hydrateEmptyGuidancePreviews,
  inboxSnapshotBelongsToScope,
} from "./composerInboxQueue";
import { onInboxChanged } from "./inboxEvents";
import type { PendingGuidance } from "./pendingGuidance";

export function useComposerInboxRefresh(
  tabId: string | undefined,
  draftKey: string,
  guidanceDraftKey: string,
  inboxSessionKey: string,
  retryNonce: number,
  running: boolean,
  applyQueue: (items: PendingGuidance[]) => void,
  bump: () => void,
  runtimeRevision?: number,
) {
  const inboxSessionKeyRef = useRef(inboxSessionKey);
  const [snapshot, setSnapshot] = useState<InboxSnapshotLike>();
  const appliedRevisionRef = useRef<{ scope: string; revision: number }>({ scope: inboxSessionKey, revision: -1 });
  const clearQueue = useCallback(() => applyQueue([]), [applyQueue]);
  useLayoutEffect(() => {
    if (inboxSessionKeyRef.current === inboxSessionKey) return;
    inboxSessionKeyRef.current = inboxSessionKey;
    appliedRevisionRef.current = { scope: inboxSessionKey, revision: -1 };
    setSnapshot(undefined);
    clearQueue();
  }, [draftKey, inboxSessionKey, clearQueue]);
  const acceptSnapshot = useCallback((snap: InboxSnapshotLike) => {
    if (inboxSessionKeyRef.current !== inboxSessionKey || !inboxSnapshotBelongsToScope(snap.sessionPath, inboxSessionKey)) return false;
    const revision = Number(snap.revision ?? -1);
    if (!Number.isFinite(revision) || (appliedRevisionRef.current.scope === inboxSessionKey && revision < appliedRevisionRef.current.revision)) return false;
    appliedRevisionRef.current = { scope: inboxSessionKey, revision };
    setSnapshot(snap);
    applyQueue(guidanceFromInboxSnapshot(snap));
    return true;
  }, [inboxSessionKey, applyQueue]);
  useEffect(() => onInboxChanged((changed) => {
    if (changed.tabId && tabId && changed.tabId !== tabId) return;
    if (!inboxSnapshotBelongsToScope(changed.sessionPath, inboxSessionKey)) return;
    if (changed.revision !== undefined
      && appliedRevisionRef.current.scope === inboxSessionKey
      && changed.revision <= appliedRevisionRef.current.revision) return;
    bump();
  }), [tabId, inboxSessionKey, bump]);
  useEffect(() => {
    if (guidanceDraftKey !== draftKey) return;
    let live = true;
    void app.InboxSnapshot(tabId || "").then((snap) => {
      if (!live || !inboxSnapshotBelongsToScope(snap?.sessionPath, inboxSessionKey)) return;
      if (!acceptSnapshot(snap)) return;
      const revision = Number(snap?.revision ?? -1);
      const durable = guidanceFromInboxSnapshot(snap);
      applyQueue(durable);
      void hydrateEmptyGuidancePreviews(durable, (id) => app.ReadInboxItem(tabId || "", id)).then((hydrated) => {
        if (live && appliedRevisionRef.current.revision === revision && hydrated.some((item) => item.text.trim())) {
          applyQueue(hydrated);
        }
      });
    }).catch(() => {
      // A transport failure must not erase the last authoritative queue.
    });
    return () => { live = false; };
  }, [draftKey, guidanceDraftKey, running, tabId, retryNonce, inboxSessionKey, applyQueue, runtimeRevision, acceptSnapshot]);
  return { snapshot, acceptSnapshot };
}
