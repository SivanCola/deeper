import type { ProjectNode } from "./types";
import { sessionIdentityBaseKey } from "./sessionIdentity";
import type { SessionRef } from "./sessionRef";

/** The local navigation route is a serialized SessionRef, never a file path. */
export function localSessionRefFromRoute(route: string | undefined): SessionRef | undefined {
  const match = /^session-id:([a-zA-Z0-9][a-zA-Z0-9._-]{0,255})$/.exec(route?.trim() ?? "");
  return match ? { hostId: "local", sessionId: match[1] } : undefined;
}

/** Stable row identity; topicId is only the last resort for an empty draft. */
export function projectSessionIdentity(node: ProjectNode): string {
  if (node.session?.sessionId) return sessionIdentityBaseKey(node);
  if (node.remoteSession) {
    const remote = node.remoteSession;
    if (remote.sessionId) return sessionIdentityBaseKey({ session: { hostId: remote.hostId, sessionId: remote.sessionId } });
    return `source\u0000${remote.hostId}\u0000${remote.workspace}\u0000${remote.path || remote.name}`;
  }
  const ref = localSessionRefFromRoute(node.sessionPath);
  if (ref) return sessionIdentityBaseKey({ session: ref });
  if (node.sessionPath) return `path\u0000${node.sessionPath.trim()}`;
  if (node.tabId) return `tab\u0000local\u0000${node.tabId}`;
  return `topic\u0000${node.topicId || node.key}`;
}

/** Only owner-verified aliases may join a source to its adopted session. */
export function projectSessionKeys(node: ProjectNode): string[] {
  return [projectSessionIdentity(node), ...(node.identityAliases ?? [])];
}

export function projectSessionExcluded(node: ProjectNode, excluded: ReadonlySet<string>): boolean {
  return projectSessionKeys(node).some(key => excluded.has(key)) || Boolean(node.topicId && excluded.has(node.topicId));
}

/** A reused tab is a surface, so it cannot lend its mounted key to a session. */
export function projectSessionRowKey(node: ProjectNode): string {
  return projectSessionIdentity(node);
}

export function sameProjectSession(a: ProjectNode, b: ProjectNode): boolean {
  if (a.session && b.session) return projectSessionIdentity(a) === projectSessionIdentity(b);
  const keys = new Set(projectSessionKeys(a));
  return projectSessionKeys(b).some(key => keys.has(key));
}

/** Directory metadata wins over incomplete observations of the same identity.
 * Lifecycle versions win first, so a delayed pre-archive row cannot downgrade
 * the newer restored row. Canonical siblings never merge through a topic alias.
 */
export function reconcileProjectSessionRows(rows: ProjectNode[]): ProjectNode[] {
  const next: ProjectNode[] = [];
  const positions = new Map<string, number[]>();
  for (const row of rows) {
    const keys = projectSessionKeys(row);
    const index = keys.flatMap(key => positions.get(key) ?? []).find(index => sameProjectSession(next[index], row));
    if (index === undefined) {
      const position = next.length;
      next.push(row);
      keys.forEach(key => positions.set(key, [...(positions.get(key) ?? []), position]));
      continue;
    }
    const old = next[index];
    const generation = (row.lifecycleGeneration ?? 0) - (old.lifecycleGeneration ?? 0);
    if (generation < 0 || (generation === 0 && old.session && !row.session)) continue;
    next[index] = row;
    keys.forEach(key => { if (!positions.get(key)?.includes(index)) positions.set(key, [...(positions.get(key) ?? []), index]); });
  }
  return next.length === rows.length && next.every((row, i) => row === rows[i]) ? rows : next;
}
