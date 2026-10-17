package boot

import (
	"context"
	"fmt"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/extension"
	"deepzora/internal/provider"
)

// RebuildFrom is Rebuild using previous BuildResult for incremental sidecars
// and subgraph-classified assembly (no-op / interceptor-only / UI-only, …).
func RebuildFrom(ctx context.Context, previous *BuildResult, opts Options) (*BuildResult, error) {
	if previous == nil || previous.Controller == nil {
		return nil, fmt.Errorf("boot: RebuildFrom requires the BuildResult being replaced")
	}
	if previous.Extensions != nil {
		opts.Extensions = previous.Extensions
	}
	if previous.Plan != nil && previous.Plan.Graph != nil {
		opts.Graph = previous.Plan.Graph
	}
	if previous.Snapshot != nil {
		opts.Generation = previous.Snapshot.Generation()
		opts.PreviousSnapshot = previous.Snapshot
	}
	if previous.Dispatcher != nil {
		opts.PreviousDispatcher = previous.Dispatcher
	}
	if previous.Owner != nil {
		opts.Owner = previous.Owner
	}
	return rebuildWithPrevious(ctx, previous.Controller, previous, opts)
}

// Rebuild prepares a replacement controller while retaining its Session Runtime,
// model context, permission grants and session-private resources. The host must
// serialize admission, activate the candidate atomically and retire the outgoing
// controller only after publication. A failed build leaves the old owner usable.
func Rebuild(ctx context.Context, old *control.Controller, opts Options) (*BuildResult, error) {
	return rebuildWithPrevious(ctx, old, nil, opts)
}

func rebuildWithPrevious(ctx context.Context, old *control.Controller, previous *BuildResult, opts Options) (*BuildResult, error) {
	if old == nil {
		return nil, fmt.Errorf("boot: Rebuild requires the controller being replaced")
	}
	scope, finishBackground, abortBackground, err := control.ReserveBackgroundReplacement(old)
	if err != nil {
		return nil, err
	}
	opts.BackgroundScope = scope
	defer abortBackground()
	if opts.Owner == nil {
		opts.Owner = old.RuntimeOwner()
	}
	opts.inheritSessionBinding(old)
	// Capture migratable state before building: every accessor returns a
	// copy, so a slow build cannot observe a half-appended turn.
	m := runtimeMigration{
		carried:          old.History(),
		authorizations:   old.SessionAuthorizations(),
		toolApprovalMode: old.ToolApprovalMode(),
		planMode:         old.PlanMode(),
		goal:             old.Goal(),
		goalRunning:      old.GoalStatus() == control.GoalStatusRunning,
	}
	// Reuse the previous Controller's session-private temporary directory so
	// model/settings hot rebuilds do not wipe temporary files mid-session.
	if opts.SessionTemp == nil {
		opts.SessionTemp = old.SessionTemp()
	}
	if opts.PersistentShell == nil {
		opts.PersistentShell = old.PersistentShell()
	}
	if opts.WorkspaceRepo.Dir == "" {
		opts.WorkspaceRepo = old.WorkspaceRepo()
	}

	home := config.DeepzoraHomeDir()
	// fromGraph must be the PREVIOUS generation's graph when available.
	// Building "current disk" for both from and to collapses every plan to no-op.
	var fromGraph *extension.DependencyGraph
	if previous != nil && previous.Plan != nil && previous.Plan.Graph != nil {
		fromGraph = previous.Plan.Graph
	} else if g, err := buildRuntimeGraph(home, nil); err == nil {
		fromGraph = g
	}
	opts.Graph = fromGraph

	// Prefer subgraph-classified rebuild when previous assembly is available.
	if previous != nil && !opts.ForceFullRebuild {
		if res, handled, err := tryRebuildSubgraph(ctx, old, previous, opts); handled {
			return res, err
		}
	}

	extension.DefaultLifecycleMetrics.FullRebuilds.Add(1)
	opts.deferPublish = true
	res, err := BuildRuntime(ctx, opts)
	if err != nil {
		// Activation failure: new generation never published; old keeps serving.
		return nil, err
	}

	var toGraph *extension.DependencyGraph
	if g, err := buildRuntimeGraph(home, nil); err == nil {
		toGraph = g
	}
	var previousSnapshot *extension.RuntimeSnapshot
	if previous != nil {
		previousSnapshot = previous.Snapshot
	}
	attachPlanAndStatus(res, fromGraph, toGraph, opts.Generation, previousSnapshot)

	if err := migrateRuntimeState(res.Controller, old, m); err != nil {
		// Fail-atomic: release the replacement; old keeps serving.
		// Activation never reached Active publish.
		if res.Snapshot != nil {
			res.Owner.Gate.BeginDrain(res.Snapshot.Generation())
		}
		res.Controller.ReleaseResources()
		if res.Runtime != nil {
			_ = res.Runtime.Close()
		}
		return nil, err
	}
	if prevGen := old.RuntimeGeneration(); prevGen != 0 && (res.Snapshot == nil || prevGen != res.Snapshot.Generation()) {
		registerControllerDrainCancel(res.Owner, prevGen, old)
		if host := old.Host(); host != nil {
			h := host
			res.Owner.Gate.RegisterDrainCancel(prevGen, func() { h.CancelInFlightMCP() })
		}
	}
	// SessionEnd is not fired on ordinary rebuild.
	if err := finishBackground(res.Controller); err != nil {
		res.Controller.ReleaseResources()
		return nil, err
	}
	// A host can still reject the prepared candidate. Its extension generation
	// must not retire the outgoing runtime before the final ownership transfer.
	stageModelRuntimePublication(res, opts)
	return res, nil
}

