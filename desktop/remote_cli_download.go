package main

import (
	"context"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/netclient"
	"deepzora/internal/releaseasset"
)

const remoteCLIDownloadTimeout = 2 * time.Minute

func downloadRemoteCLIBinary(ctx context.Context, version, goos, goarch string) ([]byte, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	client, err := netclient.NewHTTPClient(cfg.NetworkProxySpec(), netclient.TransportOptions{
		ResponseHeaderTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	client.Timeout = remoteCLIDownloadTimeout
	return releaseasset.DownloadCLI(ctx, client, version, goos, goarch)
}
