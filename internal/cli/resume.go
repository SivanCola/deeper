package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"deepzora/internal/control"
	"deepzora/internal/i18n"
	"deepzora/internal/session"
)

const resumeListCap = 10

// resumeEntry is one picker row: a session plus, for cross-project rows, the
// project it belongs to. The current directory's sessions keep project empty
// so labels stay compact. The target carries the session identity and catalog.
type resumeEntry struct {
	session session.SessionInfo
	project string
	target  cliResumeTarget
}

const resumeOtherProjectsCap = 5

// resumeEntries lists the current directory's resumable conversations, then the newest session
// of other known projects. A user who worked on this machine over SSH resumes
// from any directory, not only the workspace root (#9477), and must see the
// same conversations the desktop tree shows.
func resumeEntries(dir string) []resumeEntry {
	base := mergedResumeEntries(dir, resumeListCap)
	out := make([]resumeEntry, 0, len(base)+resumeOtherProjectsCap)
	out = append(out, base...)
	out = append(out, otherProjectResumeEntries(dir)...)
	return out
}

func otherProjectResumeEntries(excludeDir string) []resumeEntry {
	type target struct {
		path string
		root string
	}
	var targets []target
	for _, t := range defaultSessionCatalogTargets() {
		if t.Scope != "project" || t.Path == "" {
			continue
		}
		targets = append(targets, target{path: t.Path, root: t.WorkspaceRoot})
	}
	exclude := filepath.Clean(excludeDir)
	var out []resumeEntry
	for _, t := range targets {
		if filepath.Clean(t.path) == exclude {
			continue
		}
		rows := canonicalResumeEntries(context.Background(), t.path)
		if len(rows) == 0 {
			continue
		}
		name := filepath.Base(strings.TrimRight(t.root, string(filepath.Separator)))
		if name == "" || name == "." {
			name = t.root
		}
		entry := rows[0]
		entry.project = name
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].session.UpdatedAt.After(out[j].session.UpdatedAt)
	})
	if len(out) > resumeOtherProjectsCap {
		out = out[:resumeOtherProjectsCap]
	}
	return out
}

// runResumeCommand handles "/resume": with no argument it opens the recent
// session picker; "/resume <n>" loads that
// session into the running controller in place — keeping the current model and
// replaying the transcript into scrollback.
func (m *chatTUI) runResumeCommand(input string) {
	args := tokenizeArgs(input) // args[0] == "/resume"
	if len(args) < 2 {
		m.openResumePicker()
		return
	}
	// Never run recovery GC between displaying a numeric index and resolving it
	// here: dropping an earlier row would silently retarget the number the user
	// already picked. Bare /resume and startup do that cleanup instead.
	entries := resumeEntries(m.ctrl.SessionDir())
	if len(entries) == 0 {
		m.notice(i18n.M.NoSessionToResume)
		return
	}
	if m.ctrl.Running() {
		m.notice(i18n.M.ResumeBusy)
		return
	}
	idx, err := strconv.Atoi(strings.TrimSpace(args[1]))
	if err != nil || idx < 1 || idx > len(entries) {
		m.notice(fmt.Sprintf(i18n.M.ResumeBadIndexFmt, len(entries)))
		return
	}
	target := entries[idx-1]
	if resumeEntryIsActive(m.ctrl, target) {
		m.notice(i18n.M.ResumeAlreadyActive)
		return
	}
	detached := m.sessionReclaimed || m.takeover != nil && m.takeover.Returned()
	if !detached {
		// Persist the conversation we're leaving so switching back later restores it.
		// Snapshot before moving the lease: the outgoing session must be written
		// while this process still owns it.
		if err := m.ctrl.Snapshot(); err != nil {
			m.notice("resume: snapshot current session: " + err.Error())
			return
		}
		m.syncTakeoverIdentity()
	}
	if err := m.commitResumeTarget(target.target); err != nil {
		if errors.Is(err, session.ErrWriterOwned) {
			m.pendingTakeoverTarget = target.target
			m.notice("resume: " + sessionWriterHeldNotice())
			m.notice("run /takeover to take this session over")
			return
		}
		m.notice("resume: " + err.Error())
		return
	}

	m.resumeAfterReclaim()
	m.replayActiveBranch(i18n.M.ResumedTitle)
}

// resumeEntryIsActive reports whether a picker row is the controller's current
// conversation. Canonical rows carry no live path, so they compare session
// identities instead of transcript files.
func resumeEntryIsActive(ctrl control.SessionAPI, entry resumeEntry) bool {
	if entry.target.canonical() {
		if identity, ok := ctrl.(control.IdentityLifecycle); ok {
			if entry.target.service != nil && identity.SessionService() != entry.target.service {
				return false
			}
			if ref, bound := identity.SessionRef(); bound && ref == entry.target.ref {
				return true
			}
		}
		return false
	}
	return false
}

