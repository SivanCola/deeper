package cli

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"deepzora/internal/session"
)

// resumeEntryIndex returns the 1-based /resume index of the row matching the
// predicate, or 0 when absent.
func resumeEntryIndex(entries []resumeEntry, match func(resumeEntry) bool) int {
	for i, entry := range entries {
		if match(entry) {
			return i + 1
		}
	}
	return 0
}

func assertMirrorLeft(t *testing.T, m *chatTUI, fake *fakeCanonicalServe, route string) {
	t.Helper()
	if binding, _, _, _ := m.takeover.snapshot(); binding != nil {
		t.Fatalf("mirror binding still active after leaving %q: %+v", route, binding)
	}
	if ends := fake.mirrorEnds(); len(ends) != 1 || ends[0] != route {
		t.Fatalf("mirror-end requests = %v, want exactly one for %q", ends, route)
	}
	if m.takeover.Returned() {
		t.Fatal("switching away from a mirror left the manager in returned state")
	}
}

// TestResumeCanonicalSessionReturnsActiveCanonicalMirror covers /resume to a
// final-format session while this CLI mirrors another canonical identity for
// the desktop: the mirror must end so the desktop tab regains its writer and
// stops receiving the next session's frames under the old mirror id.
func TestResumeCanonicalSessionReturnsActiveCanonicalMirror(t *testing.T) {
	route := cliCanonicalRoute("held")
	fake := newFakeCanonicalServe(t, route)
	withFakeCanonicalDiscovery(t, fake.base)
	m, ctrl, _, held := newCanonicalTakeoverTUI(t)
	m.runCanonicalTakeoverCommand(route)
	if ref, bound := ctrl.SessionRef(); !bound || ref != held {
		t.Fatalf("takeover did not attach: %+v bound=%v", ref, bound)
	}
	createCanonicalTestSession(t, session.RootForSessionDir(ctrl.SessionDir()), "third", "third conversation")

	idx := resumeEntryIndex(resumeEntries(ctrl.SessionDir()), func(entry resumeEntry) bool {
		return entry.target.canonical() && entry.target.ref.SessionID == "third"
	})
	if idx == 0 {
		t.Fatal("third canonical session missing from /resume list")
	}
	m.runResumeCommand("/resume " + strconv.Itoa(idx))

	if ref, bound := ctrl.SessionRef(); !bound || ref.SessionID != "third" {
		t.Fatalf("controller after /resume = %+v bound=%v, want third", ref, bound)
	}
	assertMirrorLeft(t, m, fake, route)
}

// TestCanonicalTakeoverOfSecondIdentityReturnsFirstMirror covers /takeover of
// a second final-format identity while the first is still mirrored: Activate
// must not overwrite the live binding without ending the first mirror.
func TestCanonicalTakeoverOfSecondIdentityReturnsFirstMirror(t *testing.T) {
	routeA, routeB := cliCanonicalRoute("held"), cliCanonicalRoute("second")
	fake := newFakeCanonicalServeRoutes(t, routeA, routeB)
	withFakeCanonicalDiscovery(t, fake.base)
	m, ctrl, service, held := newCanonicalTakeoverTUI(t)
	second, err := service.Create(t.Context(), session.CreateOptions{SessionID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), second.Ref()); err != nil {
		t.Fatal(err)
	}
	m.runCanonicalTakeoverCommand(routeA)
	if ref, bound := ctrl.SessionRef(); !bound || ref != held {
		t.Fatalf("first takeover did not attach: %+v bound=%v", ref, bound)
	}

	m.runCanonicalTakeoverCommand(routeB)

	if ref, bound := ctrl.SessionRef(); !bound || ref != second.Ref() {
		t.Fatalf("controller after second takeover = %+v bound=%v, want second", ref, bound)
	}
	binding, _, _, _ := m.takeover.snapshot()
	if binding == nil || binding.path != routeB {
		t.Fatalf("mirror binding = %+v, want the second identity %q", binding, routeB)
	}
	if ends := fake.mirrorEnds(); len(ends) != 1 || ends[0] != routeA {
		t.Fatalf("mirror-end requests = %v, want exactly one for the first identity %q", ends, routeA)
	}
	if fake.handoffCount() != 2 {
		t.Fatalf("handoff requests = %d, want one per takeover", fake.handoffCount())
	}
}

// TestResumeCanonicalSessionHeldElsewhereKeepsMirror pins failure atomicity of
// the canonical switch: when the target's writer belongs to another runtime,
// the current mirror, controller binding, and lease stay exactly as they were.
func TestResumeCanonicalSessionHeldElsewhereKeepsMirror(t *testing.T) {
	route := cliCanonicalRoute("held")
	fake := newFakeCanonicalServe(t, route)
	withFakeCanonicalDiscovery(t, fake.base)
	m, ctrl, _, held := newCanonicalTakeoverTUI(t)
	m.runCanonicalTakeoverCommand(route)
	other, err := session.NewService("local", session.NewFilesystemPersistence(session.RootForSessionDir(ctrl.SessionDir())))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Shutdown(context.Background()) })
	busy, err := other.Create(t.Context(), session.CreateOptions{SessionID: "busy"})
	if err != nil {
		t.Fatal(err)
	}

	err = m.commitResumeTarget(cliResumeTarget{ref: session.SessionRef{HostID: busy.Ref().HostID, SessionID: "busy"}})

	if !errors.Is(err, session.ErrWriterOwned) {
		t.Fatalf("switch to a held session returned %v, want ErrWriterOwned", err)
	}
	if ref, bound := ctrl.SessionRef(); !bound || ref != held {
		t.Fatalf("controller moved to %+v (bound %v) despite the refused switch", ref, bound)
	}
	binding, _, _, _ := m.takeover.snapshot()
	if binding == nil || binding.path != route {
		t.Fatalf("mirror binding = %+v after a refused switch, want %q still active", binding, route)
	}
	if ends := fake.mirrorEnds(); len(ends) != 0 {
		t.Fatalf("mirror-end requests = %v after a refused switch, want none", ends)
	}
}
