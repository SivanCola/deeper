// history-fixture creates current sessions and holds their real writer locks
// until stdin closes. It is a packaging-test process, never a Desktop RPC.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/config"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (resultErr error) {
	if os.Getenv("DEEPZORA_HOME") == "" || os.Getenv("DEEPZORA_STATE_HOME") == "" {
		return fmt.Errorf("fixture requires isolated homes")
	}
	ctx := context.Background()
	root := config.DesktopSessionStoreDir()
	service, err := session.NewService("local", session.NewFilesystemPersistence(root))
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, service.Shutdown(ctx)) }()
	workspace := filepath.Join(config.DeepzoraHomeDir(), "global-workspace")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		return err
	}
	registry := workspacestate.NewStore(config.DesktopWorkspaceStatePath())
	if err := registry.EnsureWorkspace(ctx, workspacestate.Workspace{ID: workspacestate.GlobalWorkspaceID, Root: workspace, Visible: true, Title: "History fixtures"}); err != nil {
		return err
	}
	fixtures := []map[string]string{}
	for _, kind := range []string{"alpha", "beta"} {
		id := "history-" + kind
		runtime, err := service.Create(ctx, session.CreateOptions{SessionID: id, CWD: workspace})
		if err != nil {
			return err
		}
		for i := range 140 {
			for _, role := range []provider.Role{provider.RoleUser, provider.RoleAssistant} {
				text := fmt.Sprintf("CURRENT_%s_%s_%d", kind, strings.ToUpper(string(role)), i)
				if role == provider.RoleAssistant && i == 139 {
					text = strings.Repeat("完整内容🧭", 90000)
				}
				messageID := fmt.Sprintf("%s-%s-%d", id, role, i)
				payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: messageID, Role: role, Content: text}})
				if err != nil {
					return err
				}
				if _, err := runtime.Session().Append(ctx, session.Batch{OperationID: messageID, Events: []session.Event{{Kind: "message/complete", Payload: payload}}}); err != nil {
					return err
				}
			}
		}
		if _, err := runtime.Session().Flush(ctx); err != nil {
			return err
		}
		if err := registry.AttachSession(ctx, "", workspacestate.GlobalWorkspaceID, id, ""); err != nil {
			return err
		}
		if err := registry.EnsureSessionTopic(ctx, id, id, kind); err != nil {
			return err
		}
		fixtures = append(fixtures, map[string]string{"kind": kind, "id": id, "path": filepath.Join(root, id)})
	}
	if err := json.NewEncoder(os.Stdout).Encode(fixtures); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, os.Stdin)
	return err
}
