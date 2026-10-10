//go:build windows

package main

import (
	"deeper/desktop/internal/instanceidentity"
	"deeper/internal/config"
	"os"
)

func startUpdateEndpoint() (func(), error) {
	if !hostRPCRequested(os.Args[1:]) {
		return func() {}, nil
	}
	return instanceidentity.ListenEndpoint(instanceidentity.ForHome(config.DeeperHomeDir()))
}
