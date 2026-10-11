import type { ProjectNode } from "./types";
import { projectTreeReadActivityKey, topicReadRevision, topicUsesResultSequence, type ProjectTreeReadActivity } from "./projectTreeTopic";

export type ReadRecord = { metric: "result" | "time"; value: number; revision: number };
export type ReadStore = { version: 3; baselineAt: number; records: Record<string, ReadRecord> };

export function readActivityValues(store: ReadStore, nodes: readonly ProjectNode[] = []): ProjectTreeReadActivity {
  const values = Object.fromEntries(Object.entries(store.records).map(([key, record]) => [key, record.value]));
  const visit = (node: ProjectNode) => {
    const key = projectTreeReadActivityKey(node);
    // A pending remote observation cannot compare a timestamp with a result count.
    if (key && store.records[key]?.metric === "result" && !topicUsesResultSequence(node)) values[key] = Number.MAX_SAFE_INTEGER;
    node.children?.forEach(visit);
  };
  nodes.forEach(visit);
  return values;
}

export function markSessionRead(store: ReadStore, node: ProjectNode, now = Date.now()): ReadStore {
  const key = projectTreeReadActivityKey(node);
  if (!key) return store;
  const metric = topicUsesResultSequence(node) ? "result" : "time";
  if (node.remoteSession && metric === "result" && node.turnsState !== "ready") return store;
  const value = metric === "result" ? topicReadRevision(node) : Math.max(topicReadRevision(node), now);
  const previous = store.records[key];
  if (previous?.metric === "result" && metric === "time") return store;
  if (value < 0 || (value === 0 && (metric === "time" || node.turnsState !== "ready")) || (previous?.metric === metric && previous.value >= value)) return store;
  return { ...store, records: { ...store.records, [key]: { metric, value, revision: (previous?.revision ?? 0) + 1 } } };
}

/** Read positions merge monotonically across windows. Result counts take
 * precedence once a current remote observation supplies them. */
export function mergeReadStores(current: ReadStore, incoming: ReadStore): ReadStore {
  let records = current.records;
  for (const [key, next] of Object.entries(incoming.records)) {
    const old = records[key];
    if (old?.metric === "result" && next.metric === "time") continue;
    const readyResult = old?.metric === "time" && next.metric === "result";
    if (!old || readyResult || next.value > old.value || next.value === old.value && next.revision > old.revision) {
      if (records === current.records) records = { ...records };
      records[key] = next;
    }
  }
  return records === current.records ? current : { ...current, records };
}

export function seedSessionReads(store: ReadStore, nodes: readonly ProjectNode[]): ReadStore {
  let next = store;
  const visit = (node: ProjectNode) => {
    const key = projectTreeReadActivityKey(node);
    const previous = key ? next.records[key] : undefined;
    if (key && topicUsesResultSequence(node) && node.turnsState === "ready" && (!previous || node.remoteSession && previous.metric === "time")) {
      next = { ...next, records: { ...next.records, [key]: { metric: "result", value: topicReadRevision(node), revision: (previous?.revision ?? 0) + 1 } } };
    }
    node.children?.forEach(visit);
  };
  nodes.forEach(visit);
  return next;
}
