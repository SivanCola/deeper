package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deepzora/internal/filelock"
	"deepzora/internal/fileutil"
	"deepzora/internal/permission"
	"deepzora/internal/workspaceid"
)

// ProjectGrant is what the user granted one workspace folder: allow rules
// answered "always" and extra writable directories. It lives under the user's
// home, keyed by the folder's own path: a grant has no digest to bind it, so it
// must not follow a repository identity a checkout's .git file could claim.
type ProjectGrant struct {
	Allow      []string `json:"allow,omitempty"`
	AllowWrite []string `json:"allow_write,omitempty"`
}

type projectGrantFile struct {
	Version    int                     `json:"version"`
	Workspaces map[string]ProjectGrant `json:"workspaces"`
}

const (
	projectGrantsFilename = "project-grants.json"
	projectGrantsLockFile = ".project-grants.lock"
	projectGrantsVersion  = 1
)

// ErrProjectGrantsUnavailable is a record that could not be read or written.
var ErrProjectGrantsUnavailable = errors.New("workspace grants unavailable")

// ProjectGrantStore persists grants in <Deepzora home>/project-grants.json.
type ProjectGrantStore struct {
	path string
}

// NewProjectGrantStore opens the record under home.
func NewProjectGrantStore(home string) *ProjectGrantStore {
	if strings.TrimSpace(home) == "" {
		return &ProjectGrantStore{}
	}
	return &ProjectGrantStore{path: filepath.Join(home, projectGrantsFilename)}
}

// Path is the file the grants are written to.
func (s *ProjectGrantStore) Path() string { return s.path }

// Grant returns what root was granted.
func (s *ProjectGrantStore) Grant(root string) (ProjectGrant, error) {
	file, err := s.load()
	if err != nil {
		return ProjectGrant{}, err
	}
	return file.Workspaces[workspaceid.PathFingerprint(root)], nil
}

// Update replaces root's grant with what edit returns, under a cross-process lock.
func (s *ProjectGrantStore) Update(root string, edit func(ProjectGrant) (ProjectGrant, error)) error {
	ws := workspaceid.PathFingerprint(root)
	if s.path == "" || ws == "" {
		return fmt.Errorf("%w: no Deepzora home or workspace", ErrProjectGrantsUnavailable)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("%w: %w", ErrProjectGrantsUnavailable, err)
	}
	// Local writers are queued, not competing with an external lock holder.
	// Start the external contention budget only after the local queue drains;
	// otherwise a burst of legitimate updates can expire behind slow fsyncs.
	unlock, err := filelock.AcquireWithExternalTimeout(context.Background(), filepath.Join(filepath.Dir(s.path), projectGrantsLockFile), 5*time.Second)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProjectGrantsUnavailable, err)
	}
	defer unlock()
	file, err := s.load()
	if err != nil {
		return err
	}
	current := file.Workspaces[ws]
	next, err := edit(ProjectGrant{Allow: slices.Clone(current.Allow), AllowWrite: slices.Clone(current.AllowWrite)})
	if err != nil {
		return err
	}
	for _, rule := range next.Allow {
		if _, ok := permission.ParseRule(rule); !ok {
			return fmt.Errorf("invalid permission rule %q (want \"ToolName\" or \"ToolName(glob)\")", rule)
		}
	}
	file.Workspaces[ws] = next
	file.Version = projectGrantsVersion
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProjectGrantsUnavailable, err)
	}
	if err := fileutil.AtomicWriteFile(s.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("%w: %w", ErrProjectGrantsUnavailable, err)
	}
	return nil
}

func (s *ProjectGrantStore) load() (projectGrantFile, error) {
	file := projectGrantFile{Version: projectGrantsVersion, Workspaces: map[string]ProjectGrant{}}
	if s.path == "" {
		return file, nil
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return file, nil
	}
	if err != nil {
		return file, fmt.Errorf("%w: %w", ErrProjectGrantsUnavailable, err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, fmt.Errorf("%w: %s: %w", ErrProjectGrantsUnavailable, s.path, err)
	}
	if file.Workspaces == nil {
		file.Workspaces = map[string]ProjectGrant{}
	}
	return file, nil
}
