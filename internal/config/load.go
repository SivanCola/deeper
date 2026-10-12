package config

import (
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/provider"
)

// Load builds the configuration: defaults, then user config, then project
// config, then MCP servers from Claude Code's .mcp.json. Provider api_key_env values
// resolve from Deepzora's global .env, not from project .env files.
func Load() (*Config, error) {
	return LoadForRoot(".")
}

// LoadForRoot builds the configuration with project files resolved from root
// instead of the current working directory. When root is "" or ".", it behaves
// like Load(). This is the workspace-aware entry point: desktop tabs use it so
// each project's deepzora.toml + .mcp.json are resolved independently without
// changing the process cwd, while provider keys stay rooted in Deepzora home.
func LoadForRoot(root string) (*Config, error) {
	return loadForRoot(root, loadForRootOptions{loadCredentials: true})
}

// LoadForRootReadOnly is the read-only configuration entry for diagnostics and doctor.
func LoadForRootReadOnly(root string) (*Config, error) {
	return loadForRoot(root, loadForRootOptions{loadCredentials: true})
}

// LoadForRootWithoutCredentialsReadOnly is the credential-free form of
// LoadForRootReadOnly. It still merges the effective user + project config and
// carries project .env values for workspace-scoped expansion, but it neither
// pins Deepzora credentials into the process environment nor resolves provider
// API keys. Settings probes use it when they need runtime network policy before
// resolving only the edited provider's credential explicitly.
func LoadForRootWithoutCredentialsReadOnly(root string) (*Config, error) {
	return loadForRoot(root, loadForRootOptions{})
}

// LoadUserConfigReadOnly loads only the trusted user-global config. It never
// reads project deepzora.toml files and never performs on-disk migrations.
// Host-owned features that may execute a configured binary should use this
// instead of LoadForRoot so an untrusted checkout cannot choose the process.
func LoadUserConfigReadOnly() (*Config, error) {
	cfg := Default()
	if path := userConfigLoadPath(); path != "" {
		meta, err := mergeFileSnapshot(cfg, path)
		if err != nil {
			return nil, err
		}
		if meta.IsDefined("agent", "system_prompt_file") {
			cfg.systemPromptFileSource = promptFileSourceUser
		}
	}
	normalizeConfigForEdit(cfg)
	return cfg, nil
}

type loadForRootOptions struct {
	loadCredentials bool
}

