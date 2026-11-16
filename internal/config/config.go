package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Decsr/Decsr-Code/internal/safefile"
)

const Filename = ".decsr.yaml" // legacy project config (root-level)

// GlobalDir is the directory for user-wide settings.
const GlobalDir = ".decsr"

// GlobalFilename is the config file inside the global directory.
const GlobalFilename = "config.yaml"

// ProjectFilename is the project config file inside the workspace directory.
const ProjectFilename = "project.yaml"

type Config struct {
	Project        ProjectConfig          `yaml:"project,omitempty"`
	Agents         map[string]AgentConfig `yaml:"agents,omitempty"`
	PromptLanguage string                 `yaml:"prompt_language,omitempty"`
	ModelMemory    ModelMemoryConfig      `yaml:"model_memory,omitempty"`
	Notifications  NotifyConfig           `yaml:"notifications,omitempty"`
	Workspace      WorkspaceConfig        `yaml:"workspace,omitempty"`
	UI             UIConfig               `yaml:"ui,omitempty"`

	// Pricing is the USD price per million tokens, keyed by a model id or a
	// glob (path.Match) such as "deepseek-*". There are no built-in prices.
	Pricing map[string]ModelPrice `yaml:"pricing,omitempty"`

	// PluginAgents are the roles loaded from plugins, keyed "<plugin>:<agent>".
	// They are filled in at run time and never written to a config file.
	PluginAgents map[string]PluginAgent `yaml:"-"`
}

// PluginAgent is a workspace role defined by a plugin agent file.
type PluginAgent struct {
	Description string // one line
	Prompt      string // the role prompt (the agent file body)
	ReadOnly    bool
}

// ModelPrice is the price of one model in USD per million tokens.
type ModelPrice struct {
	InputPerMTok  float64 `yaml:"input_per_mtok"`
	OutputPerMTok float64 `yaml:"output_per_mtok"`
}

// WorkspaceConfig controls the node workspace.
type WorkspaceConfig struct {
	MaxParallel int `yaml:"max_parallel,omitempty"` // 0 = default (3)
	// Isolation is "worktree" (each node works in its own git worktree),
	// "shared" (all nodes work in the project checkout) or "" (auto: worktree
	// inside a git repo with a commit, shared otherwise).
	Isolation string `yaml:"isolation,omitempty"`

	// Pull request loop (/pr).
	PRBase        string `yaml:"pr_base,omitempty"`         // "" = the branch checked out in the project
	PRRemote      string `yaml:"pr_remote,omitempty"`       // "" = origin
	PRPollSeconds int    `yaml:"pr_poll_seconds,omitempty"` // 0 = 60; at least 15
	MaxCIFixes    int    `yaml:"max_ci_fixes,omitempty"`    // 0 = 3
	PRAutoFix     bool   `yaml:"pr_auto_fix,omitempty"`     // run the fix nodes of /pr watch without asking

	// BudgetUSD stops new nodes from starting once the known cost of a
	// session reaches it; 0 = no budget. BudgetWarnPct is the share of the
	// budget that raises a warning; 0 = 80.
	BudgetUSD     float64 `yaml:"budget_usd,omitempty"`
	BudgetWarnPct int     `yaml:"budget_warn_pct,omitempty"`

	// Autonomy is how much the Workspace does on its own: 1 manual (also 0 or
	// empty), 2 assisted, 3 unattended (needs a budget and worktree isolation).
	// MaxAutoFixes is the number of fix nodes level 2 and 3 add after a failed
	// Build & test per session; 0 = 2.
	Autonomy     int `yaml:"autonomy,omitempty"`
	MaxAutoFixes int `yaml:"max_auto_fixes,omitempty"`

	// Coordinator turns on the lead's review after each batch of nodes: it
	// proposes adding, changing or cancelling nodes. Off by default; the
	// session's /coordinator overrides it.
	Coordinator bool `yaml:"coordinator,omitempty"`
}

// UIConfig holds presentation settings.
type UIConfig struct {
	// Icons picks the glyph set for runner identity: "unicode" (default),
	// "ascii" or "nerd". "" = unicode.
	Icons string `yaml:"icons,omitempty"`
	// Tmux makes plain `decsr` behave like `decsr --tmux`.
	Tmux bool `yaml:"tmux,omitempty"`
}

