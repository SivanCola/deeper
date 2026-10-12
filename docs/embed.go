// Package docs exposes the product documentation bundled with each Deepzora
// build. Runtime consumers should retrieve focused sections instead of adding
// the full corpus to the provider-visible prompt.
package docs

import "embed"

// Content contains the Markdown documentation shipped by this exact build.
// Images and generated site assets are intentionally excluded from the agent
// retrieval corpus.
//
//go:embed DESKTOP_ARCHITECTURE.md DESKTOP_ARCHITECTURE.zh-CN.md current-version-baseline.md EXTENSION_PROTOCOL.md EXTENSION_PROTOCOL.zh-CN.md PLUGIN_PACKAGES.md PLUGIN_PACKAGES.zh-CN.md GUIDE.md GUIDE.zh-CN.md CLI.md CLI.zh-CN.md ACP.md ACP.zh-CN.md BROWSER_CDP.md BROWSER_CDP.zh-CN.md CAPABILITY_DIAGNOSTICS.md CAPABILITY_DIAGNOSTICS.zh-CN.md CHECKPOINTS.md CHECKPOINTS.zh-CN.md COLLABORATION_MODES.md COLLABORATION_MODES.zh-CN.md COMPOSER_CONTROLS.md COMPOSER_CONTROLS.zh-CN.md CONFIG_PATHS.md CONFIG_PATHS.zh-CN.md DESKTOP_BROWSER.md DESKTOP_BROWSER.zh-CN.md ERROR_PRESENTATION.md ERROR_PRESENTATION.zh-CN.md EXTENSIONS.md EXTENSIONS.zh-CN.md INDEPENDENT_SESSIONS.md INDEPENDENT_SESSIONS.zh-CN.md MODEL_CAPABILITIES.md MODEL_CAPABILITIES.zh-CN.md MODEL_IDENTITY.md REMOTE_SESSIONS.md REMOTE_SESSIONS.zh-CN.md RECOVERY.md RECOVERY.zh-CN.md SESSION_EXPERIENCE.md SESSION_EXPERIENCE.zh-CN.md SESSION_MEMORY_RETRIEVAL.md SESSION_MEMORY_RETRIEVAL.zh-CN.md SUBAGENT_PROFILES.md SUBAGENT_PROFILES.zh-CN.md TASK_CATALOG.md TASK_CATALOG.zh-CN.md THEME_PACK.md THEME_PACK.zh-CN.md TOOL_APPROVAL_MODES.md TOOL_APPROVAL_MODES.zh-CN.md TOOL_RECOVERY.md TOOL_RECOVERY.zh-CN.md WEB_SEARCH.md WEB_SEARCH.zh-CN.md
var Content embed.FS
