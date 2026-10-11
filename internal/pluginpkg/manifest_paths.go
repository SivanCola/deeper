package pluginpkg

func ManifestPath(kind string) string {
	switch kind {
	case "deepzora":
		return NativeManifest
	case "codex":
		return CodexManifest
	case "claude":
		return ClaudeManifest
	default:
		return NativeManifest
	}
}

func ManifestPaths() []string {
	return []string{NativeManifest, CodexManifest, ClaudeManifest}
}
