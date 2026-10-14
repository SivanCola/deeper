import { useEffect, useMemo, useState } from "react";

import { useCommittedCommand } from "../lib/useCommittedCommand";
import { desktopBridge } from "./desktopBridgeAdapter";
import type { TabMeta } from "../lib/types";

type SessionSummary = Readonly<{ turns?: number }>;

/**
 * Owns the session summary chain: the target memo keyed by SessionRef, the
 * GetSessionSummary bridge command, the single-flight fetch (an identity or
 * revision change cancels the superseded request) and the resulting
 * activeTopicTurns state. Presentation reads only the returned turns.
 */
export function useSessionSummary(input: {
  activeTab: TabMeta | undefined;
  revision: number;
}): { activeTopicTurns: number | undefined } {
  const { activeTab, revision } = input;
  const [activeTopicTurns, setActiveTopicTurns] = useState<number | undefined>(undefined);

  const hostId = activeTab?.session?.hostId;
  const sessionId = activeTab?.session?.sessionId;
  const target = useMemo(() => (hostId === "local" && sessionId ? { hostId, sessionId } : null), [hostId, sessionId]);

  const getSummary = useCommittedCommand(desktopBridge.getSessionSummary);
  const commitTurns = useCommittedCommand((turns: number | undefined) => setActiveTopicTurns(turns));

  useEffect(() => {
    const currentTarget = target;
    commitTurns(undefined);
    if (!currentTarget) {
      return;
    }
    let current = true;
    void getSummary(currentTarget).then((summary: SessionSummary) => {
      if (current) commitTurns(summary.turns);
    }).catch(() => {
      if (current) commitTurns(undefined);
    });
    return () => { current = false; };
  }, [getSummary, commitTurns, revision, target]);

  return { activeTopicTurns };
}
