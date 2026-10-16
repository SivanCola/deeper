import type { ProjectNode } from "./types";
import type { SessionRef } from "./sessionRef";
import type { TopicRemovalTarget, TopicRemovalRequest, TopicRemovalInspection, TopicRemovalResult } from "../generated/desktopContract.generated";

type Receipt = { request: string; result: Promise<TopicRemovalResult> };
const states = new WeakMap<ProjectNode[], Map<string, Receipt>>();
function receipts(tree: ProjectNode[]) {
  let value = states.get(tree);
  if (!value) { value = new Map(); states.set(tree, value); }
  return value;
}
function owner(tree: ProjectNode[], id: string) {
  const parents = tree.filter(parent => parent.children?.some(node => node.topicId === id));
  if (parents.length !== 1) throw new Error("Topic ownership conflict");
  const parent = parents[0], nodes = parent.children!.filter(node => node.topicId === id);
  if (nodes.some(node => !node.session?.sessionId)) throw new Error("Current session identity is required");
  const workspaceId = parent.kind === "global_folder" ? "global" : `project-${(parent.root || parent.key).replace(/[^a-zA-Z0-9._-]/g, "-")}`;
  return { nodes, workspaceId };
}
export function inspect(tree: ProjectNode[], target: TopicRemovalTarget): TopicRemovalInspection {
  const { nodes, workspaceId } = owner(tree, target.topicId);
  if (target.workspaceId && target.workspaceId !== workspaceId) throw new Error("Topic ownership conflict");
  const busy = nodes.some(node => node.running);
  return { target: { ...target, workspaceId }, disposition: "archive_sessions", allowed: !busy, reason: busy ? "busy" : undefined, token: JSON.stringify([workspaceId, nodes]) };
}
export function remove(tree: ProjectNode[], request: TopicRemovalRequest, archive: (refs: SessionRef[]) => Promise<void>): Promise<TopicRemovalResult> {
  const saved = receipts(tree), encoded = JSON.stringify(request), previous = saved.get(request.operationId);
  const conflict: TopicRemovalResult = { committed: false, disposition: "", retryable: false, errorCode: "state_conflict" };
  if (previous) return previous.request === encoded ? previous.result : Promise.resolve(conflict);
  const view = inspect(tree, request.target);
  if (!view.allowed || view.token !== request.expectedToken) return Promise.resolve(conflict);
  const refs = owner(tree, request.target.topicId).nodes.map(node => node.session!);
  // Publish the promise before dispatch so simultaneous duplicates join one
  // canonical lifecycle operation. Failed dispatch remains retryable.
  const result = Promise.resolve().then(async (): Promise<TopicRemovalResult> => {
    await archive(refs);
    return { committed: true, disposition: "archive_sessions", retryable: false };
  }).catch(error => { saved.delete(request.operationId); throw error; });
  saved.set(request.operationId, { request: encoded, result });
  return result;
}
