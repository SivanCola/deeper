package cli

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/event"
	"deepzora/internal/fileutil"
	filelock "deepzora/internal/identitylock"
	"deepzora/internal/session"
)

const (
	machineSchemaVersion    = 1
	machineIdentityKeyBytes = 32
	machineIdentityKeyFile  = "machine-id.key"
)

type machineSession struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Scope     string `json:"scope"`
	Turns     int    `json:"turns"`
	State     string `json:"state"`
}

type machineSessionList struct {
	SchemaVersion int              `json:"schema_version"`
	Command       string           `json:"command"`
	Sessions      []machineSession `json:"sessions"`
}

type machineSessionShow struct {
	SchemaVersion int            `json:"schema_version"`
	Command       string         `json:"command"`
	Session       machineSession `json:"session"`
}

type machineError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type machineErrorResponse struct {
	SchemaVersion int          `json:"schema_version"`
	Command       string       `json:"command"`
	Error         machineError `json:"error"`
}

type sessionMachineOptions struct {
	dir         string
	projectRoot string
	target      string
	json        bool
}

func sessionCommand(args []string) int {
	return runSessionCommand(args, os.Stdout)
}

func runSessionCommand(args []string, out io.Writer) int {
	command := "session"
	if len(args) == 0 {
		return writeMachineError(out, command, "invalid_argument", "a session operation is required")
	}
	operation := args[0]
	command = "session." + operation
	if operation != "list" && operation != "show" && operation != "status" {
		return writeMachineError(out, command, "unknown_command", fmt.Sprintf("unknown session operation %q; expected list, show or status", operation))
	}
	options, code, message := parseSessionMachineOptions(args[1:], operation)
	if code != "" {
		return writeMachineError(out, command, code, message)
	}
	if !options.json {
		return writeMachineError(out, command, "invalid_argument", "--json is required")
	}
	options.dir = resolveMachineSessionDir(options.dir, options.projectRoot)
	identityKey, err := loadMachineIdentityKey()
	if err != nil {
		return writeMachineError(out, command, "machine_identity_unavailable", "machine identity is unavailable")
	}
	sessions, err := machineSessions(options.dir, identityKey)
	if err != nil {
		return writeMachineError(out, command, "session_dir_unavailable", "session directory is unavailable")
	}
	if operation == "list" {
		return writeMachineJSON(out, machineSessionList{
			SchemaVersion: machineSchemaVersion,
			Command:       command,
			Sessions:      sessions,
		})
	}
	for _, session := range sessions {
		if session.ID != options.target {
			continue
		}
		return writeMachineJSON(out, machineSessionShow{
			SchemaVersion: machineSchemaVersion,
			Command:       command,
			Session:       session,
		})
	}
	return writeMachineError(out, command, "session_not_found", "session was not found")
}

func resolveMachineSessionDir(sessionDir, projectRoot string) string {
	if projectRoot != "" {
		return machineProjectSessionDir(projectRoot)
	}
	if sessionDir != "" {
		return sessionDir
	}
	return resolveCLISessionDir()
}

// machineProjectSessionDir maps a project root to its per-project session store.
func machineProjectSessionDir(projectRoot string) string {
	if dir := config.ProjectSessionDir(projectRoot); dir != "" {
		return dir
	}
	return projectRoot
}

func parseSessionMachineOptions(args []string, operation string) (sessionMachineOptions, string, string) {
	var options sessionMachineOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			options.json = true
		case "--dir":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return options, "invalid_argument", "--dir requires a value"
			}
			i++
			options.dir = args[i]
		case "--project-root":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return options, "invalid_argument", "--project-root requires a value"
			}
			i++
			options.projectRoot = args[i]
		case "--help", "-h":
			return options, "invalid_argument", "use the documented machine interface"
		default:
			arg := strings.TrimSpace(args[i])
			if strings.HasPrefix(arg, "-") {
				return options, "invalid_argument", "unknown session option"
			}
			if operation == "list" || options.target != "" || strings.ContainsAny(arg, `/\\`) {
				return options, "invalid_argument", "invalid session identifier"
			}
			options.target = arg
		}
	}
	if options.dir != "" && options.projectRoot != "" {
		return options, "invalid_argument", "--dir and --project-root cannot be combined"
	}
	if operation != "list" && options.target == "" {
		return options, "invalid_argument", "a session identifier is required"
	}
	return options, "", ""
}