func loadForRoot(root string, opts loadForRootOptions) (*Config, error) {
	root = resolveRoot(root)
	expansionEnv := loadProjectDotEnvForExpansion(root)
	if opts.loadCredentials {
		loadCredentialStoreForRoot(root)
	}
	cfg := Default()
	cfg.setExpansionEnv(expansionEnv)

	projectTOML := "deepzora.toml"
	if root != "." {
		projectTOML = filepath.Join(root, "deepzora.toml")
	}
	if primary := userConfigPath(); primary != "" {
		if _, err := resolveConfigAccessPath(primary, true); err != nil {
			return nil, err
		}
	}
	if _, err := resolveConfigAccessPath(projectTOML, false); err != nil {
		return nil, err
	}

	var tomlSources []string
	userDefaultModelExplicit := false
	if uc := userConfigLoadPath(); uc != "" {
		tomlSources = append(tomlSources, uc)
		meta, err := mergeFileSnapshot(cfg, uc)
		if err != nil {
			// Never rewrite the broken original file. Prefer the last verified
			// snapshot in memory, then built-in defaults, and keep loading so
			// the rest of the app stays usable.
			lkgCfg := Default()
			lkgCfg.setExpansionEnv(expansionEnv)
			if lkgErr := loadLastKnownGoodUserConfig(lkgCfg); lkgErr == nil {
				*cfg = *lkgCfg
				cfg.addLoadWarning(fmt.Sprintf(
					"user config %s is invalid (%v); using last-known-good snapshot in memory without modifying the original file",
					uc, err,
				))
			} else {
				cfg.addLoadWarning(fmt.Sprintf(
					"user config %s is invalid (%v); using built-in defaults in memory without modifying the original file",
					uc, err,
				))
			}
		} else {
			userDefaultModelExplicit = meta.IsDefined("default_model")
			if meta.IsDefined("agent", "system_prompt_file") {
				cfg.systemPromptFileSource = promptFileSourceUser
			}
		}
	}
	// A last-known-good recovery is still trusted user configuration even though
	// the broken source file cannot provide usable TOML metadata.
	if cfg.systemPromptFileSource == promptFileSourceUnknown && cfg.Agent.SystemPromptFile != "" {
		cfg.systemPromptFileSource = promptFileSourceUser
	}
	userDefaultModel := cfg.DefaultModel
	globalCLI := cfg.CLI
	globalSecrets := cfg.Secrets
	globalRemote, globalServe := cfg.Remote.Clone(), cfg.Serve
	held := holdUserScope(cfg)
	globalTelemetry, globalStatusline := cfg.Telemetry, cfg.Statusline

	tomlSources = append(tomlSources, projectTOML)
	projectMeta, err := mergeFileSnapshot(cfg, projectTOML)
	if err != nil {
		// Project config damage is isolated to this workspace: continue with
		// user/global config so other tabs stay available.
		cfg.addLoadWarning(fmt.Sprintf(
			"project config %s is invalid (%v); ignored for this workspace",
			projectTOML, err,
		))
		// Drop the project path from later multi-file merges so a broken TOML
		// cannot fail plugin/provider re-merges.
		tomlSources = tomlSources[:len(tomlSources)-1]
	} else if projectMeta.IsDefined("agent", "system_prompt_file") {
		cfg.systemPromptFileSource = promptFileSourceProject
	}
	// The native CLI update channel controls the one user-installed binary.
	// A repository-local deepzora.toml must never switch that global choice.
	cfg.CLI = globalCLI
	// Secret protection is a user-global security control: a cloned repo's
	// deepzora.toml must not be able to flip on the workflow-breaking env/path
	// protections.
	cfg.Secrets = globalSecrets
	// Remote SSH hosts and serve authentication are equally user-global: a repo
	// must not inject hosts, jump chains, or port forwards, nor choose serve's
	// launch token, auth mode, or trust in forwarded headers.
	cfg.Remote, cfg.Serve = globalRemote, globalServe
	// Telemetry is a user-global privacy choice and the statusline a command the
	// TUI runs unprompted: project config sets neither, even with no global value.
	cfg.Telemetry, cfg.Statusline = globalTelemetry, globalStatusline
	// TOML decoding replaces [[plugins]] wholesale, so cfg.Plugins now holds
	// only the last file's. Re-merge by name across all sources (later wins) so a
	// project deepzora.toml doesn't drop the global config's MCP servers.
	// mergeTOMLPlugins only reads files; it does not run on-disk migrations.
	plugins, err := mergeTOMLPlugins(tomlSources)
	if err != nil {
		cfg.addLoadWarning(fmt.Sprintf("plugin configuration could not be merged (%v); continuing without those entries", err))
	} else {
		cfg.Plugins = plugins
	}
	if providers, providerSources, shadowedProjectProviders, ok, err := mergeTOMLProviders(tomlSources); err != nil {
		cfg.addLoadWarning(fmt.Sprintf("provider configuration could not be merged (%v); keeping providers already loaded", err))
	} else if ok {
		cfg.Providers = providers
		cfg.providerSources = providerSources
		cfg.shadowedProjectProviders = shadowedProjectProviders
	}
	if access, ok, err := mergeTOMLProviderAccess(tomlSources); err != nil {
		cfg.addLoadWarning(fmt.Sprintf("provider access configuration could not be merged (%v)", err))
	} else if ok {
		cfg.Desktop.ProviderAccess = access
	}
	// Sandbox, permission, program and regional grants only narrow (see heldScope).
	held.narrow(cfg, root)

	// Claude Code's .mcp.json (project root) is read last and merged into
	// [[plugins]], so a server configured for Claude works here unchanged.
	// Project deepzora.toml wins on a name collision; project .mcp.json wins
	// over a same-name user-global entry (see mergeMCPJSON).
	mcpFile := mcpJSONFile
	if root != "." {
		mcpFile = filepath.Join(root, mcpJSONFile)
	}
	entries, err := loadMCPJSON(mcpFile)
	if err != nil {
		cfg.addLoadWarning(fmt.Sprintf("project .mcp.json is invalid (%v); MCP servers from that file are ignored", err))
	} else {
		cfg.mergeMCPJSON(entries)
	}

	_ = mergeInstalledPluginPackages(cfg, root)
	if err := normalizeLoadedConfig(cfg); err != nil {
		return nil, err
	}
	if userDefaultModelExplicit {
		restoreUnresolvableProjectDefaultModel(cfg, userDefaultModel)
	}
	cfg.setExpansionEnv(expansionEnv)
	if opts.loadCredentials {
		resolveProviderCredentialsForRoot(root, cfg)
	}
	return cfg, nil
}