// NotifyConfig controls external notifications (e.g. cmux alerts on approval
// gates, completion, and errors). Notifications are enabled by default and only
// fire when the target tool is actually running, so the opt-out is expressed as
// a "disable" flag to fit the layered config's "true overrides" merge rule.
type NotifyConfig struct {
	DisableCMux    bool   `yaml:"disable_cmux,omitempty"`    // true = never send cmux notifications
	DisableBell    bool   `yaml:"disable_bell,omitempty"`    // true = never ring the terminal bell
	DisableDesktop bool   `yaml:"disable_desktop,omitempty"` // true = never send desktop notifications
	Webhook        string `yaml:"webhook,omitempty"`         // URL that receives a POST per notification; "" = off
	WebhookFormat  string `yaml:"webhook_format,omitempty"`  // "ntfy" (default) | "slack" | "discord"
}

// ModelMemoryConfig bounds how much memory local models may use. The two modes
// are mutually exclusive (a toggle):
//   - Mode "ram": the user sets MaxRAMGB and DECSR derives a
//     per-agent maximum context size from the agent's model (see
//     runner.ContextTokensForRAM).
//   - Mode "context": the user sets MaxContextTokens directly and it is applied
//     verbatim to every agent.
//
// Mode "" (empty) disables the feature, leaving per-agent context limits as
// configured elsewhere.
type ModelMemoryConfig struct {
	Mode             string  `yaml:"mode,omitempty"`               // "ram" | "context" | ""
	MaxRAMGB         float64 `yaml:"max_ram_gb,omitempty"`         // used when Mode == "ram"
	MaxContextTokens int     `yaml:"max_context_tokens,omitempty"` // used when Mode == "context"
}

// SupportedProgrammingLanguages lists selectable project programming languages.
var SupportedProgrammingLanguages = []string{
	"python",
	"java",
	"csharp",
	"cpp",
	"c",
	"go",
	"javascript",
	"typescript",
	"rust",
	"ruby",
	"elixir",
	"swift",
	"kotlin",
	"scala",
}

type ProjectConfig struct {
	Name           string        `yaml:"name,omitempty"`
	Language       string        `yaml:"language,omitempty"`
	ModulePath     string        `yaml:"module_path,omitempty"`
	TestCmd        string        `yaml:"test_cmd,omitempty"`
	LintCmd        string        `yaml:"lint_cmd,omitempty"`
	MaxFixAttempts int           `yaml:"max_fix_attempts,omitempty"`
	Scope          ScopeConfig   `yaml:"scope,omitempty"`
	Context        ContextConfig `yaml:"context,omitempty"`
	Profile        string        `yaml:"profile,omitempty"` // name of the last applied role profile
}

type ScopeConfig struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

type ContextConfig struct {
	AlwaysInclude   []string            `yaml:"always_include,omitempty"`
	ExcludePatterns []string            `yaml:"exclude_patterns,omitempty"`
	SemanticIndex   SemanticIndexConfig `yaml:"semantic_index,omitempty"`
	Memory          MemoryConfig        `yaml:"memory,omitempty"`
	Pack            ContextPackConfig   `yaml:"pack,omitempty"`

	// ScopedContextShadow enables measurement-only logging of symbol-scoped
	// source context during review: the scoped context is rendered and its
	// token estimate logged (agent="<role>-shadow") alongside the whole-file
	// context that is actually sent. It does NOT change what agents receive.
	// Requires the semantic index to be enabled. Default false.
	ScopedContextShadow bool `yaml:"scoped_context_shadow,omitempty"`

	// ScopedContextCoderFix enables ADDITIVE symbol-scoped related context in
	// the coder fix loop: the changed/error files are still rendered whole, and
	// semantically related declarations are appended scoped (cheaply) to improve
	// fix recall. Non-destructive — it never removes the code being fixed.
	// Requires the semantic index to be enabled. Default false.
	ScopedContextCoderFix bool `yaml:"scoped_context_coder_fix,omitempty"`
}

// ContextPackConfig controls the PM-compacted repository context pack that is
// generated and approved before planning. The pack is enabled by default;
// Disable follows the layered config's "true overrides" convention.
type ContextPackConfig struct {
	Disable         bool `yaml:"disable,omitempty"`
	MaxOutputTokens int  `yaml:"max_output_tokens,omitempty"` // cap for the generated portion
}

