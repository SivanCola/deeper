//go:build darwin

package workspacelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinCaseAliasesShareCanonicalWorkspaceLock(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "MixedCaseRepo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "mixedcaserepo")
	actualIdentity, err := CanonicalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	aliasIdentity, err := CanonicalWorkspace(alias)
	if err != nil {
		t.Fatal(err)
	}
	if actualIdentity != aliasIdentity {
		t.Fatalf("workspace identities = %q / %q, want one folded identity", actualIdentity, aliasIdentity)
	}

	locks := t.TempDir()

	first, err := New(root, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(alias, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstRelease, err := first.HoldWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer firstRelease()
	aliasCtx, aliasCancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer aliasCancel()
	aliasRelease, err := second.HoldWrite(aliasCtx)
	if aliasRelease != nil {
		aliasRelease()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("case-alias owner bypassed folded workspace lock: %v", err)
	}
}

func TestDarwinParentPathHonorsCanonicalNestedWorkspace(t *testing.T) {
	parent, locks := t.TempDir(), t.TempDir()
	nested := filepath.Join(parent, "NestedRepo")
	if err := os.MkdirAll(filepath.Join(nested, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	nestedOwner, err := New(nested, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	nestedRelease, err := nestedOwner.HoldWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer nestedRelease()

	owner, err := New(parent, locks, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	release, err := owner.HoldWriteForPath(ctx, filepath.Join(nested, "new.go"))
	if release != nil {
		release()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("parent path bypassed nested workspace hold: %v", err)
	}
}
