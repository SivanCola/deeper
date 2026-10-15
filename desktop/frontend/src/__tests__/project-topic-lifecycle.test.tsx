import assert from "node:assert/strict";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { useProjectTopicCommands } from "../app-runtime/useProjectTopicCommands";
import type { ProjectTopicPorts } from "../app-runtime/projectTopicOwner";
import { mockAIRenameSession, mockSessionTitleTarget } from "../lib/mockSessionTitle";
import type { RemoteSessionView } from "../lib/remoteTypes";
import type { ProjectNode } from "../lib/types";
import { enqueueNavigationRequest, type NavigationCoalescingRefs } from "../lib/openTopicCoalescing";

function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
const dom = new JSDOM("<div id='root'></div>");
Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
const root = createRoot(document.getElementById("root")!);
let commands!: ReturnType<typeof useProjectTopicCommands>;
const effects: string[] = [];
let listing = deferred<RemoteSessionView[]>();
const localRequests = new Map<string, ReturnType<typeof deferred<void>>>();
type NavigationInput = { kind: "isolated-worktree"; workspaceRoot: string };
const navigationRefs: NavigationCoalescingRefs<NavigationInput> = {
  seqRef: { current: 0 }, runningRef: { current: false }, pendingRef: { current: null },
};
let navigationGate: ReturnType<typeof deferred<void>> | undefined;
let folderGate: ReturnType<typeof deferred<void>> | undefined;
let folderError: Error | undefined;
const reportedErrors: unknown[] = [];
const ports: ProjectTopicPorts = {
  renameLocal: async (selector, title) => {
    const id = selector.ref?.sessionId ?? selector.sessionPath ?? "";
    effects.push(`rename:${id}:${title}`);
    const gate = deferred<void>(); localRequests.set(id, gate); await gate.promise;
  },
  listRemote: async () => listing.promise,
  renameRemote: async (_host, _workspace, name, title) => { effects.push(`remote:${name}:${title}`); },
  markChanged: () => { effects.push("refresh-projects"); },
  refreshTabs: async () => [],
  syncActive: async () => { effects.push("sync-current"); },
};
const navigation = {
  openBlank: async (scope: string, path: string) => { effects.push(`blank:${scope}:${path}`); },
  enqueue: (input: NavigationInput) => enqueueNavigationRequest(navigationRefs, input, async request => {
    effects.push(`worktree:${request.workspaceRoot}`);
    if (navigationGate) await navigationGate.promise;
    if (request.seq === navigationRefs.seqRef.current && navigationGate) effects.push(`visible:${request.workspaceRoot}`);
  }),
  switchFolder: async (path?: string) => {
    effects.push(`project:${path}`);
    if (folderGate) await folderGate.promise;
    if (folderError) throw folderError;
  },
};

const ref = { hostId: "local", sessionId: "session-a" };
const topicNode: ProjectNode = { key: "topic-A", kind: "topic", label: "A", topicId: "A", session: ref };
const projectNode: ProjectNode = { key: "project", kind: "project", label: "Project folder", root: "/fixture", children: [topicNode] };
assert.equal(mockSessionTitleTarget([projectNode], { topicId: "A" }), topicNode,
  "an unsaved titlebar session resolves by topic ID instead of its parent project");
assert.equal(mockSessionTitleTarget([projectNode], { ref }), topicNode,
  "canonical selectors resolve the same session row");
assert.equal(mockSessionTitleTarget([projectNode], {}), undefined,
  "missing session identity never resolves to the project row");
assert.equal(mockSessionTitleTarget([projectNode], { ref: { ...ref, sessionId: "other" } }), undefined,
  "an unknown canonical session never resolves to the project row");

const sibling: ProjectNode = { ...topicNode, key: "sibling", label: "Sibling", preview: "Sibling preview",
  session: { ...ref, sessionId: "sibling" } };
const titleTarget = mockSessionTitleTarget([topicNode, sibling], { ref: sibling.session });
assert.equal(mockAIRenameSession(titleTarget), "Sibling preview");
assert.equal(topicNode.label, "A", "AI rename keeps the sibling with the same topic unchanged");
assert.equal(sibling.label, "Sibling preview", "AI rename mutates the resolved session directly");