func machineSessions(dir string, identityKey []byte) ([]machineSession, error) {
	ordered, err := machineSessionCatalog(context.Background(), dir)
	if err != nil {
		return nil, err
	}
	out := make([]machineSession, 0, len(ordered))
	for _, info := range ordered {
		if canonicalResumeHidden(info) {
			continue
		}
		state := "idle"
		if session.ProbeWriterHeld(info.Path) {
			state = "active"
		} else {
			snapshot, err := cliSessionService(dir).Query().Snapshot(context.Background(), info.Ref)
			if err != nil {
				return nil, err
			}
			if turns := snapshot.Projection.Turns; len(turns) > 0 {
				last := turns[len(turns)-1]
				if last.EndSequence == 0 || last.Status == event.TurnInterrupted {
					state = "interrupted"
				}
			}
		}
		scope := "global"
		if info.CWD != "" {
			scope = "project"
		}
		out = append(out, machineSession{
			ID:        machineSessionIDWithKey(info.Ref.SessionID, identityKey),
			CreatedAt: machineTime(info.CreatedAt),
			UpdatedAt: machineTime(info.UpdatedAt),
			Scope:     scope,
			Turns:     info.Turns,
			State:     state,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

// Machine interfaces enumerate every current catalog page without the resume
// picker's display cap. Errors are returned, never disguised as a partial list.
func machineSessionCatalog(ctx context.Context, dir string) ([]session.SessionInfo, error) {
	service := cliSessionService(dir)
	if service == nil {
		return nil, fmt.Errorf("session service unavailable")
	}
	var entries []session.SessionInfo
	for cursor := ""; ; {
		page, err := service.Query().List(ctx, cursor, 100)
		if err != nil {
			return nil, err
		}
		for _, info := range page.Sessions {
			if info.MetadataStatus != session.MetadataReady {
				info, err = service.Query().RefreshMetadata(ctx, info.Ref)
				if err != nil {
					return nil, err
				}
			}
			entries = append(entries, info)
		}
		if page.NextCursor == "" {
			return entries, nil
		}
		if page.NextCursor == cursor || len(page.Sessions) == 0 {
			return nil, fmt.Errorf("session catalog cursor did not advance")
		}
		cursor = page.NextCursor
	}
}

func machineTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func loadMachineIdentityKey() ([]byte, error) {
	root := strings.TrimSpace(config.MemoryUserDir())
	if root == "" {
		return nil, fmt.Errorf("machine identity: Deepzora state directory is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return loadMachineIdentityKeyAt(ctx, root)
}

func loadMachineIdentityKeyAt(ctx context.Context, root string) ([]byte, error) {
	path := filepath.Join(root, machineIdentityKeyFile)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("machine identity: create state directory: %w", err)
	}
	// Readers must join publication too: Windows can reject an unlocked read
	// while the writer or its filesystem filter still holds the destination.
	unlock, err := filelock.Acquire(ctx, path+".lock")
	if err != nil {
		return nil, fmt.Errorf("machine identity: initialize key: %w", err)
	}
	defer unlock()

	key, err := readMachineIdentityKey(path)
	if err == nil {
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key = make([]byte, machineIdentityKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("machine identity: generate key: %w", err)
	}
	if err := fileutil.AtomicWriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("machine identity: persist key: %w", err)
	}
	return key, nil
}

func readMachineIdentityKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != machineIdentityKeyBytes {
		return nil, fmt.Errorf("machine identity: invalid key length %d", len(key))
	}
	return key, nil
}

// machineSessionIDWithKey keeps the public machine contract stable without
// exposing or making offline guesses about transcript filenames, which include
// creation timestamps and configured model labels.
func machineSessionIDWithKey(branchID string, identityKey []byte) string {
	branchID = strings.TrimSpace(branchID)
	if branchID == "" || len(identityKey) != machineIdentityKeyBytes {
		return ""
	}
	digest := hmac.New(sha256.New, identityKey)
	_, _ = digest.Write([]byte("deepzora-machine-session-v1\x00"))
	_, _ = digest.Write([]byte(branchID))
	return "session_" + hex.EncodeToString(digest.Sum(nil)[:16])
}

func writeMachineJSON(out io.Writer, value any) int {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return 1
	}
	return 0
}

func writeMachineError(out io.Writer, command, code, message string) int {
	if writeMachineJSON(out, machineErrorResponse{
		SchemaVersion: machineSchemaVersion,
		Command:       command,
		Error: machineError{
			Code:    code,
			Message: message,
		},
	}) != 0 {
		return 1
	}
	if code == "invalid_argument" || code == "unknown_command" {
		return 2
	}
	return 1
}
