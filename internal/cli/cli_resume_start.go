package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"deepzora/internal/control"
	"deepzora/internal/i18n"
	"deepzora/internal/session"
)

// headlessResumeTarget resolves --resume/--continue for the non-interactive
// agent path, before any heavy assembly, so --copy and the session lease are
// settled first. --resume takes precedence over --continue, matching the
// Resume call downstream, and accepts file paths, branch IDs, preview text,
// opaque machine session IDs (#7429) and final-format session identities.
// The int is a process exit code: 0 means the (possibly empty) target is
// usable, anything else is the code the command must exit with.
func headlessResumeTarget(resume string, cont, copySession bool) (cliResumeTarget, int) {
	target := cliResumeTarget{}
	if strings.TrimSpace(resume) != "" {
		resolved, rc := resumeQueryTarget(resolveCLISessionDir(), strings.TrimSpace(resume), os.Stderr, nil)
		if rc != 0 {
			return cliResumeTarget{}, rc
		}
		target = resolved
	}
	if target.empty() && cont {
		continued, rc := continueResumeTarget()
		if rc != 0 {
			return cliResumeTarget{}, rc
		}
		target = continued
	}
	return target, copySessionExitCode(copySession, target)
}

// interactiveResumeTarget resolves the same selection for the TUI, where the
// picker sentinel opens the chooser instead of failing. resumeValue must
// already be normalized by normalizedResumeFlag.
func interactiveResumeTarget(resumeValue string, cont, copySession bool) (cliResumeTarget, int) {
	var target cliResumeTarget
	switch {
	case resumeValue == resumePickerSentinel:
		picked, rc := pickSessionToResume()
		if rc != 0 {
			return cliResumeTarget{}, rc
		}
		target = picked
	case resumeValue != "":
		var choose func([]resumeEntry) (cliResumeTarget, int)
		if isInteractive() {
			choose = chooseResumeEntry
		}
		resolved, rc := resumeQueryTarget(resolveCLISessionDir(), resumeValue, os.Stderr, choose)
		if rc != 0 {
			return cliResumeTarget{}, rc
		}
		target = resolved
	case cont:
		continued, rc := continueResumeTarget()
		if rc != 0 {
			return cliResumeTarget{}, rc
		}
		target = continued
	}
	return target, copySessionExitCode(copySession, target)
}

// resumeQueryTarget resolves --resume QUERY in dir. A query matching several
// conversations goes to choose when one is given; otherwise the candidates are
// listed on w and the start fails.
func resumeQueryTarget(dir, query string, w io.Writer, choose func([]resumeEntry) (cliResumeTarget, int)) (cliResumeTarget, int) {
	target, err := resolveSessionQuery(dir, query)
	if err == nil {
		return target, 0
	}
	var ambiguous *ambiguousSessionQueryError
	if errors.As(err, &ambiguous) && choose != nil {
		return choose(ambiguous.matches)
	}
	fmt.Fprintln(w, i18n.M.ErrorPrefix, err)
	if ambiguous != nil {
		for _, entry := range ambiguous.matches {
			fmt.Fprintf(w, "  %s  %s  %s\n", entry.session.UpdatedAt.Local().Format("01-02 15:04"),
				resumeEntryID(entry), sessionSummary(entry.session))
		}
		fmt.Fprintln(w, i18n.M.AmbiguousResumeHint)
	}
	return cliResumeTarget{}, 1
}

// chooseResumeEntry shows entries in the terminal menu and returns the pick.
// The caller has already checked isInteractive.
func chooseResumeEntry(entries []resumeEntry) (cliResumeTarget, int) {
	items := make([]menuItem, len(entries))
	for i, s := range entries {
		when := s.session.UpdatedAt.Local().Format("01-02 15:04")
		items[i] = menuItem{
			name: when,
			desc: sessionSummary(s.session),
		}
	}
	idx, err := selectOne(i18n.M.PickSessionLabel, items)
	if err != nil {
		return cliResumeTarget{}, 1
	}
	return entries[idx].target, 0
}

// resumeEntryID is the identity --resume matches exactly for entry.
func resumeEntryID(entry resumeEntry) string {
	return entry.target.ref.SessionID
}

// normalizedResumeFlag maps the bare-flag spellings of --resume onto the
// values the resolver understands. The result also decides the telemetry
// session mode, so it is computed once and shared.
func normalizedResumeFlag(resume string) string {
	value := strings.TrimSpace(resume)
	switch strings.ToLower(value) {
	case "true":
		return resumePickerSentinel
	case "false":
		return ""
	}
	return value
}

// continueResumeTarget implements --continue: sweep the recovery branches this
// workspace left behind, then take the newest conversation.
func continueResumeTarget() (cliResumeTarget, int) {
	sessionDir := resolveCLISessionDir()

	target, ok := newestResumeTarget(sessionDir)
	if !ok {
		fmt.Fprintln(os.Stderr, i18n.M.NoSessionToResume)
		return cliResumeTarget{}, 1
	}
	return target, 0
}

// copySessionExitCode requires an explicit source for a full-history copy.
func copySessionExitCode(copySession bool, target cliResumeTarget) int {
	if !copySession || !target.empty() {
		return 0
	}
	fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "--copy requires --resume or --continue")
	return 2
}

// commitStartupResume opens the chosen identity; takeover requires the caller's
// explicit flag or interactive answer after writer ownership refuses the open.
func commitStartupResume(manager *cliTakeoverManager, ctrl *control.Controller, target cliResumeTarget, approve func(error) bool) error {
	if target.empty() {
		return nil
	}
	service := resumeTargetService(target)
	_, err := ctrl.OpenSessionFromService(context.Background(), service, target.ref)
	if err == nil || !errors.Is(err, session.ErrWriterOwned) || !approve(err) {
		return err
	}
	return cliStartupCanonicalTakeover(ctrl, manager, target)
}

// flagTakeoverApproval answers for --takeover, which commits before the
// conflict is known.
func flagTakeoverApproval(enabled bool) func(error) bool {
	return func(error) bool { return enabled }
}

// promptTakeoverApproval asks on the terminal; a non-interactive start declines.
func promptTakeoverApproval(_ error) bool {
	return isInteractive() && promptSessionTakeover()
}
