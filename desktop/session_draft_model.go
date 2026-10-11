package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"deepzora/desktop/internal/draftstate"
	"deepzora/internal/config"
	"deepzora/internal/extension/providerext"
)

func draftSubmissionFingerprint(request SessionDraftSubmissionRequest) (string, string, error) {
	request.DraftID = strings.TrimSpace(request.DraftID)
	request.Display = strings.TrimSpace(request.Display)
	request.Input = strings.TrimSpace(request.Input)
	if request.Kind == "" {
		request.Kind = "turn"
	}
	data, err := json.Marshal(request)
	if err != nil {
		return "", "", err
	}
	// Request identity is transport metadata; equivalent concurrent requests
	// must still converge on the same operation.
	request.RequestID = ""
	request.SourceContentJSON = ""
	if request.Settings.ModelSource == draftModelSourceDefault {
		request.Settings.Model = ""
	}
	semantic, err := json.Marshal(request)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(semantic)
	return hex.EncodeToString(digest[:]), string(data), nil
}

const (
	draftModelSourceDefault  = "default"
	draftModelSourceExplicit = "explicit"
)

func (a *App) draftViewForOperation(record draftstate.Draft, op *draftstate.Operation) (SessionDraftView, error) {
	view, err := a.draftView(record)
	if err != nil || op == nil || op.Phase == "cancelled" || op.Phase == "terminal_failed" {
		return view, err
	}
	// A resumable submission owns a frozen model. The editor and picker must
	// show the same model that Continue will run, even after a default change.
	var request SessionDraftSubmissionRequest
	if json.Unmarshal([]byte(op.RequestJSON), &request) == nil && request.SnapshotVersion == draftstate.SnapshotVersion && request.Settings.Model != "" {
		view.Settings.Model = request.Settings.Model
	}
	return view, nil
}

func draftWorkspaceRoot(record draftstate.Draft) string {
	if record.Scope == "project" {
		return record.WorkspaceRoot
	}
	return globalWorkspaceRoot()
}

func (a *App) normalizeDraftSettingsForStorage(record draftstate.Draft, settings SessionDraftSettings) SessionDraftSettings {
	if settings.ModelSource == draftModelSourceDefault {
		settings.Model = ""
	}
	if settings.DisabledMCP == nil {
		settings.DisabledMCP = map[string]ServerView{}
	}
	if settings.MCPOrder == nil {
		settings.MCPOrder = []string{}
	}
	return settings
}

func (a *App) freezeDraftSubmissionModel(record draftstate.Draft, view SessionDraftView, request SessionDraftSubmissionRequest) (SessionDraftSubmissionRequest, error) {
	if request.Settings.ModelSource == draftModelSourceDefault {
		request.Settings.Model = view.Settings.Model
	}
	if providerext.PluginRefOwner(request.Settings.Model) != "" {
		return request, nil
	}
	cfg, err := config.LoadForRootReadOnly(draftWorkspaceRoot(record))
	if err != nil {
		return request, err
	}
	request.Settings.Model, err = resolveDraftCreateModelStrict(cfg, request.Settings.Model)
	return request, err
}
