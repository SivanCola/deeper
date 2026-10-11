//go:build windows

package main

import (
	"deepzora/desktop/internal/instanceidentity"
	"deepzora/internal/config"
	"os"
)

func startUpdateEndpoint() (func(), error) {
	if !hostRPCRequested(os.Args[1:]) {
		return func() {}, nil
	}
	return instanceidentity.ListenEndpoint(instanceidentity.ForHome(config.DeepzoraHomeDir()))
}
