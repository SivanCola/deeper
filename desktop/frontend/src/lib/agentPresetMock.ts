import { AGENT_PRESET_ID_PATTERN, type AgentPreset, type AgentPresetBindings } from "./agentPresets";
import type { TabMeta } from "./types";

const builtin: AgentPreset = {
  id: "default", name: "Default", description: "The standard Deepzora agent with every tool",
  body: "", keepCoding: true, complete: false, includeRuntimeContext: true, tools: null, scope: "builtin", path: "",
};

function parseContent(id: string, content: string): Omit<AgentPreset, "scope" | "path"> {
  const match = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)([\s\S]*)$/u.exec(content);
  if (!match) throw new Error("Invalid agent preset frontmatter");
  const fields: Record<string, unknown> = {};
  for (const line of match[1].split(/\r?\n/u)) {
    const split = line.indexOf(":");
    const key = line.slice(0, split);
    if (split < 1 || !["name", "description", "keep-coding-instructions", "complete", "include-runtime-context", "tools"].includes(key)) throw new Error(`Unknown field: ${key}`);
    if (key in fields) throw new Error(`Duplicate field: ${key}`);
    fields[key] = JSON.parse(line.slice(split + 1).trim());
  }
  for (const key of ["name", "description"]) {
    if (key in fields && typeof fields[key] !== "string") throw new Error(`${key} must be a string`);
  }
  for (const key of ["keep-coding-instructions", "complete", "include-runtime-context"]) {
    if (key in fields && typeof fields[key] !== "boolean") throw new Error(`${key} must be a boolean`);
  }
  if (fields.tools != null && (!Array.isArray(fields.tools) || fields.tools.some(tool => typeof tool !== "string"))) throw new Error("tools must be a list of strings");
  return {
    id, name: String(fields.name ?? id).trim() || id, description: String(fields.description ?? "").trim(),
    body: match[2].trim(), keepCoding: fields["keep-coding-instructions"] !== false,
    complete: fields.complete === true, includeRuntimeContext: fields["include-runtime-context"] !== false,
    tools: fields.tools == null ? null : [...fields.tools as string[]],
  };
}

export function makeMockAgentPresetBindings(context: {
  tabs: () => TabMeta[];
  update: (tab: TabMeta) => void;
  isBlank: (topicId: string) => boolean;
  changed: (tabId: string) => Promise<void>;
}): AgentPresetBindings {
  const definitions = new Map<string, AgentPreset>();
  const copy = (preset: AgentPreset): AgentPreset => ({ ...preset, tools: preset.tools === null ? null : [...preset.tools] });
  const tabFor = (id: string) => context.tabs().find(tab => id ? tab.id === id : tab.active);
  const rootFor = (id: string) => tabFor(id)?.workspaceRoot ?? "";
  const keyFor = (tabId: string, scope: string, id: string) => `${scope}:${scope === "project" ? rootFor(tabId) : ""}:${id}`;
  const locked = (tab: TabMeta) => tab.agentPresetLocked ?? (tab.running || !context.isBlank(tab.topicId));
  const list = (tabId: string) => {
    const root = rootFor(tabId);
    const byID = new Map<string, AgentPreset>();
    for (const preset of definitions.values()) if (preset.scope === "user") byID.set(preset.id, preset);
    for (const [key, preset] of definitions) if (key.startsWith(`project:${root}:`)) byID.set(preset.id, preset);
    return [copy(builtin), ...[...byID.values()].sort((a, b) => a.id.localeCompare(b.id)).map(copy)];
  };
  return {
    async ListAgentPresets(tabId) {
      const tab = tabFor(tabId);
      return { presets: list(tabId), problems: [], current: tab?.agentPreset || "default", locked: tab ? locked(tab) : false };
    },
    async SaveAgentPreset(tabId, scope, id, content) {
      if (scope !== "project" && scope !== "user") throw new Error("Choose a writable preset scope");
      if (id === "default" || !AGENT_PRESET_ID_PATTERN.test(id)) throw new Error("Invalid or reserved preset ID");
      if (scope === "project" && !rootFor(tabId)) throw new Error("Project preset directory is unavailable");
      const parsed = parseContent(id, content);
      if ((parsed.complete || !parsed.keepCoding) && !parsed.body) throw new Error("A replacement persona body is required");
      const preset: AgentPreset = { ...parsed, scope, path: `${scope === "user" ? "/mock/user" : rootFor(tabId)}/agents/${id}.md` };
      definitions.set(keyFor(tabId, scope, id), preset);
      return copy(preset);
    },
    async DeleteAgentPreset(tabId, scope, id) {
      if (scope !== "project" && scope !== "user" || id === "default") throw new Error("The built-in default preset is read-only");
      definitions.delete(keyFor(tabId, scope, id));
    },
    async SetAgentPresetForTab(tabId, id) {
      const tab = tabFor(tabId);
      if (!tab) throw new Error("Session is unavailable");
      if (locked(tab)) throw new Error("Agent preset is locked after the first turn. Start a new session to change it.");
      if (!list(tabId).some(preset => preset.id === id)) throw new Error("Agent preset not found");
      context.update({ ...tab, agentPreset: id });
      await context.changed(tab.id);
    },
  };
}
