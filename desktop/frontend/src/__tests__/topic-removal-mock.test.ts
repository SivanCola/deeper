import assert from "node:assert/strict";
import { inspect, remove } from "../lib/topicRemovalMock";
import { makeMockSessionLifecycleBindings } from "../lib/sessionLifecycleBindings";
import type { AppBindings } from "../lib/bridge";
import type { ProjectNode } from "../lib/types";

const tree: ProjectNode[] = [{ key: "global", kind: "global_folder", label: "Global", children: [
  { key: "empty", topicId: "empty", session: { hostId: "local", sessionId: "empty-session" }, kind: "global_topic", label: "New conversation" },
  { key: "named", topicId: "named", session: { hostId: "local", sessionId: "named-session" }, kind: "global_topic", label: "My plan" },
] }];
const archived = new Set<string>(), calls: string[] = [];
const archive = async (refs: { hostId: string; sessionId: string }[]) => {
  for (const ref of refs) { calls.push(ref.sessionId); archived.add(ref.sessionId); }
};
let view = inspect(tree, { workspaceId: "global", topicId: "empty" });
assert.equal(view.disposition, "archive_sessions");
const emptyRequest = { operationId: "empty", target: view.target, expectedToken: view.token };
const [first, duplicate] = await Promise.all([remove(tree, emptyRequest, archive), remove(tree, emptyRequest, archive)]);
assert.ok(first.committed); assert.deepEqual(duplicate, first);
assert.deepEqual(calls, ["empty-session"], "concurrent duplicate joins one current archive");
view = inspect(tree, { workspaceId: "global", topicId: "named" });
const request = { operationId: "named", target: view.target, expectedToken: view.token };
tree[0].children![1].label = "Renamed";
assert.equal((await remove(tree, request, archive)).errorCode, "state_conflict");
request.expectedToken = inspect(tree, request.target).token;
const result = await remove(tree, request, archive);
assert.ok(result.committed);
archived.delete("named-session");
assert.deepEqual(await remove(tree, request, archive), result);
assert.ok(!archived.has("named-session"), "duplicate cannot archive a restored session");
const bindings = {
  ...makeMockSessionLifecycleBindings(() => ({ generation: 3, workspaces: [], archivedSessionIds: [...archived], pendingCreates: [] }), archived, new Set(), () => {}, tree),
  async ArchiveCanonicalSession(ref: { hostId: string; sessionId: string }) { await archive([ref]); },
} as unknown as AppBindings;
assert.equal((await bindings.InspectTopicRemoval(request.target)).disposition, "archive_sessions");
assert.deepEqual(await bindings.RemoveTopic(request), result, "lazy delegate shares current receipt identity");
assert.deepEqual(calls, ["empty-session", "named-session"]);
await bindings.RemoveTopic({ ...request, operationId: "named-again" });
assert.ok(archived.has("named-session"), "new explicit request delegates to current archive");
assert.deepEqual(calls, ["empty-session", "named-session", "named-session"]);
assert.equal(tree[0].children!.length, 2, "canonical lifecycle retains durable rows for trash and restore");
console.log("PASS current topic archive: identity, confirmation, concurrent idempotency and lazy delegate");
