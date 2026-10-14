import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { app, onEvent, type AppBindings } from "../lib/bridge";
import { DECISION_SURFACE_MOCK_TRIGGERS } from "../lib/decisionSurfaceMock";
import type { WireEvent } from "../lib/types";

const dom = new JSDOM("<!doctype html><html><body></body></html>");
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;

const events: WireEvent[] = [];
const unsubscribe = onEvent((event) => events.push(event));
const cases: Array<{
  kind: "ask" | "approval" | "plan";
  trigger: "ask" | "tool_approval" | "plan_approval";
  answer: Parameters<NonNullable<AppBindings["ResolvePromptForSession"]>>[1];
  text: string;
}> = [
  { kind: "ask", trigger: "ask", answer: { questions: [{ questionId: "q1", selected: ["staging", "custom answer"] }] }, text: "q1: staging, custom answer" },
  { kind: "approval", trigger: "tool_approval", answer: { allow: true }, text: "allowed once" },
  { kind: "approval", trigger: "tool_approval", answer: { allow: true, session: true }, text: "grant active this session" },
  { kind: "approval", trigger: "tool_approval", answer: { allow: true, persist: true }, text: "grant saved" },
  { kind: "approval", trigger: "tool_approval", answer: { allow: false }, text: "denied" },
  ...["start_execution", "revise_plan", "exit_plan"].map((action) => ({
    kind: "plan" as const, trigger: "plan_approval" as const, answer: { action }, text: action,
  })),
];

try {
  for (const [index, scene] of cases.entries()) {
    const tabId = `mock-prompt-tab-${index}`;
    const start = events.length;
    await app.SubmitToTabWithID(tabId, DECISION_SURFACE_MOCK_TRIGGERS[scene.trigger], `mock-prompt-submit-${index}`);
    const prompt = events.slice(start).find((event) => event.kind === (scene.kind === "ask" ? "ask_request" : "approval_request"));
    assert.ok(prompt, `${scene.kind} publishes a prompt`);
    assert.equal(prompt.tabId, tabId, "prompt belongs to its origin tab");
    const promptId = prompt.ask?.id ?? prompt.approval?.id;
    assert.ok(promptId);
    assert.ok(prompt.turnId);
    const target = {
      tabId, hostId: "local", sessionId: `mock-session-${index}`, sessionGeneration: 0,
      promptId, turnId: prompt.turnId, runtimeEpoch: prompt.runtimeEpoch ?? "", kind: scene.kind,
    };
    // The mock session endpoint delegates to the same tab resolver as production helpers.
    await app.ResolvePromptForSession!(target, scene.answer);
    await app.ResolvePromptForTab!(tabId, promptId, target.turnId, target.runtimeEpoch, scene.kind, scene.answer);
    const completed = events.slice(start);
    const messages = completed.filter((event) => event.kind === "message");
    assert.equal(messages.length, 1, "duplicate answers do not repeat the mock result");
    assert.ok(messages[0].text?.includes(scene.text), "answer selection is preserved");
    assert.equal(messages[0].tabId, tabId, "answer message stays on the origin tab");
    assert.equal(completed.filter((event) => event.kind === "turn_done").length, 1, "each prompt finishes once");
    console.log(`PASS ${scene.kind}: ${scene.text}`);
  }
} finally {
  unsubscribe();
  dom.window.close();
}
