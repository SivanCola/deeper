package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"deepzora/internal/control"
)

type toolRecoveryController interface {
	ToolRecoverySnapshot() control.ToolRecoverySnapshot
}

// Both local and remote surfaces use the same generation-bound request shape.
func (a *App) GetToolRecoveryForTab(tabID string) (control.ToolRecoverySnapshot, error) {
	if a.isRemoteTab(tabID) {
		return a.remoteToolRecovery(tabID)
	}
	_, ctrl := a.tabAndCtrlByID(tabID)
	if target, ok := ctrl.(toolRecoveryController); ok {
		return target.ToolRecoverySnapshot(), nil
	}
	return control.ToolRecoverySnapshot{}, fmt.Errorf("tool recovery unavailable")
}

func (a *App) remoteToolRecovery(tabID string) (control.ToolRecoverySnapshot, error) {
	client, base, path, err := a.remoteTabCommandTarget(tabID)
	if err != nil {
		return control.ToolRecoverySnapshot{}, err
	}
	ctx, cancel := commandContext(a)
	defer cancel()
	resp, err := serveDoForSession(ctx, client, http.MethodGet, serveURL(base, "/tool-recovery"), nil, path)
	if err != nil {
		return control.ToolRecoverySnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return control.ToolRecoverySnapshot{}, fmt.Errorf("tool recovery: %s", b)
	}
	var view control.ToolRecoverySnapshot
	err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&view)
	if err == nil && view.SessionPath != path {
		return view, fmt.Errorf("remote recovery session changed")
	}
	return view, err
}
