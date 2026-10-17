export type AgentPresetScope = "builtin" | "user" | "project";
export type WritableAgentPresetScope = Exclude<AgentPresetScope, "builtin">;

export interface AgentPreset {
  id: string;
  name: string;
  description: string;
  body: string;
  keepCoding: boolean;
  complete: boolean;
  includeRuntimeContext: boolean;
  /** null inherits the available tool surface; [] explicitly admits no tools. */
  tools: string[] | null;
  scope: AgentPresetScope;
  path: string;
}

export interface AgentPresetsView {
  presets: AgentPreset[];
  problems: string[];
  current: string;
  locked: boolean;
}

export interface AgentPresetBindings {
  ListAgentPresets(tabID: string): Promise<AgentPresetsView>;
  SaveAgentPreset(tabID: string, scope: string, id: string, content: string): Promise<AgentPreset>;
  DeleteAgentPreset(tabID: string, scope: string, id: string): Promise<void>;
  SetAgentPresetForTab(tabID: string, id: string): Promise<void>;
}

export const AGENT_PRESET_ID_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/;
export const AGENT_PRESETS_CHANGED = "deepzora:agent-presets-changed";

export function agentPresetTools(text: string): string[] {
  return [...new Set(text.split(/[,\r\n]+/u).map(tool => tool.trim()).filter(Boolean))];
}

export function renderAgentPreset(preset: Pick<AgentPreset, "name" | "description" | "body" | "keepCoding" | "complete" | "includeRuntimeContext" | "tools">): string {
  // JSON scalars are valid YAML. Quoting every value prevents names and tool
  // patterns from becoming YAML keys, aliases, booleans or frontmatter fences.
  const fields = [
    `name: ${JSON.stringify(preset.name.trim())}`,
    `description: ${JSON.stringify(preset.description.trim())}`,
    `keep-coding-instructions: ${preset.keepCoding}`,
  ];
  if (preset.complete) fields.push("complete: true");
  if (preset.includeRuntimeContext === false) fields.push("include-runtime-context: false");
  if (preset.tools !== null) fields.push(`tools: ${JSON.stringify(preset.tools)}`);
  const body = preset.body.trim();
  return `---\n${fields.join("\n")}\n---\n${body ? `${body}\n` : ""}`;
}

export function nextAgentPresetCopyID(id: string, presets: AgentPreset[]): string {
  const existing = new Set(presets.map(preset => preset.id));
  const base = `${id.slice(0, 54)}-copy`;
  let candidate = base;
  for (let suffix = 2; existing.has(candidate); suffix += 1) candidate = `${base}-${suffix}`;
  return candidate;
}

export function agentPresetError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function notifyAgentPresetsChanged(): void {
  window.dispatchEvent(new Event(AGENT_PRESETS_CHANGED));
}
