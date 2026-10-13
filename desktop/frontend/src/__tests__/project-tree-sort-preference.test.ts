import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { loadWorkbenchSortMode, WORKBENCH_SORT_KEY } from "../lib/projectTreeTopic";

function installLocalStorage(seed: Record<string, string> = {}): Storage {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
  globalThis.localStorage = dom.window.localStorage;
  for (const [key, value] of Object.entries(seed)) localStorage.setItem(key, value);
  return localStorage;
}

{
  const storage = installLocalStorage();
  assert.equal(loadWorkbenchSortMode(), "created", "a fresh install defaults to creation time");
  assert.equal(storage.length, 0, "reading the default does not write storage");
}

for (const mode of ["created", "updated"] as const) {
  const storage = installLocalStorage({ [WORKBENCH_SORT_KEY]: mode });
  assert.equal(loadWorkbenchSortMode(), mode, "a saved sort preference remains authoritative");
  assert.equal(storage.getItem(WORKBENCH_SORT_KEY), mode, "reading preserves the saved preference");
  assert.equal(storage.length, 1, "reading does not create migration markers");
}

{
  const storage = installLocalStorage();
  assert.equal(loadWorkbenchSortMode(), "created");
  storage.setItem(WORKBENCH_SORT_KEY, "updated");
  assert.equal(loadWorkbenchSortMode(), "updated", "a manual choice remains authoritative on the next read");
}

{
  const storage = installLocalStorage({ [WORKBENCH_SORT_KEY]: "invalid" });
  assert.equal(loadWorkbenchSortMode(), "created", "invalid preferences use the default");
  assert.equal(storage.getItem(WORKBENCH_SORT_KEY), "invalid", "reading does not rewrite invalid storage");
}

{
  installLocalStorage();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    get() { throw new Error("storage unavailable"); },
  });
  assert.equal(loadWorkbenchSortMode(), "created", "unavailable storage uses the default");
  delete (globalThis as { localStorage?: Storage }).localStorage;
}

console.log("  PASS  project tree sort preference");