// SemanticIndexConfig controls the optional embeddings-based file index.
// Disabled by default; when enabled it requires a reachable embedder backend.
type SemanticIndexConfig struct {
	Enabled  bool   `yaml:"enabled,omitempty"`
	Embedder string `yaml:"embedder,omitempty"` // currently only "ollama"
	Model    string `yaml:"model,omitempty"`    // e.g. "nomic-embed-text"
	BaseURL  string `yaml:"base_url,omitempty"` // optional override for embedder endpoint
	TopK     int    `yaml:"top_k,omitempty"`    // default 20
}

// MemoryConfig controls OpenClaw-style persistent project memory.
// Memory is stored as Markdown files under .decsr/memory and indexed
// by SQLite FTS5/BM25. When UseEmbeddings is true an embedder backend
// computes dense vectors for hybrid retrieval.
type MemoryConfig struct {
	Enabled           bool    `yaml:"enabled,omitempty"`             // default true
	TopK              int     `yaml:"top_k,omitempty"`               // default 8
	ChunkTokens       int     `yaml:"chunk_tokens,omitempty"`        // default 400
	OverlapTokens     int     `yaml:"overlap_tokens,omitempty"`      // default 80
	HybridAlpha       float64 `yaml:"hybrid_alpha,omitempty"`        // default 0.5; 1.0 = pure BM25
	MaxRecallChars    int     `yaml:"max_recall_chars,omitempty"`    // default 6000
	MaxPinnedChars    int     `yaml:"max_pinned_chars,omitempty"`    // default 4000
	UseEmbeddings     bool    `yaml:"use_embeddings,omitempty"`      // default false
	Embedder          string  `yaml:"embedder,omitempty"`            // "openai" | "ollama" | "cybertron"
	EmbedderModel     string  `yaml:"embedder_model,omitempty"`      // default per backend
	EmbedderBaseURL   string  `yaml:"embedder_base_url,omitempty"`   // for openai/ollama
	EmbedderAPIKey    string  `yaml:"embedder_api_key,omitempty"`    // env var indirection acceptable
	EmbedderModelsDir string  `yaml:"embedder_models_dir,omitempty"` // cybertron model cache; default ~/.decsr/cybertron-models
	AutoPromote       bool    `yaml:"auto_promote,omitempty"`        // default true; promote decisions to MEMORY.md
}

type AgentConfig struct {
	Runner           string   `yaml:"runner,omitempty"`
	Model            string   `yaml:"model,omitempty"`
	Description      string   `yaml:"description,omitempty"`        // role description (custom workspace roles)
	Effort           string   `yaml:"effort,omitempty"`             // "" = unset, "default" = provider default
	BaseURL          string   `yaml:"base_url,omitempty"`           // API runners (openai, deepseek): endpoint base, e.g. https://openrouter.ai/api/v1
	Provider         string   `yaml:"provider,omitempty"`           // API runners: name of a saved custom provider (decsr connect custom) whose base_url is used when base_url is empty
	APIKeyEnv        string   `yaml:"api_key_env,omitempty"`        // API runners: NAME of the env var holding the key; the key is never stored
	MaxContextTokens int      `yaml:"max_context_tokens,omitempty"` // 0 = no limit (for local models)
	ReadOnly         bool     `yaml:"read_only,omitempty"`          // workspace role that reports only and never writes files
	Mode             string   `yaml:"mode,omitempty"`               // workspace roles: "" = by runner, "text" (decsr writes file blocks) or "agent" (the runner edits files with its own tools)
	Skills           []string `yaml:"-"`                            // always from defaults, never persisted
	UserSkills       []string `yaml:"skills,omitempty"`             // file-based skills (.decsr/skills/<name>.md)
}

// globalConfigDir returns the directory for the global config (~/.decsr).
func globalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, GlobalDir)
}

// EnsureGlobalDir creates ~/.decsr if it doesn't exist.
func EnsureGlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, GlobalDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

// Save writes cfg to <root>/.decsr/project.yaml.
func Save(root string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, GlobalDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return safefile.WriteFile(dir, ProjectFilename, data, 0o600)
}

