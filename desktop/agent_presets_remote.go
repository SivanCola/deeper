package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
)

const serveCapabilityAgentPresetV1 = "agent-preset-v1"

type remoteAgentPresetState struct {
	route   string
	current string
	locked  bool
}

func remoteAgentPresetID(tab *remoteTab) string {
	if tab.agentPreset.route == tab.routing.currentPath {
		return agentpreset.NormalizeID(tab.agentPreset.current)
	}
	return agentpreset.DefaultID
}

// remoteAgentPresetAdmissionError reads only fields protected by remoteTabMu.
func remoteAgentPresetAdmissionError(tabID, method string, tab *remoteTab) error {
	if tab == nil || !tab.capabilities[serveCapabilityAgentPresetV1] {
		return fmt.Errorf("this remote Deepzora Serve does not support %s; upgrade it to manage agent presets", serveCapabilityAgentPresetV1)
	}
	executionSupported := tab.capabilities[serveCapabilityExecutionV2] && tab.capabilities[serveCapabilitySessions] && tab.capabilities[serveCapabilitySessionIdentityV1] && tab.capabilities[serveCapabilitySessionOwnershipV1]
	if method != http.MethodGet && !executionSupported {
		return fmt.Errorf("this remote Deepzora Serve is read-only because it does not support %s, %s, %s, and %s; upgrade the remote service to execute or control a session", serveCapabilityExecutionV2, serveCapabilitySessions, serveCapabilitySessionIdentityV1, serveCapabilitySessionOwnershipV1)
	}
	if tab.routing.rehydratingPath != "" {
		return fmt.Errorf("remote tab %q is switching sessions; wait for it to become ready", tabID)
	}
	if tab.client == nil || tab.state != "ready" {
		return fmt.Errorf("remote tab %q is not connected", tabID)
	}
	return nil
}

func (a *App) remoteAgentPresetRequest(tabID, method, path string, body any, out any) error {
	a.remoteTabMu.Lock()
	tab := a.remoteTabs[tabID]
	if err := remoteAgentPresetAdmissionError(tabID, method, tab); err != nil {
		a.remoteTabMu.Unlock()
		return err
	}
	generation, revision, selection := tab.gen, tab.routing.pathRevision, tab.selectionRevision
	client, base, expectedPath := tab.client, tab.base, tab.routing.currentPath
	a.remoteTabMu.Unlock()
	// Capability admission and the request target belong to the same snapshot.
	// Resolving the tab again here could send this mutation to a newer session.
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(a)
	defer cancel()
	endpoint := serveURL(base, path)
	response, err := serveDoForSession(ctx, client, method, endpoint, payload, expectedPath)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, serveSnapshotMaxBytes+1))
	if err != nil {
		return err
	}
	if len(data) > serveSnapshotMaxBytes {
		return fmt.Errorf("agent preset response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &serveHTTPStatusError{url: endpoint, statusCode: response.StatusCode, message: strings.TrimSpace(string(data))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return err
		}
	}
	a.remoteTabMu.Lock()
	defer a.remoteTabMu.Unlock()
	current := a.remoteTabs[tabID]
	if current != tab || current.client != client || current.base != base || current.gen != generation ||
		current.routing.pathRevision != revision || current.selectionRevision != selection ||
		current.routing.currentPath != expectedPath || current.routing.rehydratingPath != "" || current.state != "ready" {
		return fmt.Errorf("remote session changed while managing agent presets")
	}
	// Cache the result under the same fence that accepts it; navigation may start
	// immediately after this lock is released.
	if list, ok := out.(*control.AgentPresetList); ok {
		current.agentPreset = remoteAgentPresetState{route: expectedPath, current: list.Current, locked: list.Locked}
	}
	return nil
}

func (a *App) remoteAgentPresets(tabID string) (control.AgentPresetList, error) {
	var list control.AgentPresetList
	err := a.remoteAgentPresetRequest(tabID, http.MethodGet, "/agent-presets", nil, &list)
	return list, err
}

func (a *App) saveRemoteAgentPreset(tabID, scope, id, content string) (agentpreset.Preset, error) {
	var saved agentpreset.Preset
	if err := agentpreset.ValidID(id); err != nil {
		return saved, err
	}
	err := a.remoteAgentPresetRequest(tabID, http.MethodPut, "/agent-presets/"+url.PathEscape(id), map[string]string{"scope": scope, "content": content}, &saved)
	return saved, err
}

func (a *App) deleteRemoteAgentPreset(tabID, scope, id string) error {
	if err := agentpreset.ValidID(id); err != nil {
		return err
	}
	return a.remoteAgentPresetRequest(tabID, http.MethodDelete, "/agent-presets/"+url.PathEscape(id), map[string]string{"scope": scope}, nil)
}

func (a *App) setRemoteAgentPreset(tabID, id string) error {
	if err := a.remoteAgentPresetRequest(tabID, http.MethodPost, "/agent-preset", map[string]string{"agentPreset": id}, nil); err != nil {
		return err
	}
	_, err := a.remoteAgentPresets(tabID)
	if err == nil {
		a.emitReady(a.ctx, tabID)
	}
	return err
}
