package main

import (
	"net/http"
	"testing"
)

func TestRemoteToolRecoveryRejectsOtherSessionSnapshot(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return remoteRuntimeTestResponse(req, 200, `{"sessionPath":"/different","runtimeEpoch":"epoch","revision":"revision","calls":[]}`), nil
	})}
	a, tab := remoteRuntimeTestApp(client)
	if _, err := a.GetToolRecoveryForTab(tab.id); err == nil {
		t.Fatal("another session's snapshot was accepted")
	}
}