// LoadBuiltinDefaultsForRoot returns a read-only built-in-only configuration
// without reading or migrating user/project TOML. Diagnostic and recovery tools
// use it when configuration is malformed; it does not put the process into any
// degraded product "mode". Provider credentials still resolve only from
// Deepzora's global credential store.
func LoadBuiltinDefaultsForRoot(root string) *Config {
	cfg := Default()
	cfg.Plugins = nil
	cfg.Skills = SkillsConfig{}
	cfg.Statusline.Command = ""
	cfg.LSP.Enabled = false
	cfg.setExpansionEnv(nil)
	resolveProviderCredentialsForRoot(root, cfg)
	return cfg
}

// LoadRecoveryDefaultsForRoot is retained as an alias of LoadBuiltinDefaultsForRoot
// for older recovery call sites.
func LoadRecoveryDefaultsForRoot(root string) *Config {
	return LoadBuiltinDefaultsForRoot(root)
}

func (c *Config) setExpansionEnv(env map[string]string) {
	if c == nil {
		return
	}
	c.expansionEnv = cloneStringMap(env)
	for i := range c.Plugins {
		// A project .env belongs to the project: it expands only servers the
		// project declares, whose decision covers the values it supplies.
		c.Plugins[i].expansionEnv = nil
		if RepositoryDeclared(c.Plugins[i]) {
			c.Plugins[i].expansionEnv = c.expansionEnv
		}
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

// restoreUnresolvableProjectDefaultModel falls back to the user/global
// default_model when a project deepzora.toml overrides it with a reference no
// configured provider serves (#4218). Pre-v1.11 persistence paths (e.g. the
// "always allow" writer) full-rendered ./deepzora.toml and pinned the built-in
// default_model ("deepseek-flash") into it; once the user's [[providers]]
// replaced the built-in presets, that stale name resolved to nothing and boot
// hard-failed in every launch from that folder. In-memory only — the project
// file is untouched, and a project override that does resolve still wins. The
// ignored value is kept so boot can surface a notice.
//
// Callers must only invoke this when the user config explicitly defines
// default_model: falling back to the built-in default would silently mask a
// broken ref when the project file is the user's only config, and that case
// must keep the actionable boot error (TestBuildUnknownModelErrorIsActionable).
func restoreUnresolvableProjectDefaultModel(c *Config, userDefault string) {
	if c == nil {
		return
	}
	if c.DefaultModel == userDefault {
		return
	}
	if _, ok := c.ResolveModel(c.DefaultModel); ok {
		return
	}
	if _, ok := c.ResolveModel(userDefault); !ok {
		return
	}
	c.ignoredProjectDefaultModel = c.DefaultModel
	c.DefaultModel = userDefault
}

// tomlFileDefinesKey reports whether the TOML file at path explicitly defines
// the given top-level key. Missing or unparseable files report false.
func tomlFileDefinesKey(path string, key ...string) bool {
	var f Config
	meta, err := decodeTOMLFile(path, &f)
	if err != nil {
		return false
	}
	return meta.IsDefined(key...)
}

func backfillDeepSeekOfficialPrices(c *Config) {
	if c == nil {
		return
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		if officialProviderKind(p) != "deepseek" {
			continue
		}
		backfillDeepSeekOfficialEndpointDefaults(p)
		currency := p.ProviderBillingCurrency()
		if currency == "" {
			currency = p.persistedOfficialCurrency
		}
		if currency == "" {
			currency = "USD"
		}
		defaults := DeepSeekV4PricesForCurrency(currency)
		if p.Price != nil {
			continue
		}
		if p.Prices == nil {
			p.Prices = map[string]*provider.Pricing{}
		}
		for model, price := range defaults {
			if p.HasModel(model) && p.Prices[model] == nil {
				p.Prices[model] = clonePricing(price)
			}
		}
	}
}

// backfillDeepSeekOfficialEndpointDefaults restores the two official-endpoint
// fields a config may legitimately omit. Both are safe to infer here precisely
// because the caller already matched api.deepseek.com: the wallet endpoint is
// the vendor's own, and 1M is that vendor's real window. Values the file
// declares are never overwritten.
//
// This is keyed on the endpoint rather than on list position, so it cannot leak
// onto a custom provider the way the previous positional decode overlay did
// (#7357, #7358).
func backfillDeepSeekOfficialEndpointDefaults(p *ProviderEntry) {
	if p == nil {
		return
	}
	if strings.TrimSpace(p.BalanceURL) == "" {
		p.BalanceURL = "https://api.deepseek.com/user/balance"
	}
	backfillOfficialContextWindow(p, 1_000_000)
}

func officialProviderKind(p *ProviderEntry) string {
	if p == nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(p.BaseURL))
	if err != nil {
		return ""
	}
	if strings.EqualFold(u.Hostname(), "api.deepseek.com") {
		return "deepseek"
	}
	return ""
}

func resolveRoot(root string) string {
	if root == "" || root == "." {
		return "."
	}
	return filepath.Clean(root)
}

// mergeTOMLPlugins merges [[plugins]] across TOML sources by name (later source wins).
func mergeTOMLPlugins(paths []string) ([]PluginEntry, error) {
	var merged []PluginEntry
	index := map[string]int{}
	for _, path := range paths {
		_, exists, err := statConfigPath(path)
		if err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
		if !exists {
			continue
		}
		var f Config
		if _, err := decodeTOMLFile(path, &f); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
		for _, p := range f.Plugins {
			p, _ = NormalizePluginCommandLine(p)
			if isUserConfigPath(path) {
				p.Source = MCPSourceUserConfig
			} else {
				p.Source = MCPSourceProjectConfig
			}
			if i, ok := index[p.Name]; ok {
				merged[i] = p
				continue
			}
			index[p.Name] = len(merged)
			merged = append(merged, p)
		}
	}
	return merged, nil
}

// mergeTOMLProviders merges [[providers]] across TOML sources by provider name.
// User-global providers win over same-named project providers; project providers
// only fill names the global config does not define. Provider identities remain distinct.
func mergeTOMLProviders(paths []string) ([]ProviderEntry, map[string]providerSourceScope, []ProviderEntry, bool, error) {
	var merged []ProviderEntry
	var shadowedProject []ProviderEntry
	index := map[string]int{}
	sources := map[string]providerSourceScope{}
	saw := false
	for _, path := range paths {
		_, exists, err := statConfigPath(path)
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("config %s: %w", path, err)
		}
		if !exists {
			continue
		}
		var f Config
		if _, err := decodeTOMLFile(path, &f); err != nil {
			return nil, nil, nil, false, fmt.Errorf("config %s: %w", path, err)
		}
		markPersistedDeepSeekOfficialPricing(&f)
		if len(f.Providers) == 0 {
			continue
		}
		saw = true
		source := providerSourceForPath(path)
		for _, p := range f.Providers {
			normalizeProviderEffortFields(&p)
			key := providerMergeKey(p)
			if i, ok := index[key]; ok {
				if sources[key] == providerSourceProject && source == providerSourceUser {
					shadowedProject = append(shadowedProject, merged[i])
					merged[i] = p
					sources[key] = source
				} else if sources[key] == providerSourceUser && source == providerSourceProject {
					shadowedProject = append(shadowedProject, p)
				}
				continue
			} else {
				index[key] = len(merged)
				merged = append(merged, p)
				sources[key] = source
			}
		}
	}
	return merged, sources, shadowedProject, saw, nil
}