// SaveGlobal writes cfg to ~/.decsr/config.yaml.
func SaveGlobal(cfg Config) error { return saveGlobal(cfg, false) }

// SaveGlobalEndpoints is SaveGlobal that also writes base_url and provider of
// every agent in cfg exactly as given, so an empty value removes it from the
// file (a plain SaveGlobal keeps the old value when the new one is empty).
func SaveGlobalEndpoints(cfg Config) error { return saveGlobal(cfg, true) }

func saveGlobal(cfg Config, exactEndpoints bool) error {
	dir, err := EnsureGlobalDir()
	if err != nil {
		return err
	}
	// Merge with existing global config to preserve other settings.
	existing := Config{}
	if gDir := globalConfigDir(); gDir != "" {
		loaded, loadErr := loadFile(gDir, GlobalFilename)
		switch {
		case loadErr == nil:
			existing = loaded
		case !errors.Is(loadErr, os.ErrNotExist):
			// Do not clobber an existing but unreadable/unparseable config.
			return loadErr
		}
	}
	merge(&existing, cfg)
	if exactEndpoints {
		for role, ac := range cfg.Agents {
			if cur, ok := existing.Agents[role]; ok {
				cur.BaseURL, cur.Provider = ac.BaseURL, ac.Provider
				existing.Agents[role] = cur
			}
		}
	}
	data, err := yaml.Marshal(existing)
	if err != nil {
		return err
	}
	return safefile.WriteFile(dir, GlobalFilename, data, 0o600)
}

// Load reads config with layered precedence:
// 1. Built-in defaults
// 2. ~/.decsr/config.yaml (global user settings — models, runners, skills)
// 3. <root>/.decsr/project.yaml (project-specific overrides)
func Load(root string) (Config, error) {
	cfg := DefaultConfig()

	// Layer 2: global user config.
	if gDir := globalConfigDir(); gDir != "" {
		if global, mem, err := loadLayer(gDir, GlobalFilename); err == nil {
			merge(&cfg, global)
			mem.apply(&cfg.Project.Context.Memory)
		}
	}

	// Layer 3: project-level config.
	projectDir := filepath.Join(root, GlobalDir)
	if project, mem, err := loadLayer(projectDir, ProjectFilename); err == nil {
		merge(&cfg, project)
		mem.apply(&cfg.Project.Context.Memory)
	} else if errors.Is(err, os.ErrNotExist) {
		// Fallback: try legacy root-level config and auto-migrate.
		if project, mem, legacyErr := loadLayer(root, Filename); legacyErr == nil {
			merge(&cfg, project)
			mem.apply(&cfg.Project.Context.Memory)
			// Migrate to the new location; only drop the legacy file once saved.
			if saveErr := Save(root, project); saveErr == nil {
				_ = os.Remove(filepath.Join(root, Filename))
			}
		}
	} else {
		return cfg, err
	}

	return cfg, nil
}

// LoadProject reads only the project-level config (<root>/.decsr/project.yaml)
// without layering defaults or global settings. Returns an empty config if
// the file does not exist.
func LoadProject(root string) Config {
	cfg, err := loadFile(filepath.Join(root, GlobalDir), ProjectFilename)
	if err != nil {
		return Config{}
	}
	return cfg
}

// loadFile reads and unmarshals a single YAML config file scoped to dir.
func loadFile(dir, filename string) (Config, error) {
	loaded, _, err := loadLayer(dir, filename)
	return loaded, err
}

// memoryOverrides records memory settings whose zero value is meaningful
// (false / 0) and therefore must be distinguished from "not set" in a layer.
type memoryOverrides struct {
	Enabled     *bool    `yaml:"enabled"`
	HybridAlpha *float64 `yaml:"hybrid_alpha"`
	AutoPromote *bool    `yaml:"auto_promote"`
}

func (o memoryOverrides) apply(dst *MemoryConfig) {
	if o.Enabled != nil {
		dst.Enabled = *o.Enabled
	}
	if o.HybridAlpha != nil {
		dst.HybridAlpha = *o.HybridAlpha
	}
	if o.AutoPromote != nil {
		dst.AutoPromote = *o.AutoPromote
	}
}

