import { useCallback, useEffect, useMemo, useState } from "react";
import type { ProjectNode } from "../lib/types";
import { markSessionRead, mergeReadStores, readActivityValues, seedSessionReads, type ReadStore } from "../lib/sessionReadActivity";

const STORE_KEY = "projectTree:readActivity:v3";

function loadStore(): ReadStore {
  const empty: ReadStore = { version: 3, baselineAt: Date.now(), records: {} };
  try {
    const stored = localStorage.getItem(STORE_KEY);
    if (stored) {
      const data = JSON.parse(stored) as ReadStore;
      if (data.version === 3 && data.records) return data;
    }
  } catch { /* Storage may be unavailable. */ }
  return empty;
}

function persist(store: ReadStore): ReadStore {
  try {
    const merged = mergeReadStores(loadStore(), store);
    localStorage.setItem(STORE_KEY, JSON.stringify(merged));
    return merged;
  } catch { return store; }
}

export function useProjectTreeReadActivity(nodes: readonly ProjectNode[]) {
  const [store, setStore] = useState<ReadStore>(loadStore);
  const readActivity = useMemo(() => readActivityValues(store, nodes), [store, nodes]);
  const markNodeRead = useCallback((node: ProjectNode) => {
    setStore(current => {
      const next = markSessionRead(current, node);
      return next === current ? current : persist(next);
    });
  }, []);

  useEffect(() => {
    setStore(current => {
      const next = seedSessionReads(current, nodes);
      return next === current ? current : persist(next);
    });
  }, [nodes]);

  useEffect(() => {
    const changed = (event: StorageEvent) => {
      if (event.key !== STORE_KEY || !event.newValue) return;
      try {
        const incoming = JSON.parse(event.newValue) as ReadStore;
        if (incoming.version === 3 && incoming.records) setStore(current => mergeReadStores(current, incoming));
      } catch { /* Ignore invalid external storage. */ }
    };
    window.addEventListener("storage", changed);
    return () => window.removeEventListener("storage", changed);
  }, []);
  return { readActivity, readBaselineAt: store.baselineAt, markNodeRead };
}
