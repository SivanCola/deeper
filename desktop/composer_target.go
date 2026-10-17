package main

import (
	"deepzora/internal/control"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

func (a *App) composerTargetWorkspace(target ComposerTarget) (string, control.SessionAPI, error) {
	if target.Kind == "session" && target.Session != nil {
		if err := validateLocalSessionRef(*target.Session); err != nil {
			return "", nil, err
		}
		var ctrl control.SessionAPI
		if target.TabID != "" {
			a.mu.RLock()
			tab := a.tabs[target.TabID]
			matches := tab != nil && !tab.removed && tab.SessionID == target.Session.SessionID
			if matches {
				ctrl = tab.Ctrl
			}
			a.mu.RUnlock()
			if !matches {
				return "", nil, errors.New("composer target changed")
			}
		}
		info, err := a.desktopSessionService("").Query().Stat(a.bootContext(), *target.Session)
		if err != nil {
			return "", nil, err
		}
		base, err := workspaceBaseFromRoot(info.CWD)
		return base, ctrl, err
	}
	if target.Kind != "session" {
		return "", nil, errors.New("unsupported composer target")
	}
	root, ctrl, ok := a.workspaceTargetForTab(target.TabID)
	if !ok {
		return "", nil, errors.New("composer target is unavailable")
	}
	base, err := workspaceBaseFromRoot(root)
	return base, ctrl, err
}

func (a *App) SavePastedImageForComposerTarget(target ComposerTarget, dataURL string) (string, error) {
	root, _, err := a.composerTargetWorkspace(target)
	if err != nil {
		return "", err
	}
	const marker = ";base64,"
	before, after, ok := strings.Cut(dataURL, marker)
	if !ok || !strings.HasPrefix(before, "data:") {
		return "", errors.New("unsupported pasted image")
	}
	raw, err := decodeBase64(after)
	if err != nil {
		return "", err
	}
	return control.SaveImageBytesInRoot(root, strings.TrimPrefix(before, "data:"), raw)
}

func (a *App) SavePastedFileForComposerTarget(target ComposerTarget, name, dataURL string) (string, error) {
	root, _, err := a.composerTargetWorkspace(target)
	if err != nil {
		return "", err
	}
	_, after, ok := strings.Cut(dataURL, ";base64,")
	if !ok {
		return "", errors.New("unsupported pasted file")
	}
	raw, err := decodeBase64(after)
	if err != nil {
		return "", err
	}
	return control.SaveAttachmentBytesInRoot(root, name, raw)
}

func (a *App) SaveClipboardImageForComposerTarget(target ComposerTarget) (string, error) {
	root, _, err := a.composerTargetWorkspace(target)
	if err != nil {
		return "", err
	}
	return control.SaveClipboardImageInRoot(root)
}

func decodeBase64(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode attachment: %w", err)
	}
	return decoded, nil
}

func (a *App) ListDirForTarget(target ComposerTarget, rel string) []DirEntry {
	root, ctrl, err := a.composerTargetWorkspace(target)
	if err != nil {
		return []DirEntry{}
	}
	return listDirForWorkspaceTarget(root, ctrl, rel)
}

func (a *App) SearchFileRefsForTarget(target ComposerTarget, query string) []DirEntry {
	root, ctrl, err := a.composerTargetWorkspace(target)
	if err != nil {
		return []DirEntry{}
	}
	return searchFileRefsForWorkspaceTarget(root, ctrl, query)
}

func (a *App) AttachmentDataURLForComposerTarget(target ComposerTarget, rel string) (string, error) {
	root, _, err := a.composerTargetWorkspace(target)
	if err != nil {
		return "", err
	}
	return control.ImageDataURLInRoot(root, rel)
}

func (a *App) AttachDroppedForComposerTarget(target ComposerTarget, path string) (DroppedItem, error) {
	owner, err := a.attachmentTargetForComposerTarget(target)
	if err != nil {
		return DroppedItem{}, err
	}
	return a.attachDroppedForTarget(owner, path)
}