// loadLayer is loadFile plus the explicitly-set memory overrides of the file.
func loadLayer(dir, filename string) (Config, memoryOverrides, error) {
	data, err := safefile.ReadFile(dir, filename)
	if err != nil {
		return Config{}, memoryOverrides{}, err
	}
	var loaded Config
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		return Config{}, memoryOverrides{}, err
	}
	var raw struct {
		Project struct {
			Context struct {
				Memory memoryOverrides `yaml:"memory"`
			} `yaml:"context"`
		} `yaml:"project"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, memoryOverrides{}, err
	}
	return loaded, raw.Project.Context.Memory, nil
}

// mergeMemory copies non-zero fields from src into dst. A few fields (Enabled,
// UseEmbeddings, AutoPromote) are booleans, so the convention is "src=true
// overrides", "src=false leaves dst untouched". To explicitly disable, set
// the field at the level where you want it disabled and rely on layered load.
func mergeMemory(dst *MemoryConfig, src MemoryConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.TopK > 0 {
		dst.TopK = src.TopK
	}
	if src.ChunkTokens > 0 {
		dst.ChunkTokens = src.ChunkTokens
	}
	if src.OverlapTokens > 0 {
		dst.OverlapTokens = src.OverlapTokens
	}
	if src.HybridAlpha > 0 {
		dst.HybridAlpha = src.HybridAlpha
	}
	if src.MaxRecallChars > 0 {
		dst.MaxRecallChars = src.MaxRecallChars
	}
	if src.MaxPinnedChars > 0 {
		dst.MaxPinnedChars = src.MaxPinnedChars
	}
	if src.UseEmbeddings {
		dst.UseEmbeddings = true
	}
	if src.Embedder != "" {
		dst.Embedder = src.Embedder
	}
	if src.EmbedderModel != "" {
		dst.EmbedderModel = src.EmbedderModel
	}
	if src.EmbedderBaseURL != "" {
		dst.EmbedderBaseURL = src.EmbedderBaseURL
	}
	if src.EmbedderAPIKey != "" {
		dst.EmbedderAPIKey = src.EmbedderAPIKey
	}
	if src.EmbedderModelsDir != "" {
		dst.EmbedderModelsDir = src.EmbedderModelsDir
	}
	if src.AutoPromote {
		dst.AutoPromote = true
	}
}

func merge(dst *Config, src Config) {
	if src.Project.Name != "" {
		dst.Project.Name = src.Project.Name
	}
	if src.Project.Language != "" {
		dst.Project.Language = src.Project.Language
	}
	if src.Project.ModulePath != "" {
		dst.Project.ModulePath = src.Project.ModulePath
	}
	if src.Project.TestCmd != "" {
		dst.Project.TestCmd = src.Project.TestCmd
	}
	if src.Project.LintCmd != "" {
		dst.Project.LintCmd = src.Project.LintCmd
	}
	if src.Project.Profile != "" {
		dst.Project.Profile = src.Project.Profile
	}
	if src.Project.MaxFixAttempts > 0 {
		dst.Project.MaxFixAttempts = src.Project.MaxFixAttempts
	}
	if len(src.Project.Scope.Allow) > 0 {
		dst.Project.Scope.Allow = src.Project.Scope.Allow
	}
	if len(src.Project.Scope.Deny) > 0 {
		dst.Project.Scope.Deny = src.Project.Scope.Deny
	}
	if len(src.Project.Context.AlwaysInclude) > 0 {
		dst.Project.Context.AlwaysInclude = src.Project.Context.AlwaysInclude
	}
	if len(src.Project.Context.ExcludePatterns) > 0 {
		dst.Project.Context.ExcludePatterns = src.Project.Context.ExcludePatterns
	}
	if src.Project.Context.SemanticIndex.Enabled {
		dst.Project.Context.SemanticIndex.Enabled = true
	}
	if src.Project.Context.SemanticIndex.Embedder != "" {
		dst.Project.Context.SemanticIndex.Embedder = src.Project.Context.SemanticIndex.Embedder
	}
	if src.Project.Context.SemanticIndex.Model != "" {
		dst.Project.Context.SemanticIndex.Model = src.Project.Context.SemanticIndex.Model
	}
	if src.Project.Context.SemanticIndex.BaseURL != "" {
		dst.Project.Context.SemanticIndex.BaseURL = src.Project.Context.SemanticIndex.BaseURL
	}
	if src.Project.Context.SemanticIndex.TopK > 0 {
		dst.Project.Context.SemanticIndex.TopK = src.Project.Context.SemanticIndex.TopK
	}
	if src.Project.Context.ScopedContextShadow {
		dst.Project.Context.ScopedContextShadow = true
	}
	if src.Project.Context.ScopedContextCoderFix {
		dst.Project.Context.ScopedContextCoderFix = true
	}
	if src.Project.Context.Pack.Disable {
		dst.Project.Context.Pack.Disable = true
	}
	if src.Project.Context.Pack.MaxOutputTokens > 0 {
		dst.Project.Context.Pack.MaxOutputTokens = src.Project.Context.Pack.MaxOutputTokens
	}
	mergeMemory(&dst.Project.Context.Memory, src.Project.Context.Memory)
	if src.PromptLanguage != "" {
		dst.PromptLanguage = src.PromptLanguage
	}

	if src.ModelMemory.Mode != "" {
		dst.ModelMemory.Mode = src.ModelMemory.Mode
	}
	if src.ModelMemory.MaxRAMGB > 0 {
		dst.ModelMemory.MaxRAMGB = src.ModelMemory.MaxRAMGB
	}
	if src.ModelMemory.MaxContextTokens > 0 {
		dst.ModelMemory.MaxContextTokens = src.ModelMemory.MaxContextTokens
	}

	if src.Notifications.DisableCMux {
		dst.Notifications.DisableCMux = true
	}
	if src.Notifications.DisableBell {
		dst.Notifications.DisableBell = true
	}
	if src.Notifications.DisableDesktop {
		dst.Notifications.DisableDesktop = true
	}
	if src.Notifications.Webhook != "" {
		dst.Notifications.Webhook = src.Notifications.Webhook
	}
	if src.Notifications.WebhookFormat != "" {
		dst.Notifications.WebhookFormat = src.Notifications.WebhookFormat
	}

	if src.UI.Icons != "" {
		dst.UI.Icons = src.UI.Icons
	}
	if src.UI.Tmux {
		dst.UI.Tmux = true
	}

	if src.Workspace.MaxParallel > 0 {
		dst.Workspace.MaxParallel = src.Workspace.MaxParallel
	}
	if src.Workspace.Isolation != "" {
		dst.Workspace.Isolation = src.Workspace.Isolation
	}
	if src.Workspace.PRBase != "" {
		dst.Workspace.PRBase = src.Workspace.PRBase
	}
	if src.Workspace.PRRemote != "" {
		dst.Workspace.PRRemote = src.Workspace.PRRemote
	}
	if src.Workspace.PRPollSeconds > 0 {
		dst.Workspace.PRPollSeconds = src.Workspace.PRPollSeconds
	}
	if src.Workspace.MaxCIFixes > 0 {
		dst.Workspace.MaxCIFixes = src.Workspace.MaxCIFixes
	}
	if src.Workspace.PRAutoFix {
		dst.Workspace.PRAutoFix = true
	}
	if src.Workspace.BudgetUSD > 0 {
		dst.Workspace.BudgetUSD = src.Workspace.BudgetUSD
	}
	if src.Workspace.BudgetWarnPct > 0 {
		dst.Workspace.BudgetWarnPct = src.Workspace.BudgetWarnPct
	}
	if src.Workspace.Autonomy > 0 {
		dst.Workspace.Autonomy = src.Workspace.Autonomy
	}
	if src.Workspace.MaxAutoFixes > 0 {
		dst.Workspace.MaxAutoFixes = src.Workspace.MaxAutoFixes
	}
	if src.Workspace.Coordinator {
		dst.Workspace.Coordinator = true
	}
	if len(src.Pricing) > 0 {
		if dst.Pricing == nil {
			dst.Pricing = make(map[string]ModelPrice, len(src.Pricing))
		}
		for k, v := range src.Pricing {
			dst.Pricing[k] = v
		}
	}

	if src.Agents != nil {
		if dst.Agents == nil {
			dst.Agents = make(map[string]AgentConfig)
		}
		for role, ac := range src.Agents {
			existing, ok := dst.Agents[role]
			if !ok {
				dst.Agents[role] = ac
				continue
			}
			if ac.Runner != "" {
				existing.Runner = ac.Runner
			}
			if ac.Model != "" {
				existing.Model = ac.Model
			}
			if ac.Effort != "" {
				existing.Effort = ac.Effort
			}
			if ac.BaseURL != "" {
				existing.BaseURL = ac.BaseURL
			}
			if ac.APIKeyEnv != "" {
				existing.APIKeyEnv = ac.APIKeyEnv
			}
			if ac.Provider != "" {
				existing.Provider = ac.Provider
			}
			if ac.Description != "" {
				existing.Description = ac.Description
			}
			if ac.MaxContextTokens > 0 {
				existing.MaxContextTokens = ac.MaxContextTokens
			}
			if ac.ReadOnly {
				existing.ReadOnly = true
			}
			if ac.Mode != "" {
				existing.Mode = ac.Mode
			}
			if len(ac.UserSkills) > 0 {
				existing.UserSkills = ac.UserSkills
			}
			dst.Agents[role] = existing
		}
	}
}

// BuiltinRoles are the agent roles shipped with DECSR, in display order.
var BuiltinRoles = []string{"pm", "coder", "coder_fixer", "qa", "ux_reviewer", "security"}

// IsBuiltinRole reports whether name is one of the built-in roles.
func IsBuiltinRole(name string) bool {
	for _, r := range BuiltinRoles {
		if r == name {
			return true
		}
	}
	return false
}

var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// ValidRoleName reports whether name is a valid custom role name.
func ValidRoleName(name string) bool {
	return roleNameRe.MatchString(name)
}

// ValidQualifiedName reports whether s is "<plugin>:<name>", both parts valid
// per ValidName. It names plugin skills, commands and agent roles.
func ValidQualifiedName(s string) bool {
	plugin, name, ok := strings.Cut(s, ":")
	return ok && ValidName(plugin) && ValidName(name)
}

// SaveProjectRole sets agents[name] in <root>/.decsr/project.yaml, keeping the
// rest of the project settings.
// It fails without touching the file when project.yaml exists but cannot be
// parsed.
func SaveProjectRole(root, name string, ac AgentConfig) error {
	cfg, err := loadFile(filepath.Join(root, GlobalDir), ProjectFilename)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", ProjectFilename, err)
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]AgentConfig)
	}
	existing := cfg.Agents[name]
	existing.Description = ac.Description
	existing.Runner = ac.Runner
	existing.Model = ac.Model
	existing.Effort = ac.Effort
	existing.ReadOnly = ac.ReadOnly
	existing.Mode = ac.Mode
	if ac.UserSkills != nil {
		existing.UserSkills = ac.UserSkills
	}
	cfg.Agents[name] = existing
	return Save(root, cfg)
}

// Agent modes of a workspace role.
const (
	ModeText  = "text"  // the runner answers with text and decsr writes the file blocks
	ModeAgent = "agent" // the runner edits files itself with its own tools
)

// modeRunner is the runner the mode rules apply to: an empty runner is opencode.
func (ac AgentConfig) modeRunner() string {
	if ac.Runner == "" {
		return "opencode"
	}
	return ac.Runner
}

// EffectiveMode is the explicit mode, else "agent" for opencode and codex and
// "text" for every other runner.
func (ac AgentConfig) EffectiveMode() string {
	if ac.Mode != "" {
		return ac.Mode
	}
	switch ac.modeRunner() {
	case "opencode", "codex":
		return ModeAgent
	}
	return ModeText
}

// ModeError explains why the role's mode cannot run, or returns nil. Agent mode
// needs a CLI that edits files with its own tools. opencode has no reliable
// read-only switch, so text mode (where decsr alone writes the files) cannot be
// guaranteed for it.
func (ac AgentConfig) ModeError() error {
	switch ac.Mode {
	case "", ModeText, ModeAgent:
	default:
		return fmt.Errorf("invalid mode %q (use text or agent)", ac.Mode)
	}
	r := ac.modeRunner()
	switch ac.EffectiveMode() {
	case ModeAgent:
		if r != "claude" && r != "opencode" && r != "codex" {
			return errors.New("agent mode needs claude, opencode or codex")
		}
	case ModeText:
		if r == "opencode" {
			return errors.New("text mode is not supported for opencode (it has no read-only switch); use mode: agent or another runner")
		}
	}
	return nil
}