func providerSourceForPath(path string) providerSourceScope {
	if isUserConfigPath(path) {
		return providerSourceUser
	}
	return providerSourceProject
}

func providerMergeKey(p ProviderEntry) string {
	return strings.TrimSpace(p.Name)
}

// mergeTOMLProviderAccess merges desktop.provider_access across TOML sources so
// project desktop settings do not hide account-level providers from the desktop
// model switcher.
func mergeTOMLProviderAccess(paths []string) ([]string, bool, error) {
	var merged []string
	seen := map[string]bool{}
	saw := false
	userDeclared := false
	for _, path := range paths {
		_, exists, err := statConfigPath(path)
		if err != nil {
			return nil, false, fmt.Errorf("config %s: %w", path, err)
		}
		if !exists {
			continue
		}
		var f Config
		meta, err := decodeTOMLFile(path, &f)
		if err != nil {
			return nil, false, fmt.Errorf("config %s: %w", path, err)
		}
		if !meta.IsDefined("desktop", "provider_access") {
			continue
		}
		if !saw {
			// Preserve declaration state even when the list is explicitly empty.
			// A nil slice means undeclared access; a non-nil empty slice
			// means the user intentionally removed every desktop provider.
			merged = []string{}
		}
		saw = true
		if isUserConfigPath(path) {
			userDeclared = true
		}
		for _, name := range f.Desktop.ProviderAccess {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			merged = append(merged, name)
		}
	}
	// An undeclared user list means "allow all"; a union with a project-only
	// list would silently narrow that to whatever the project happens to name.
	if saw && !userDeclared {
		return nil, false, nil
	}
	return merged, saw, nil
}

