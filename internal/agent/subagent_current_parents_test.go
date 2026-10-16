package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

type testParentIdentity struct {
	parent string
	err    error
}

var testParentCatalogs = struct {
	sync.Mutex
	entries map[string]map[string]testParentIdentity
}{entries: map[string]map[string]testParentIdentity{}}

func newTestSubagentStore(t *testing.T, dir string) *SubagentStore {
	t.Helper()
	root := filepath.Dir(dir)
	if filepath.Base(dir) != "subagents" {
		root = dir
	}
	testParentCatalogs.Lock()
	if testParentCatalogs.entries[root] == nil {
		testParentCatalogs.entries[root] = map[string]testParentIdentity{}
	}
	testParentCatalogs.Unlock()
	t.Cleanup(func() {
		testParentCatalogs.Lock()
		delete(testParentCatalogs.entries, root)
		testParentCatalogs.Unlock()
	})
	return NewSubagentStore(&testSubagentPersistence{dir: dir, records: map[string]*testSubagentTranscript{}}).WithParentCatalog(func(_ context.Context, id string) (string, error) {
		testParentCatalogs.Lock()
		defer testParentCatalogs.Unlock()
		info, ok := testParentCatalogs.entries[root][id]
		if !ok {
			return "", fmt.Errorf("current session %q not found", id)
		}
		return info.parent, info.err
	}).WithParentIdleGuard(func(_ context.Context, _ string, work func() error) (bool, error) {
		return true, work()
	})
}
func saveTestParentSession(t *testing.T, root, id, parent string) {
	t.Helper()
	testParentCatalogs.Lock()
	defer testParentCatalogs.Unlock()
	if testParentCatalogs.entries[root] == nil {
		testParentCatalogs.entries[root] = map[string]testParentIdentity{}
	}
	testParentCatalogs.entries[root][id] = testParentIdentity{parent: parent}
}
func setTestParentError(t *testing.T, root, id string, err error) {
	t.Helper()
	testParentCatalogs.Lock()
	defer testParentCatalogs.Unlock()
	testParentCatalogs.entries[root][id] = testParentIdentity{err: err}
}
func rewriteTestParentHeader(t *testing.T, root, id, field, value string) {
	t.Helper()
	if field == "parentSessionId" {
		saveTestParentSession(t, root, id, value)
		return
	}
	setTestParentError(t, root, id, fmt.Errorf("current header for %q declares identity %q", id, value))
}
