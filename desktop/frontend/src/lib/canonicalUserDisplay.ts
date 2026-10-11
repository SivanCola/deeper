// Presentation only: never rewrite persisted or provider-visible messages.
// Keep this tag vocabulary aligned with agent.TransientUserBlockTags.
export const transientUserTags = [
  "response-language", "reasoning-language", "memory-update", "background-jobs",
  "active-goal", "autoresearch-runtime", "hook-context", "capability-route",
  "interrupted-turn-recovery", "execution-policy",
] as const;
const steerPrefix = "[Mid-turn steer queued by the user. Do not treat this as a new task; use it only as additional guidance for the current task after completing the current step.]";
export function canonicalSteerGuidance(content: string): string | undefined {
  let framed = content;
  for (let depth = 0; depth < 24; depth++) {
    if (framed.startsWith(steerPrefix)) {
      let text = framed.slice(steerPrefix.length);
      if (text.startsWith("\n")) text = text.slice(1);
      return text;
    }
    const trimmed = framed.replace(/^[ \t\r\n]+/, "");
    const tag = ["response-language", "reasoning-language"].find(value => trimmed.startsWith(`<${value}>`) || trimmed.startsWith(`<${value} `));
    if (!tag) return undefined;
    const end = trimmed.indexOf(`</${tag}>`);
    if (end < 0) return undefined;
    framed = trimmed.slice(end + tag.length + 3).replace(/^[ \t\r\n]+/, "");
  }
  return undefined;
}

export function canonicalUserDisplay(raw: Record<string, unknown>, fallback: string): { role: string; content: string } {
  if (raw.origin === "host") return { role: "hidden", content: "" };
  const stored = typeof raw.content === "string" ? raw.content : fallback;
  const authored = typeof raw.raw_content === "string" ? raw.raw_content : stored;
  const guidance = canonicalSteerGuidance(stored);
  return guidance === undefined ? { role: "user", content: authored } : { role: "notice", content: `↪ ${authored === stored ? guidance : authored}` };
}
