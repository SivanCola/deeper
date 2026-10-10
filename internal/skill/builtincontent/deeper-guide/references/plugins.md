# Plugin packages

### Manifests

- Native: `deeper-plugin.json`
- Codex: `.codex-plugin/plugin.json`
- Claude: `.claude-plugin/plugin.json` (+ limited Claude compatibility paths)

State: `<Deeper home>/plugin-packages.json`. Disabled packages do not contribute skills/hooks/MCP.

Unmapped Claude-only features may appear as compatibility warnings — Deeper does not invent support.

### Checks

`deeper plugin doctor <name>`, Settings → Plugins, Diagnostics → Plugins.

### Symptom → cause → fix

| Symptom | Cause | Fix |
| --- | --- | --- |
| Package missing | Bad root path | Reinstall / fix root (`plugin.missing_root`) |
| Invalid manifest | Parse failure | Fix JSON/manifest (`plugin.invalid_manifest`) |
| Skills missing | Disabled package | Enable package |
