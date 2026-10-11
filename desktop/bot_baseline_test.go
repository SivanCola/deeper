package main

import (
	"deepzora/internal/config"
	"testing"
)

func TestBotPanelsShareCurrentConnectionWithoutOverwritingGenericEdits(t *testing.T) {
	initial := config.BotConfig{Connections: []config.BotConnectionConfig{{ID: "ding", Provider: "dingtalk", Credential: config.BotConnectionCredential{AppID: "before"}, Model: "before", SessionMappings: []config.BotConnectionSessionMapping{{RemoteID: "chat", SessionID: "current"}}}}}
	generic := botConfigWithConnectionViews(initial)
	generic.Connections = append([]config.BotConnectionConfig(nil), initial.Connections...)
	generic.Connections[0].Model = "generic-edit"
	got := updateDedicatedBotConnections(generic, initial)
	if got[0].Model != "generic-edit" {
		t.Fatalf("generic edit lost: %+v", got)
	}
	panel := botConfigWithConnectionViews(initial)
	panel.Dingtalk.ClientID = "panel-edit"
	got = updateDedicatedBotConnections(panel, initial)
	if got[0].Credential.AppID != "panel-edit" || len(got[0].SessionMappings) != 1 || got[0].SessionMappings[0].SessionID != "current" {
		t.Fatalf("panel edit lost binding: %+v", got)
	}
}
