import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import type { FollowRequest, TranscriptFollowResponse } from "../generated/desktopContract.generated";
import { useController } from "../lib/useController";
import { installDesktopHostStub } from "./desktopHostStub";
import { meta, tabMeta } from "./helpers/sessionSwitchFixtures";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
const desktopStub = installDesktopHostStub({});
let controller: ReturnType<typeof useController> | undefined;
function Probe() { controller = useController(); return null; }
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}
function flushPromises(): Promise<void> { return new Promise(resolve => setTimeout(resolve, 0)); }
async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 100; attempt++) {
    await act(async () => { await flushPromises(); });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

for (const scenario of ["pending", "ready-before-failure", "ready-after-failure", "send-after-failure", "superseded-failure", "session-rebound", "runtime-rebuilt", "same-runtime-old-first", "same-runtime-current-first"] as const) {
  const id = `follow-admission-${scenario}`;
  const session = { hostId: "local", sessionId: id };
  let epoch = `${id}-initial`;
  let generation = 0;
  const initialEpoch = epoch;
  const requests: Array<{ epoch: string; gate: ReturnType<typeof deferred<TranscriptFollowResponse>> }> = [];
  const submissions: Array<{ tabId: string; epoch: string }> = [];
  let lists = 0;
  const response = (runtimeEpoch: string): TranscriptFollowResponse => ({
    protocolVersion: 2, subscription: runtimeEpoch, changes: [], resetRequired: false,
    snapshot: {
      protocolVersion: 2, snapshotId: runtimeEpoch, identity: { sessionId: id, runtimeEpoch, headId: "", rewriteEpoch: 0 },
      projectionRevision: 1, coveredThroughSeq: 0, durableSeq: 0, records: [], activeRecords: [], activeAttempts: [],
      runtime: { status: "completed", pendingEvents: [], samplingCount: 0, toolCount: 0 },
      before: 0, hasOlder: false, totalRecords: 0, totalTurns: 0, stale: false,
    },
  });
  desktopStub.replaceCommands({
    RegisterNavigationIntent: async () => {},
    ListTabs: async () => { lists++; return [tabMeta({ id, session, sessionGeneration: generation, runtime: { phase: "ready", epoch } })]; },
    MetaForTab: async () => meta({ session, sessionGeneration: generation, runtime: { phase: "ready", epoch } }),
    ContextUsageForTab: async () => ({ used: 0, window: 100, sessionTokens: 0 }),
    EffortForTab: async () => ({ supported: true, current: "auto", default: "auto", levels: ["auto"] }),
    BalanceForTab: async () => ({ available: false, display: "" }), JobsForTab: async () => [], CheckpointsForTab: async () => [],
    ForkTargetsForTab: async () => ({ targets: [], verifiable: false }), HistoryCheckpointTurnsForTab: async () => [],
    ReplayPendingPromptsForTab: async () => {},
    TranscriptFollowForTab: (_tab: string, request: FollowRequest) => {
      if (request.close) return Promise.resolve({ protocolVersion: 2, subscription: request.subscription, changes: [], resetRequired: false });
      if (request.subscription) return new Promise<TranscriptFollowResponse>(() => {});
      const gate = deferred<TranscriptFollowResponse>();
      requests.push({ epoch, gate });
      return gate.promise;
    },
    StartTurnForTab: async (tabId: string) => { submissions.push({ tabId, epoch }); return { turnId: `${id}-turn` }; },
  });
  const admissionRoot = createRoot(document.createElement("div"));
  await act(async () => { admissionRoot.render(<Probe />); await flushPromises(); });
  await waitFor(`${scenario} initial baseline`, () => requests.length === 1);
  await act(async () => { requests[0].gate.resolve(response(initialEpoch)); await flushPromises(); });
  await waitFor(`${scenario} connected`, () => controller?.state.transcriptConnection === "connected" && !controller.state.hydrating);
  epoch = `${id}-replacement`;
  await act(async () => { desktopStub.emit("runtime:rebuilt", id, epoch); await flushPromises(); });
  await waitFor(`${scenario} replacement baseline`, () => requests.length === 2);
  assert.equal(controller?.state.transcriptConnection, "syncing", `${scenario}: old connected state is withdrawn immediately`);

  const ready = async () => {
    const before = lists;
    await act(async () => { desktopStub.emit("agent:ready", id); await flushPromises(); });
    await waitFor(`${scenario} ready hydration`, () => lists > before && Boolean(controller?.state.hydrating));
  };
  if (scenario === "pending" || scenario === "ready-before-failure") {
    await ready();
    assert.equal(requests.length, 2, `${scenario}: ready joins the baseline already in progress`);
  }
  if (scenario === "ready-before-failure" || scenario === "ready-after-failure" || scenario === "send-after-failure") {
    await act(async () => { requests[1].gate.reject(new Error("runtime changed while following transcript")); await flushPromises(); });
    await waitFor(`${scenario} failed baseline`, () => controller?.state.transcriptConnection === "disconnected" && !controller.state.hydrating);
    if (scenario !== "send-after-failure") await ready();
  }
  if (scenario === "superseded-failure") {
    epoch = `${id}-newest`;
    await act(async () => { desktopStub.emit("runtime:rebuilt", id, epoch); await flushPromises(); });
    await waitFor("newest replacement baseline", () => requests.length === 3);
  }

  const beforeSend = requests.length;
  let sendOutcome = "pending";
  let sending!: Promise<void>;
  await act(async () => {
    sending = controller!.sendToTab(id, "first turn").then(() => { sendOutcome = "accepted"; }, () => { sendOutcome = "rejected"; });
    await flushPromises();
  });
  if (scenario === "send-after-failure") await waitFor("send replaces failed baseline", () => requests.length === 3);
  else assert.equal(requests.length, beforeSend, `${scenario}: send shares the current startup`);
  assert.equal(sendOutcome, "pending", `${scenario}: admission waits for the baseline`);
  assert.equal(submissions.length, 0, `${scenario}: no turn is submitted before connection`);

  if (scenario === "session-rebound") {
    generation++;
    await act(async () => { await controller!.refreshMeta(); await flushPromises(); });
  }
  if (scenario === "runtime-rebuilt" || scenario === "same-runtime-old-first" || scenario === "same-runtime-current-first") {
    if (scenario === "runtime-rebuilt") epoch = `${id}-newest`;
    await act(async () => { desktopStub.emit("runtime:rebuilt", id, epoch); await flushPromises(); });
    await waitFor("runtime rebuilt while send waits", () => requests.length === 3);
    if (scenario === "same-runtime-old-first") {
      await act(async () => { requests[1].gate.resolve(response(requests[1].epoch)); await flushPromises(); });
      assert.equal(sendOutcome, "pending", "same-runtime observer replacement joins the current baseline");
      assert.equal(submissions.length, 0, "old baseline completion cannot admit a send while its replacement is syncing");
      await act(async () => { requests[2].gate.resolve(response(epoch)); await sending; await flushPromises(); });
    } else {
      await act(async () => { requests[2].gate.resolve(response(epoch)); await flushPromises(); });
      assert.equal(controller?.state.transcriptConnection, "connected");
      await sending;
      assert.equal(sendOutcome, scenario === "runtime-rebuilt" ? "rejected" : "accepted",
        "a retired bridge read cannot hold the send after its current owner settles");
      await act(async () => { requests[1].gate.resolve(response(requests[1].epoch)); await flushPromises(); });
    }
    if (scenario === "runtime-rebuilt") {
      assert.equal(sendOutcome, "rejected", "a waiting send cannot cross a runtime rebuild under the same session");
      assert.equal(submissions.length, 0);
    } else {
      assert.equal(sendOutcome, "accepted", "same-runtime observer replacement preserves a valid pending send");
      assert.deepEqual(submissions, [{ tabId: id, epoch }]);
      assert.equal(requests.length, 3, "joining the current observer does not start another baseline");
    }
    assert.equal(controller?.state.transcriptConnection, "connected", "late old startup leaves the replacement connected");
  } else {
    const current = requests.at(-1)!;
    await act(async () => { current.gate.resolve(response(current.epoch)); await sending; await flushPromises(); });
    if (scenario === "session-rebound") {
      assert.equal(sendOutcome, "rejected", "a waiting send cannot cross a session binding change");
      assert.equal(submissions.length, 0);
    } else {
      assert.equal(sendOutcome, "accepted", `${scenario}: connected baseline admits the waiting send`);
      assert.deepEqual(submissions, [{ tabId: id, epoch }], `${scenario}: exactly one submission reaches the current host runtime`);
      assert.equal(controller?.state.transcriptConnection, "connected");
      assert.equal(controller?.state.runtimeStatusEpoch, epoch);
    }
  }
  if (scenario === "superseded-failure") {
    await act(async () => { requests[1].gate.reject(new Error("old baseline failed late")); await flushPromises(); });
    assert.equal(controller?.state.transcriptConnection, "connected", "old rejection cannot disconnect the replacement owner");
    await act(async () => { await controller!.sendToTab(id, "second turn"); await flushPromises(); });
    assert.equal(requests.length, 3, "old cleanup cannot remove the connected replacement owner");
    assert.equal(submissions.length, 2);
  }
  await act(async () => { admissionRoot.unmount(); });
  console.log(`PASS ${scenario}: follower startup owns submission admission`);
}
desktopStub.uninstall();
dom.window.close();