// ConfigFileDeclarations contains provider settings explicitly declared by one
// TOML file, without defaults or values inherited from another scope.
type ConfigFileDeclarations struct {
	ProviderNames                 []string
	DesktopProviderAccessDeclared bool
}

// InspectConfigFileDeclarations returns the provider-related fields explicitly
// present in one TOML file. It deliberately does not include built-in defaults
// or values inherited from another config scope.
func InspectConfigFileDeclarations(path string) (ConfigFileDeclarations, error) {
	var declarations ConfigFileDeclarations
	path = strings.TrimSpace(path)
	if path == "" {
		return declarations, nil
	}
	_, exists, err := statConfigPath(path)
	if err != nil {
		return declarations, err
	}
	if !exists {
		return declarations, nil
	}
	var f Config
	meta, err := decodeTOMLFile(path, &f)
	if err != nil {
		return declarations, fmt.Errorf("config %s: %w", path, err)
	}
	seen := make(map[string]bool, len(f.Providers))
	for _, provider := range f.Providers {
		name := strings.TrimSpace(provider.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		declarations.ProviderNames = append(declarations.ProviderNames, name)
	}
	declarations.DesktopProviderAccessDeclared = meta.IsDefined("desktop", "provider_access")
	return declarations, nil
}

// DesktopProviderAccessDeclared reports whether path explicitly declares
// desktop.provider_access. It distinguishes omission from an intentional [].
func DesktopProviderAccessDeclared(path string) (bool, error) {
	declarations, err := InspectConfigFileDeclarations(path)
	return declarations.DesktopProviderAccessDeclared, err
}

// LoadForEdit returns a config to seed the `deepzora setup` wizard when reconfiguring:
// the built-in defaults with the file at path (if present) decoded on top, so a
// reconfigure preserves the user's existing providers and agent settings instead
// of resetting to defaults. Deepzora's global .env is loaded so api_key_env
// resolution works while the wizard decides which keys are still missing.
func LoadForEdit(path string) *Config {
	return loadForEdit(path, true)
}

// LoadForEditReadOnlyStrict is the error-returning commit-time variant. It must
// not fall back to defaults when another writer leaves malformed TOML, because
// saving that fallback would overwrite the user's recoverable file.
func LoadForEditReadOnlyStrict(path string) (*Config, error) {
	return loadForEditStrict(path, true)
}

// LoadForEditWithoutCredentialsReadOnlyStrict is the credential-free strict
// edit loader. It never writes migrations and never substitutes defaults for a
// malformed file.
func LoadForEditWithoutCredentialsReadOnlyStrict(path string) (*Config, error) {
	return loadForEditStrict(path, false)
}

// ValidateFile parses one TOML config in isolation without loading credentials,
// applying migrations, or writing the file. A missing file is valid.
func ValidateFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	_, exists, err := statConfigPath(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	cfg := Default()
	if _, err := decodeTOMLFile(path, cfg); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	return nil
}

// ValidateBytes parses one in-memory TOML config without loading credentials,
// applying migrations, or writing any state.
func ValidateBytes(data []byte) error {
	cfg := Default()
	if _, err := decodeTOMLBytes(data, cfg); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

func loadForEdit(path string, loadCredentials bool) *Config {
	cfg, err := loadForEditStrict(path, loadCredentials)
	if err == nil {
		return cfg
	}
	slog.Warn("config: load for edit failed, using defaults", "path", path, "err", err)
	if loadCredentials {
		loadDotEnvForEditPath(path)
	}
	cfg = Default()
	normalizeConfigForEdit(cfg)
	cfg.editLoadErr = err
	return cfg
}

func LoadForEditWithoutCredentials(path string) *Config {
	return loadForEdit(path, false)
}

func loadForEditStrict(path string, loadCredentials bool) (*Config, error) {
	if loadCredentials {
		loadDotEnvForEditPath(path)
	}
	cfg := Default()
	meta, err := mergeFileSnapshot(cfg, path)
	if err != nil {
		return nil, err
	}
	markExplicitDefaultProjectSkillKeys(cfg, path, meta)
	normalizeConfigForEdit(cfg)
	return cfg, nil
}

// markExplicitDefaultProjectSkillKeys preserves project skill fields that are
// explicitly present in a file but equal the built-in default. Without this
// transient provenance, saving an unrelated project setting would mistake an
// intentional `false`/empty override for a stale delta and remove it.
func markExplicitDefaultProjectSkillKeys(c *Config, path string, meta toml.MetaData) {
	if c == nil || isUserConfigPath(path) {
		return
	}
	for _, key := range projectSkillKeys {
		if !meta.IsDefined("skills", key) || !projectSkillKeyIsDefault(c, key) {
			continue
		}
		if c.explicitProjectSkillKeys == nil {
			c.explicitProjectSkillKeys = make(map[string]bool)
		}
		c.explicitProjectSkillKeys[key] = true
	}
}

func normalizeConfigForEdit(cfg *Config) {
	normalizePluginCommandLines(cfg)
	freezeProviderBillingCurrencies(cfg)
	applyDeepSeekOfficialDefaultPricing(cfg)
	backfillDeepSeekOfficialPrices(cfg)
	normalizeEffortConfig(cfg)
}

func loadDotEnvForEditPath(path string) {
	path = strings.TrimSpace(path)
	if path == "" || isUserConfigPath(path) {
		loadDotEnv()
		return
	}
	loadDotEnvForRoot(filepath.Dir(path))
}

// mergeFile decodes a TOML file onto cfg if it exists. An absent file is not an error.
func mergeFile(cfg *Config, path string) error {
	_, err := mergeFileSnapshot(cfg, path)
	return err
}

// mergeFileSnapshot decodes one immutable read of a TOML file onto cfg and
// returns metadata from those exact bytes. Callers that derive source or
// precedence decisions from metadata must use this result instead of reading
// the path again: a config file may be atomically replaced between reads.
func mergeFileSnapshot(cfg *Config, path string) (toml.MetaData, error) {
	return mergeFileSnapshotWithRead(cfg, path, fileencoding.ReadFileUTF8)
}

func mergeFileSnapshotWithRead(cfg *Config, path string, readFile func(string) ([]byte, error)) (toml.MetaData, error) {
	resolved, exists, err := statConfigPath(path)
	if err != nil {
		return toml.MetaData{}, err
	}
	if !exists {
		return toml.MetaData{}, nil
	}
	data, err := readFile(resolved)
	if err != nil {
		return toml.MetaData{}, fmt.Errorf("config %s: %w", path, err)
	}
	// BurntSushi/toml decodes struct fields incrementally and can leave earlier
	// fields mutated when a later value has the wrong type. Validate the complete
	// snapshot against a disposable Config before merging those same bytes into
	// the active object. This makes user LKG fallback, project-level isolation,
	// and metadata-derived provenance transactional with respect to file changes.
	var validated Config
	if _, err := decodeTOMLBytes(data, &validated); err != nil {
		return toml.MetaData{}, fmt.Errorf("config %s: %w", path, err)
	}
	meta, err := decodeTOMLBytes(data, cfg)
	if err != nil {
		return toml.MetaData{}, fmt.Errorf("config %s: %w", path, err)
	}
	if meta.IsDefined("providers") {
		var persisted Config
		if _, err := decodeTOMLBytes(data, &persisted); err != nil {
			return toml.MetaData{}, fmt.Errorf("config %s: %w", path, err)
		}
		markPersistedDeepSeekOfficialPricing(&persisted)
		markers := map[string]string{}
		for i := range persisted.Providers {
			markers[providerMergeKey(persisted.Providers[i])] = persisted.Providers[i].persistedOfficialCurrency
		}
		for i := range cfg.Providers {
			cfg.Providers[i].persistedOfficialCurrency = markers[providerMergeKey(cfg.Providers[i])]
		}
	}
	return meta, nil
}

// tomlStringState tracks whether a line-oriented scan is currently inside a
// TOML multiline string, so retired-key strippers never treat prose inside a
// `"""..."""` or `”'...”'` value (e.g. a config example quoted in a
// system_prompt) as a section header or key assignment.
type tomlStringState int

const (
	tomlOutside tomlStringState = iota
	tomlInMultilineBasic
	tomlInMultilineLiteral
)

// advanceTOMLStringState scans one raw line and returns the multiline-string
// state after it. Outside strings it honours single-line strings and `#`
// comments so quote delimiters inside them cannot open a multiline state.
// The scan is intentionally conservative: on malformed input it prefers
// staying/returning outside, which makes callers keep lines rather than
// delete them.
func advanceTOMLStringState(state tomlStringState, line string) tomlStringState {
	i := 0
	for i < len(line) {
		switch state {
		case tomlInMultilineBasic:
			if line[i] == '\\' {
				i += 2
				continue
			}
			if strings.HasPrefix(line[i:], `"""`) {
				state = tomlOutside
				i += 3
				continue
			}
			i++
		case tomlInMultilineLiteral:
			if strings.HasPrefix(line[i:], "'''") {
				state = tomlOutside
				i += 3
				continue
			}
			i++
		default: // tomlOutside
			switch {
			case line[i] == '#':
				return state // rest of the line is a comment
			case strings.HasPrefix(line[i:], `"""`):
				state = tomlInMultilineBasic
				i += 3
			case strings.HasPrefix(line[i:], "'''"):
				state = tomlInMultilineLiteral
				i += 3
			case line[i] == '"': // single-line basic string
				i++
				for i < len(line) && line[i] != '"' {
					if line[i] == '\\' {
						i++
					}
					i++
				}
				i++ // closing quote (or line end on malformed input)
			case line[i] == '\'': // single-line literal string
				i++
				for i < len(line) && line[i] != '\'' {
					i++
				}
				i++
			default:
				i++
			}
		}
	}
	return state
}

func tomlSectionHeader(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return ""
	}
	if i := strings.Index(trimmed, "#"); i >= 0 {
		trimmed = strings.TrimSpace(trimmed[:i])
	}
	if strings.HasPrefix(trimmed, "[[") && strings.HasSuffix(trimmed, "]]") {
		return strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	}
	if strings.HasSuffix(trimmed, "]") {
		return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	return "other"
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func backfillDeepSeekAnthropicCapabilities(p *ProviderEntry) {
	if p == nil || !strings.EqualFold(strings.TrimSpace(p.Kind), "anthropic") ||
		!IsOfficialDeepSeekWebSearchEndpoint(p) {
		return
	}
	if strings.TrimSpace(p.Thinking) == "" {
		p.Thinking = "enabled"
	}
}

func officialProviderHost(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func ensureProviderModels(p *ProviderEntry, required []string, fallbackDefault string) {
	if p == nil {
		return
	}
	// If the user has explicitly curated a model list (via Settings), respect
	// that choice and do not merge additional required models.
	if len(p.Models) > 0 {
		return
	}
	models := mergeModelLists(required, p.ModelList())
	if len(models) == 0 {
		return
	}
	p.Model = models[0]
	if len(models) > 1 {
		p.Models = models
		p.Default = firstKnownModel(p.Default, models, fallbackDefault)
		return
	}
	p.Models = nil
	p.Default = ""
}

func desktopProviderAccessMap(names []string) map[string]bool {
	out := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

func mergeCuratedModelsIntoProvider(e *ProviderEntry, models []string, fallback string) {
	// If the user has explicitly curated a model list (via Settings), respect
	// that choice and do not merge additional curated models.
	if len(e.Models) > 0 {
		return
	}
	currentDefault := e.Default
	if strings.TrimSpace(currentDefault) == "" {
		currentDefault = e.Model
	}
	e.Models = mergeModelLists(models, e.ModelList())
	e.Default = firstKnownModel(currentDefault, e.Models, fallback)
}

func backfillOfficialContextWindow(e *ProviderEntry, fallback int) {
	if e != nil && e.ContextWindow <= 0 {
		e.ContextWindow = fallback
	}
}

func pricingForModelKey(prices map[string]*provider.Pricing, model string) (*provider.Pricing, bool) {
	for key, price := range prices {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(model)) {
			return price, true
		}
	}
	return nil, false
}

func mergeModelLists(primary, extra []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(primary))
	for _, list := range [][]string{primary, extra} {
		for _, model := range list {
			model = strings.TrimSpace(model)
			if model == "" || seen[model] {
				continue
			}
			seen[model] = true
			out = append(out, model)
		}
	}
	return out
}

func firstKnownModel(current string, models []string, fallback string) string {
	current = strings.TrimSpace(current)
	if slices.Contains(models, current) {
		return current
	}
	if slices.Contains(models, fallback) {
		return fallback
	}
	if len(models) > 0 {
		return models[0]
	}
	return ""
}

func CanonicalDesktopOfficialProviderName(name string) string { return strings.TrimSpace(name) }
