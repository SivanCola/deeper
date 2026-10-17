// Package agentpreset loads user-authored agent presets: a persona block for
// the system prompt plus an optional tool allowlist, selected once per session.
//
// A preset is a Markdown file `<id>.md` under the user preset directory
// (`<Deepzora home>/presets`) or the project one (`<root>/.deepzora/presets`);
// the project file wins for the same id. The built-in `default` preset is the
// unmodified agent and cannot be shadowed, so it stays the baseline to compare
// a broken local preset against.
//
// Frontmatter keys:
//
//	name: Reviewer                 # display name (defaults to the id)
//	description: Read-only review  # one line for pickers
//	keep-coding-instructions: true # false replaces the base prompt
//	complete: false                # true uses the body as the whole system prompt
//	include-runtime-context: true  # false keeps only cwd runtime context
//	tools: [read_file, grep, mcp:github]
//
// The body is appended to (or replaces) the base system prompt. Complete mode
// requires a nonempty body and overrides, but preserves, KeepCoding. Assembly
// suppresses core policies, standing documents, skills, and v4 system text;
// runtime context defaults to enabled independently. Historical conversation
// context, execution policy, and permissions are unchanged by prompt options.
//
// Omitting `tools` keeps every tool; listing it admits only those tool names, where
// `mcp:<server>` admits one whole MCP server and `mcp:*` admits all of them.
// Per-tool MCP entries are refused: an unconnected server is reached through
// use_capability without passing the tool registry, so only server-level
// admission is enforceable.
//
// Presets own only what the model sees (persona and tools). Planning,
// verification and review intensity stay with the per-turn task policy, and
// model choice stays with the session's model selection.
//
// The package resolves definitions; boot applies them at assembly and the
// session log records only the id, so edits take effect on the next build.
// It is a domain package that uses shared file and frontmatter utilities;
// it has no dependency on controllers or frontend assembly.
package agentpreset
