import React from "react";
import { createRoot } from "react-dom/client";
import { ToolRecoveryPanel } from "../src/components/ToolRecoveryPanel";
import { LocaleProvider } from "../src/lib/i18n";
import type { ToolRecoveryBindings, ToolRecoverySnapshot } from "../src/lib/toolRecovery";
import "../src/styles.css";

const initial: ToolRecoverySnapshot = {
  sessionPath: "fixture", runtimeEpoch: "epoch", revision: "one",
  calls: [{ identity: { attempt_id: "attempt", canonical_tool: "write_file", resource_scope: "fixture.txt", argument_digest: "digest" }, state: "unknown", read_only: false }],
};
let view = structuredClone(initial);
const actions: string[] = [];
let release: ((view: ToolRecoverySnapshot) => void) | undefined;
const bindings: ToolRecoveryBindings = {
  async GetToolRecoveryForTab(tab) { return tab === "new-tab" ? { ...initial, calls: [] } : structuredClone(view); },

};
const root = createRoot(document.getElementById("root")!);
function render(tab = "fixture-tab") {
  root.render(<LocaleProvider><main className="transcript-shell" style={{ maxWidth: 720, margin: 32, height: 600 }}><div className="transcript"><p>Existing transcript</p></div><ToolRecoveryPanel tabId={tab} sessionKey={tab} running={false} refreshKey={1} bindings={bindings} /></main></LocaleProvider>);
}
Object.assign(window, { recoveryFixture: { actions, render, reset() { view = structuredClone(initial); render(); }, release() { release?.({ ...initial, calls: initial.calls.map(c => ({ ...c, identity: { ...c.identity, canonical_tool: "STALE_RESULT" } })) }); } } });
render();
