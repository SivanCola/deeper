package main

import (
	"context"
	"log/slog"
	"strings"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

// sessionDisplayTitle is the one name projection every local surface uses for
// a canonical session, so the sidebar row and the topicbar cannot disagree.
// The session log owns the name once any rename has committed, including an
// explicitly cleared one; before that, the current presentation title counts;
// an untitled session shows its first user turn.
// The creation default is never a real name and only closes the chain.
func sessionDisplayTitle(info session.SessionInfo, presentation workspacestate.Presentation) (string, string) {
	if title := strings.TrimSpace(info.Title); title != "" {
		return title, topicTitleSourceManual
	}
	if info.TitleSequence == 0 {
		if title := strings.TrimSpace(presentation.Title); title != "" {
			return title, topicTitleSourceManual
		}
	}
	if preview := strings.TrimSpace(info.Preview); preview != "" {
		return preview, topicTitleSourceAuto
	}
	return defaultTopicTitle, topicTitleSourceAuto
}

// canonicalTabTitle resolves the projected name for ref. Callers run it before
// taking App.mu: the catalog read may touch the filesystem.
func (a *App) canonicalTabTitle(ctx context.Context, state workspacestate.State, ref session.SessionRef) (string, string) {
	return a.canonicalTabTitleWithPresentation(ctx, state.Presentation[ref.SessionID], ref)
}

func (a *App) canonicalTabTitleWithPresentation(ctx context.Context, presentation workspacestate.Presentation, ref session.SessionRef) (string, string) {
	info, err := a.desktopSessionService("").Query().Stat(ctx, ref)
	if err != nil {
		info = session.SessionInfo{}
	}
	return sessionDisplayTitle(info, presentation)
}

// publishCanonicalSessionTitle is the single exit of every canonical rename.
// It mirrors the committed log title into each local reader that still
// caches a name: the presentation row (cold binds, trash rows, fork labels),
// the open tabs, the prompt-history cache, and the project tree.
func (a *App) publishCanonicalSessionTitle(ref session.SessionRef, title string) {
	title = strings.TrimSpace(title)
	ctx := a.bootContext()
	if err := a.workspaceRegistry().UpdatePresentation(ctx, []string{ref.SessionID}, &title, nil); err != nil {
		slog.Warn("desktop: session title presentation write-through failed", "session", ref.SessionID, "err", err)
	}
	display, source := title, topicTitleSourceManual
	if display == "" {
		// A cleared name falls back through the shared chain instead of
		// leaving the tab blank while the sidebar shows the preview.
		state, err := a.workspaceRegistry().Load(ctx)
		if err != nil {
			state = workspacestate.State{}
		}
		display, source = a.canonicalTabTitle(ctx, state, ref)
	}
	a.updateCanonicalSessionTitle(ref, display, source)
	a.invalidatePromptHistoryCache()
	a.emitProjectTreeChanged()
}
