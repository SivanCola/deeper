package main

import (
	"deepzora/internal/config"
	"testing"
)

func targetModelForFixture(app *App, tab *WorkspaceTab) string {
	cfg, err := config.LoadForRoot(tab.WorkspaceRoot)
	if err != nil {
		panic(err)
	}
	for _, entry := range cfg.Providers {
		for _, id := range entry.ModelList() {
			ref := entry.Name + "/" + id
			if ref != tab.model {
				return ref
			}
		}
	}
	panic("fixture has no alternate model")
}

var _ *testing.T
