package main

import (
	"errors"
	"path/filepath"
	"strings"

	"deepzora/internal/pathidentity"
)

func cleanDesktopPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func sameDesktopPath(a, b string) bool {
	same, err := sameDesktopPathStrict(a, b)
	return err == nil && same
}

func sameDesktopPathStrict(a, b string) (bool, error) {
	return newDesktopPathMatcher().sameStrict(a, b)
}

// desktopPathMatcher resolves each distinct path once for the pass that owns
// it. A pass comparing many roots against each other shares one matcher.
type desktopPathMatcher struct{ matcher *pathidentity.Matcher }

func newDesktopPathMatcher() desktopPathMatcher {
	return desktopPathMatcher{pathidentity.NewMatcher(pathidentity.Options{FollowLeaf: true}, nil)}
}

func (m desktopPathMatcher) same(a, b string) bool {
	same, err := m.sameStrict(a, b)
	return err == nil && same
}

func (m desktopPathMatcher) sameStrict(a, b string) (bool, error) {
	a, b = cleanDesktopPath(a), cleanDesktopPath(b)
	if a == "" || b == "" {
		return false, &pathidentity.Error{Kind: pathidentity.ErrorInvalid, Stage: "input", Err: errors.New("desktop path is empty")}
	}
	return m.matcher.Same(a, b)
}

func projectRootKey(root string) string {
	root = cleanDesktopPath(root)
	if root == "" {
		return ""
	}
	identity, err := pathidentity.Resolve(root, pathidentity.Options{FollowLeaf: true})
	if err != nil {
		return ""
	}
	return identity.Key
}