func (m *chatTUI) commitResumeTarget(target cliResumeTarget) error {
	ref := target.ref
	identity, ok := m.ctrl.(control.IdentityLifecycle)
	if !ok || !identity.UsesExclusiveSession() {
		return errors.New("final-format session resume requires the session engine")
	}
	service := target.service
	if service == nil {
		service = identity.SessionService()
	}
	if service == nil {
		return errors.New("session service unavailable")
	}
	ctx := context.Background()
	// The probe grant secures the target writer before anything changes hands,
	// so a held target (ErrWriterOwned) leaves this session, its lease and its
	// mirror untouched. Unsupported stores are refused without changing them.
	probe, err := service.Open(ctx, ref)
	if err != nil {
		return err
	}
	if probe != nil {
		defer func() { _ = probe.Release(ctx) }()
	}
	replacement, err := m.preparePresetResume(ref, service)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if replacement != nil && !committed {
			replacement.Close()
		}
	}()
	if m.takeover != nil {
		if err := m.takeover.leaveMirror(""); err != nil {
			return err
		}
	}
	if replacement != nil {
		identity = replacement
	}
	if _, err := identity.OpenSessionFromService(ctx, service, ref); err != nil {
		return err
	}
	if replacement != nil {
		if err := m.publishPresetResume(replacement); err != nil {
			return err
		}
	}
	committed = true
	// The controller now holds its own grant on the runtime; the probe's
	// release cannot retire it.
	if probe != nil {
		if err := probe.Release(ctx); err != nil {
			return fmt.Errorf("release session probe grant: %w", err)
		}
	}
	return nil
}

// runTakeoverCommand handles "/takeover": it force-takes the last refused
// resume target (or an explicit index/identity argument) from the resident serve
// on this machine, then resumes it.
func (m *chatTUI) runTakeoverCommand(input string) {
	m.echoLocalCommand(input)
	args := tokenizeArgs(input) // args[0] == "/takeover"
	target := ""
	if !m.pendingTakeoverTarget.empty() {
		target = cliCanonicalRoute(m.pendingTakeoverTarget.ref.SessionID)
	}
	if len(args) >= 2 {
		target = strings.TrimSpace(args[1])
		if idx, err := strconv.Atoi(target); err == nil {
			entries := resumeEntries(m.ctrl.SessionDir())
			if idx < 1 || idx > len(entries) {
				m.notice(fmt.Sprintf(i18n.M.ResumeBadIndexFmt, len(entries)))
				return
			}
			picked := entries[idx-1]
			m.pendingTakeoverTarget = picked.target
			target = cliCanonicalRoute(picked.target.ref.SessionID)
		}
	}
	if target == "" && m.sessionReclaimed {
		// The reclaim notice promises "/takeover takes it back": the session
		// the desktop re-claimed is remembered in reclaimedTarget, not in
		// pendingTakeoverTarget (which only a refused resume populates).
		if m.reclaimedTarget.canonical() {
			target = cliCanonicalRoute(m.reclaimedTarget.ref.SessionID)
		}
	}
	if target == "" {
		m.notice("takeover: no refused session; run /resume <n> first or pass an index")
		return
	}
	if !isCLICanonicalRoute(target) {
		m.notice("takeover: use a session index or session-id identity")
		return
	}
	m.runCanonicalTakeoverCommand(target)
}

// resumeArgItems completes the index argument of "/resume <n>": once past the
// command word it lists recent sessions, inserting the 1-based index and
// showing timestamp + turn count + preview as the hint. Indices match
// the picker because both window through recentSessions.
func (m *chatTUI) resumeArgItems(val string) ([]compItem, int, bool) {
	cmdEnd := strings.IndexAny(val, " \t")
	if cmdEnd < 0 || val[:cmdEnd] != "/resume" {
		return nil, 0, false
	}
	from := strings.LastIndexAny(val, " \t") + 1
	if len(strings.Fields(val[:from])) != 1 || m.ctrl == nil {
		return nil, from, true
	}
	cur := val[from:]
	var out []compItem
	for i, entry := range resumeEntries(m.ctrl.SessionDir()) {
		idx := strconv.Itoa(i + 1)
		if cur != "" && !strings.HasPrefix(idx, cur) {
			continue
		}
		hint := fmt.Sprintf("%s · %s", entry.session.UpdatedAt.Local().Format("01-02 15:04"), sessionSummary(entry.session))
		if entry.project != "" {
			hint = fmt.Sprintf("[%s] %s", entry.project, hint)
		}
		out = append(out, compItem{label: idx, insert: idx, hint: hint})
	}
	return out, from, true
}

// sessionSummary is the "N turns · display title" line shared by the /resume
// list and its argument completion. Explicit session titles win, then the preview so the user can identify sessions at a glance.
func sessionSummary(s session.SessionInfo) string {
	preview := strings.TrimSpace(s.Title)
	if preview == "" {
		preview = s.Preview
	}
	if preview == "" {
		preview = "(no user message yet)"
	}
	return fmt.Sprintf("%d turns · %s", s.Turns, preview)
}