// runtimeMigration carries the captured old-controller state into
// migrateRuntimeState.
type runtimeMigration struct {
	carried          []provider.Message
	authorizations   control.SessionAuthorizations
	toolApprovalMode string
	planMode         bool
	goal             string
	goalRunning      bool
}

// migrateRuntimeState restores captured state on the prepared controller.
// Model-context persistence and lifecycle adoption must succeed before the
// host can publish the replacement.
func migrateRuntimeState(ctrl, old *control.Controller, m runtimeMigration) error {
	_, oldRuntime, oldBound := old.SessionBinding()
	_, newRuntime, newBound := ctrl.SessionBinding()
	if oldBound {
		if !newBound || newRuntime != oldRuntime {
			return fmt.Errorf("rebuild changed the current session runtime")
		}
		// Construction restores the old runtime's model context. Use the assembled
		// prompt so preset edits reach the replacement without rewriting history.
		carried := spliceFreshSystemPrompt(m.carried, agent.NewSession(ctrl.SystemPrompt()).Snapshot())
		if err := ctrl.AdoptRebuiltModelContext(carried); err != nil {
			return err
		}
	} else {
		// A controller can be assembled before its first session is allocated.
		// It has no conversation to transfer; keep the fresh build's prompt.
		// Historical in-memory/path conversations must not enter this boundary.
		for _, message := range m.carried {
			if message.Role != provider.RoleSystem {
				return fmt.Errorf("rebuilding a conversation requires a current session runtime")
			}
		}
	}

	// Rebuilds preserve the live frontend mode, including a transient TUI
	// downgrade inside a session that once stored a remote Serve preset.
	ctrl.SetToolApprovalMode(m.toolApprovalMode)
	ctrl.SetPlanMode(m.planMode)
	if m.goalRunning && strings.TrimSpace(m.goal) != "" && strings.TrimSpace(ctrl.Goal()) == "" {
		ctrl.SetGoal(m.goal)
	}
	if err := ctrl.InheritLifecycleFrom(old); err != nil {
		return fmt.Errorf("inherit controller lifecycle: %w", err)
	}
	ctrl.RestoreSessionAuthorizations(m.authorizations)
	return nil
}

// spliceFreshSystemPrompt replaces the carried conversation's system message
// with the fresh build's, so the resumed session speaks the rebuilt profile
// contract. A carried conversation without a system message gets the fresh
// one prepended; a fresh build without one leaves the conversation untouched.
func spliceFreshSystemPrompt(carried, fresh []provider.Message) []provider.Message {
	var system *provider.Message
	for i := range fresh {
		if fresh[i].Role == provider.RoleSystem {
			system = &fresh[i]
			break
		}
	}
	if system == nil {
		return carried
	}
	out := append([]provider.Message(nil), carried...)
	for i := range out {
		if out[i].Role == provider.RoleSystem {
			out[i] = *system
			return out
		}
	}
	return append([]provider.Message{*system}, out...)
}
