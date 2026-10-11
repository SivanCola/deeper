import assert from "node:assert/strict";
import { setAttentionPreference, setSuccessPreference } from "../lib/sound";
import { createRuntimeNotifications } from "../lib/runtimeNotifications";
import { t } from "../lib/i18n";

const stored = new Map<string, string>();
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: {
    getItem(key: string) { return stored.get(key) ?? null; },
    setItem(key: string, value: string) { stored.set(key, value); },
    removeItem(key: string) { stored.delete(key); },
    clear() { stored.clear(); },
  },
});
let contexts = 0;
class FakeAudioContext {
  destination = {};
  constructor() { contexts++; }
  createOscillator() { return { type: "sine", frequency: { setValueAtTime() {} }, connect() {}, start() {}, stop() {} }; }
  createGain() {
    return { gain: { setValueAtTime() {}, linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {} }, connect() {} };
  }
  close() {}
}
Object.defineProperty(globalThis, "AudioContext", { configurable: true, value: FakeAudioContext });

const owner = createRuntimeNotifications(() => ({ activeTabId: "A", t, showToast() {} }));

function chimesFor(event: { err?: string; outcome?: string }, only: "success" | "attention"): number {
  setSuccessPreference(only === "success" ? "synth" : "off");
  setAttentionPreference(only === "attention" ? "synth" : "off");
  const before = contexts;
  owner.accept({ event: { kind: "turn_done", tabId: "A", ...event } });
  return contexts - before;
}

for (const outcome of ["incomplete_read"]) {
  assert.equal(chimesFor({ err: "paused", outcome }, "attention"), 1, `${outcome} waits on the user`);
}
assert.equal(chimesFor({}, "success"), 1, "a clean finish keeps the success chime");
assert.equal(chimesFor({}, "attention"), 0, "a clean finish does not ring the attention chime");
assert.equal(chimesFor({ err: "provider failed" }, "success") + chimesFor({ err: "provider failed" }, "attention"), 0,
  "an ordinary failure stays silent");
owner.dispose();
console.log("runtime notifications: paused turn outcomes ring the attention chime");
