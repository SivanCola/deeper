package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/session"
)

func ambiguousResumeWorkspace(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("DEEPZORA_STATE_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	dir := resolveCLISessionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "alpha", "demo-alpha rollout")
	createCanonicalTestSession(t, session.RootForSessionDir(dir), "beta", "demo-beta rollout")
	waitForCatalogMetadata(t, dir, "alpha", "beta")
	alpha, beta := cliCanonicalRoute("alpha"), cliCanonicalRoute("beta")
	return alpha, beta
}

func TestHeadlessResumeAmbiguousQueryListsCandidates(t *testing.T) {
	ambiguousResumeWorkspace(t)

	var rc int
	out := captureStderr(t, func() {
		_, rc = headlessResumeTarget("demo", false, false)
	})
	if rc != 1 {
		t.Fatalf("exit code = %d, want 1", rc)
	}
	for _, want := range []string{
		"alpha", "demo-alpha rollout",
		"beta", "demo-beta rollout",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stderr does not name candidate %q:\n%s", want, out)
		}
	}
}

func TestInteractiveResumeAmbiguousQueryOffersOnlyMatches(t *testing.T) {
	alpha, beta := ambiguousResumeWorkspace(t)
	createCanonicalTestSession(t, session.RootForSessionDir(resolveCLISessionDir()), "gamma", "unrelated work")
	waitForCatalogMetadata(t, resolveCLISessionDir(), "gamma")

	var offered []string
	choose := func(entries []resumeEntry) (cliResumeTarget, int) {
		for _, entry := range entries {
			offered = append(offered, cliCanonicalRoute(entry.target.ref.SessionID))
		}
		return entries[len(entries)-1].target, 0
	}
	target, rc := resumeQueryTarget(resolveCLISessionDir(), "demo", os.Stderr, choose)
	if rc != 0 {
		t.Fatalf("exit code = %d, want the chooser's pick", rc)
	}
	if len(offered) != 2 || !containsPath(offered, alpha) || !containsPath(offered, beta) {
		t.Fatalf("chooser offered %v, want exactly %s and %s", offered, alpha, beta)
	}
	if cliCanonicalRoute(target.ref.SessionID) != offered[1] {
		t.Fatalf("target = %+v, want the chosen %s", target, offered[1])
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(want) {
			return true
		}
	}
	return false
}