function Probe({ tab, remote = false }: { tab: string; remote?: boolean }) {
  commands = useProjectTopicCommands({ visible: { tabId: tab, sessionKey: tab },
    topic: { id: tab, title: tab, target: remote
      ? { kind: "remote", hostId: "fixture", workspace: "fixture", sessionPath: `${tab}.jsonl` }
      : { kind: "local", topicId: tab, selector: { ref: { hostId: "local", sessionId: tab } } } },
    ports, navigation, reportError: error => { reportedErrors.push(error); },
  });
  return null;
}
async function paint(tab: string, remote = false) { await act(async () => root.render(<Probe tab={tab} remote={remote} />)); }
try {
  await paint("A");
  const first = commands;
  await paint("B");
  assert.equal(commands.onCreateTopic, first.onCreateTopic);
  assert.equal(commands.onCreateIsolatedWorktree, first.onCreateIsolatedWorktree);
  assert.equal(commands.onAddProject, first.onAddProject);
  await commands.onCreateTopic("global", "/fixture/global-workspace");
  await commands.onCreateIsolatedWorktree("worktree");
  await act(async () => commands.onAddProject("project"));
  assert.deepEqual(effects, ["blank:global:/fixture/global-workspace", "worktree:worktree", "project:project"],
    "global creation retains the actual directory for workspace preferences until navigation serialization");

  effects.length = 0;
  folderGate = deferred<void>();
  let picker!: Promise<void>;
  await act(async () => { picker = commands.onAddProject(); });
  assert.equal(commands.addingProject, true, "the native picker publishes pending feedback");
  await act(async () => commands.onAddProject());
  assert.deepEqual(effects, ["project:undefined"], "repeated clicks open only one native picker");
  folderGate.resolve();
  await act(async () => picker);
  assert.equal(commands.addingProject, false, "canceling the picker releases pending feedback");
  folderGate = undefined;
  folderError = new Error("folder picker unavailable");
  await act(async () => commands.onAddProject());
  assert.deepEqual(reportedErrors, [folderError], "picker failures surface through the existing error feedback");
  assert.equal(commands.addingProject, false, "failure releases the button for retry");
  folderError = undefined;

  effects.length = 0;
  navigationGate = deferred<void>();
  const firstNavigation = commands.onCreateIsolatedWorktree("A");
  const supersededNavigation = commands.onCreateIsolatedWorktree("B");
  const lastNavigation = commands.onCreateIsolatedWorktree("C");
  await supersededNavigation;
  assert.deepEqual(effects, ["worktree:A"], "replaced requests do not execute while the first backend call is pending");
  navigationGate.resolve();
  await Promise.all([firstNavigation, lastNavigation]);
  assert.deepEqual(effects, ["worktree:A", "worktree:C", "visible:C"], "real coalescing queue accepts the last worktree command and rejects old UI continuation");
  assert.equal(navigationRefs.pendingRef.current, null);
  assert.equal(navigationRefs.runningRef.current, false);
  navigationGate = undefined;

  effects.length = 0;
  await act(async () => commands.startActiveTopicRename());
  await act(async () => commands.setTopicTitleDraft("changed"));
  await act(async () => commands.cancelActiveTopicRename());
  await commands.commitActiveTopicRename();
  assert.deepEqual(effects, [], "escape followed by blur never submits a rename");
  await act(async () => commands.startActiveTopicRename());
  await paint("A");
  assert.equal(commands.topicbarEditing, false, "switching resources releases the former draft");

  await paint("A", true);
  await act(async () => commands.startActiveTopicRename());
  await act(async () => commands.setTopicTitleDraft("source title"));
  let pending!: Promise<void>;
  await act(async () => { pending = commands.commitActiveTopicRename(); });
  await paint("B", true); await paint("A", true);
  listing.resolve([{ name: "A", path: "A.jsonl", title: "A", turns: 1 }, { name: "B", path: "B.jsonl", title: "B", turns: 1, current: true }]);
  await act(async () => { await pending; });
  assert.deepEqual(effects, ["remote:A:source title", "refresh-projects"], "remote current may change but rename retains A; ABA cannot resync the visible tab");

  effects.length = 0;
  await paint("A");
  const renameA = commands.renameTopic("A", "one");
  await paint("B");
  const renameB = commands.renameTopic("B", "two");
  localRequests.get("A")!.resolve(); await renameA;
  localRequests.get("B")!.resolve(); await renameB;
  assert.equal(effects.filter(effect => effect === "refresh-projects").length, 2, "unrelated topics retain independent operation lanes");

  effects.length = 0;
  listing = deferred<RemoteSessionView[]>();
  await paint("A", true);
  await act(async () => commands.startActiveTopicRename());
  await act(async () => { pending = commands.commitActiveTopicRename(); });
  await act(async () => root.unmount());
  listing.resolve([{ name: "A", path: "A.jsonl", title: "A", turns: 1 }]); await pending;
  first.onAddProject("stale");
  assert.deepEqual(effects, [], "disposed feature cannot rename, refresh, or navigate");
  console.log("project/topic commands: stable entry, targeted rename, independent lanes, ABA, cancel and disposal passed");
} finally { dom.window.close(); }
