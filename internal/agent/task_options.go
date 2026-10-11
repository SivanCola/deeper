package agent

import (
	"context"

	"deepzora/internal/checkpoint"
	"deepzora/internal/event"
	"deepzora/internal/imageinput"
	"deepzora/internal/provider"
	"deepzora/internal/runtimepolicy"
	"deepzora/internal/tool"
)

// NewTaskToolWithOptions is the internal standard constructor for TaskTool.
// An empty SysPrompt still resolves to DefaultTaskSystemPrompt. No extra
// validation or default overrides are applied.
func NewTaskToolWithOptions(opts TaskToolOptions) *TaskTool {
	sysPrompt := opts.SysPrompt
	if sysPrompt == "" {
		sysPrompt = DefaultTaskSystemPrompt
	}
	return &TaskTool{
		imageInput:       opts.ImageInput,
		prov:             opts.Provider,
		pricing:          opts.Pricing,
		quoteContext:     opts.QuoteContext,
		parentReg:        opts.ParentRegistry,
		maxSteps:         opts.MaxSteps,
		contextWindow:    opts.ContextWindow,
		compaction:       compactionThreshold{opts.CompactRatio, opts.CompactRatioSource},
		temperature:      opts.Temperature,
		archiveDir:       opts.ArchiveDir,
		sysPrompt:        sysPrompt,
		gate:             opts.Gate,
		subagentModel:    opts.SubagentModel,
		subagentEffort:   opts.SubagentEffort,
		resolveProvider:  opts.ResolveProvider,
		maxSubagentDepth: DefaultMaxSubagentDepth,
		imageResolver:    opts.ImageRequestResolver,
		hooksForSession:  opts.HooksForSession,
	}
}

// subagentOptions is the single construction point for the run options every
// sub-agent spawned through this tool shares (task, read_only_task, and
// parallel_tasks children). Compaction, language preferences, and depth limits
// must stay uniform across those paths — add new fields here, not at call sites.
func (t *TaskTool) subagentOptions(ctx context.Context, maxSteps int, pricing *provider.Pricing, ctxWin, childDepth int, recoveryTaskID string, mutationObserver *checkpoint.MutationObserver) Options {
	opts := Options{
		ImageInput:               t.imageInput,
		MaxSteps:                 maxSteps,
		MaxOutputTokens:          childOutputBudgetFrom(ctx),
		Temperature:              t.temperature,
		Pricing:                  pricing,
		QuoteContext:             t.quoteContext,
		UsageSource:              event.UsageSourceSubagent,
		Gate:                     t.gate,
		ContextWindow:            ctxWin,
		CompactRatio:             t.compaction.ratio(),
		ArchiveDir:               t.archiveDir,
		ResponseLanguage:         ResponseLanguageFromContext(ctx),
		ReasoningLanguage:        ReasoningLanguageFromContext(ctx),
		SubagentDepth:            childDepth,
		MaxSubagentDepth:         t.maxDepth(),
		Ablation:                 t.ablation,
		WorkspaceLease:           t.workspaceLease,
		MutationObserver:         mutationObserver,
		WriteRoots:               t.writeRoots,
		DisableWriteAccessExpand: true,
		WriteWorkspaceRoot:       t.workspaceRoot,
		ImageRequestResolver:     t.imageResolver,
	}
	if t.hooksForSession != nil {
		opts.Hooks = t.hooksForSession(recoveryTaskID)
	}
	// Writer children inherit the parent turn's frozen risk and closure floors.
	// The parent publishes its policy into the run context; a child that never
	// received it (direct unit construction) keeps its own derived policy.
	if inherited, ok := runtimepolicy.InheritedFromContext(ctx); ok {
		copy := inherited
		opts.InheritedExecution = &copy
	} else if constraints, ok := runtimepolicy.FromContext(ctx); ok {
		opts.InheritedExecution = &runtimepolicy.InheritedExecutionContext{Constraints: constraints}
	}
	return opts
}

func (t *TaskTool) WithImageRequestResolver(resolver ImageRequestResolver) *TaskTool {
	if t == nil {
		return nil
	}
	t.imageResolver = resolver
	return t
}

// TaskToolOptions holds the construction parameters for a TaskTool.
// Construct tasks through NewTaskToolWithOptions.
type TaskToolOptions struct {
	ImageInput           *imageinput.Config
	ImageRequestResolver ImageRequestResolver
	HooksForSession      func(string) ToolHooks
	Provider             provider.Provider
	Pricing              *provider.Pricing
	QuoteContext         *event.QuoteContext
	ParentRegistry       *tool.Registry
	MaxSteps             int
	ContextWindow        int
	CompactRatio         float64
	// CompactRatioSource must be concurrency-safe; each child snapshots it once.
	CompactRatioSource    func() float64
	Temperature           float64
	ArchiveDir, SysPrompt string
	Gate                  Gate
	SubagentModel         string
	SubagentEffort        string
	ResolveProvider       func(string, string) (provider.Provider, *provider.Pricing, int, error)
}
