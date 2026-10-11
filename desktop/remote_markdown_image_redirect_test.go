package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"deepzora/internal/netclient"
)

func TestRemoteMarkdownImageRevalidatesRedirectDNS(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		privateDNS   bool
		wantStatus   int
		wantDials    int32
	}{
		{"public redirect", "http://other.example.test/second", false, http.StatusOK, 2},
		{"private DNS", "http://private.example.test/second", true, http.StatusBadGateway, 1},
		{"same-host DNS rebinding", "http://images.example.test/second", true, http.StatusBadGateway, 1},
		{"private literal", "http://127.0.0.1/second", false, http.StatusBadGateway, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lookups, dials atomic.Int32
			factory := func(spec netclient.ProxySpec) (*http.Client, error) {
				client, err := newRemoteMarkdownImageClientWithLookup(spec, func(context.Context, string) ([]net.IPAddr, error) {
					ip := "93.184.216.34"
					if lookups.Add(1) > 1 && tc.privateDNS {
						ip = "127.0.0.1"
					}
					return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
				})
				if err != nil {
					return nil, err
				}
				rt := client.Transport.(remoteMarkdownImageRoundTripper)
				rt.dialerForProxy = func(*url.URL) (netclient.StreamDialer, error) {
					return netclient.DialerFunc(func(_ context.Context, _, address string) (net.Conn, error) {
						dials.Add(1)
						if address != "93.184.216.34:80" {
							t.Errorf("unvetted address reached dial: %s", address)
						}
						clientSide, serverSide := net.Pipe()
						go func() {
							defer serverSide.Close()
							r, err := http.ReadRequest(bufio.NewReader(serverSide))
							if err != nil {
								return
							}
							_ = r.Body.Close()
							status, body := http.StatusOK, markdownImageTestPNG
							header := http.Header{"Content-Type": {"image/png"}}
							if r.URL.Path == "/first" {
								status, body = http.StatusTemporaryRedirect, nil
								header.Set("Location", tc.target)
							}
							response := http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Close: true}
							_ = response.Write(serverSide)
						}()
						return clientSide, nil
					}), nil
				}
				client.Transport = rt
				return client, nil
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, remoteMarkdownImagePath+"?url="+url.QueryEscape("http://images.example.test/first"), nil)
			serveRemoteMarkdownImage(recorder, request, netclient.ProxySpec{Mode: netclient.ModeOff}, factory)
			if recorder.Code != tc.wantStatus || dials.Load() != tc.wantDials {
				t.Fatalf("redirect status=%d dials=%d; want status=%d dials=%d", recorder.Code, dials.Load(), tc.wantStatus, tc.wantDials)
			}
		})
	}
}
