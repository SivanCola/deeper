import assert from "node:assert/strict";
import { agentPresetTools, nextAgentPresetCopyID, renderAgentPreset, type AgentPreset } from "../lib/agentPresets";
import { makeMockAgentPresetBindings } from "../lib/agentPresetMock";
import type { TabMeta } from "../lib/types";

const special = { name: 'Reviewer: "safe"\n---', description: "true # comment\n&alias", body: "# Review\n\n---\nCheck everything.", keepCoding: false, complete: true, includeRuntimeContext: false, tools: ["read_*", "mcp:research"] };
const content = renderAgentPreset(special);
assert.match(content, /name: "Reviewer: \\"safe\\"\\n---"/u);
assert.match(content, /keep-coding-instructions: false/u);
assert.match(content, /^complete: true$/mu);
assert.match(content, /^include-runtime-context: false$/mu);
assert.doesNotMatch(renderAgentPreset({ ...special, complete: false, includeRuntimeContext: true }), /^(?:complete|include-runtime-context):/mu, "default prompt options are omitted");
assert.equal(content.split("\n").filter(line => line === "---").length, 3, "persona fences stay in the body");
assert.doesNotMatch(renderAgentPreset({ ...special, tools: null }), /^tools:/mu, "null inherits the available surface");
assert.match(renderAgentPreset({ ...special, tools: [] }), /^tools: \[\]$/mu, "an explicit empty surface remains empty");
assert.deepEqual(agentPresetTools("read_file, grep\nread_file\r\nmcp:research, \n"), ["read_file", "grep", "mcp:research"]);
assert.equal(nextAgentPresetCopyID("review", [{ id: "review-copy" }, { id: "review-copy-2" }] as AgentPreset[]), "review-copy-3");

let tabs = [{ id: "a", active: true, workspaceRoot: "/project-a", topicId: "blank", agentPreset: "default" },
  { id: "b", workspaceRoot: "/project-b", topicId: "blank" }] as TabMeta[];
const changed: string[] = [];
const mock = makeMockAgentPresetBindings({ tabs: () => tabs, update: tab => { tabs = tabs.map(item => item.id === tab.id ? tab : item); }, isBlank: id => id === "blank", changed: async id => { changed.push(id); } });
const saved = await mock.SaveAgentPreset("a", "user", "review", content);
assert.deepEqual({ name: saved.name, description: saved.description, body: saved.body, keepCoding: saved.keepCoding, complete: saved.complete, includeRuntimeContext: saved.includeRuntimeContext, tools: saved.tools }, special, "canonical YAML preserves prompt options, special scalars and persona body");
const legacy = await mock.SaveAgentPreset("a", "user", "legacy", '---\nname: "Legacy"\n---\n');
assert.equal(legacy.complete, false, "older preset files default to an assembled system prompt");
assert.equal(legacy.includeRuntimeContext, true, "older preset files include runtime context");
assert.equal(legacy.tools, null, "omitted tools inherit the available surface");
const defaultPreset = (await mock.ListAgentPresets("a")).presets.find(p => p.id === "default")!;
assert.equal(defaultPreset.complete, false);
assert.equal(defaultPreset.includeRuntimeContext, true);
for (const complete of [false, true]) {
  for (const includeRuntimeContext of [false, true]) {
    const preset = { ...special, complete, includeRuntimeContext, keepCoding: true, tools: null };
    const roundTrip = await mock.SaveAgentPreset("a", "user", "prompt-options", renderAgentPreset(preset));
    assert.deepEqual([roundTrip.complete, roundTrip.includeRuntimeContext, roundTrip.keepCoding, roundTrip.tools], [complete, includeRuntimeContext, true, null], "prompt options vary independently and preserve keepCoding and inherited tools");
  }
}
for (const field of ["complete", "include-runtime-context", "keep-coding-instructions"]) {
  for (const value of ['"false"', "0", "null", "[]"]) {
    await assert.rejects(mock.SaveAgentPreset("a", "user", "invalid", `---\n${field}: ${value}\n---\nBody\n`), /must be a boolean/u);
  }
}
await assert.rejects(mock.SaveAgentPreset("a", "user", "invalid", '---\ncomplete: true\ncomplete: false\n---\nBody\n'), /Duplicate field/u);
await assert.rejects(mock.SaveAgentPreset("a", "user", "invalid", '---\nunknown: true\n---\nBody\n'), /Unknown field/u);
await assert.rejects(mock.SaveAgentPreset("a", "user", "invalid", '---\ntools: [1]\n---\nBody\n'), /list of strings/u);
await assert.rejects(mock.SaveAgentPreset("a", "user", "empty-complete", renderAgentPreset({ ...special, keepCoding: true, body: "  \n " })), /body is required/u);
await mock.SaveAgentPreset("a", "project", "review", renderAgentPreset({ ...special, name: "Project review", tools: [] }));
assert.equal((await mock.ListAgentPresets("a")).presets.find(p => p.id === "review")?.scope, "project", "project overrides user definition");
assert.equal((await mock.ListAgentPresets("b")).presets.find(p => p.id === "review")?.scope, "user", "project definitions stay in their workspace");
assert.deepEqual((await mock.ListAgentPresets("a")).presets.find(p => p.id === "review")?.tools, []);
await mock.SetAgentPresetForTab("a", "review");
assert.equal((await mock.ListAgentPresets("a")).current, "review");
assert.deepEqual(changed, ["a"]);
const detached = await mock.ListAgentPresets("a");
detached.presets.find(p => p.id === "review")!.tools!.push("bash");
assert.deepEqual((await mock.ListAgentPresets("a")).presets.find(p => p.id === "review")?.tools, [], "views do not mutate saved definitions");
await mock.DeleteAgentPreset("a", "project", "review");
assert.equal((await mock.ListAgentPresets("a")).presets.find(p => p.id === "review")?.scope, "user", "deleting override reveals user preset");
await assert.rejects(mock.SaveAgentPreset("a", "builtin", "default", content));
await assert.rejects(mock.DeleteAgentPreset("a", "builtin", "default"));
await assert.rejects(mock.SaveAgentPreset("a", "user", "../escape", content));
await assert.rejects(mock.SaveAgentPreset("a", "user", "empty", renderAgentPreset({ ...special, body: "" })));
tabs = tabs.map(tab => tab.id === "a" ? { ...tab, agentPresetLocked: true } : tab);
await assert.rejects(mock.SetAgentPresetForTab("a", "default"), /locked/u);
assert.equal((await mock.ListAgentPresets("a")).current, "review", "a locked selection is retained");
console.log("Agent presets: prompt option defaults and strict validation, canonical escaping, null/empty tools, scoped CRUD, copy IDs and locked selection passed.");
