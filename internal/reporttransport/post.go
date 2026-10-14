// Package reporttransport sends the shared desktop/CLI report protocol.
package reporttransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

const TokenHeader = "X-Deepzora-Report-Token"

// Post preserves the existing JSON ingest contract. The optional token grants
// report submission only; it must never be a Supabase database/API secret.
func Post(ctx context.Context, client *http.Client, endpoint string, report any) error {
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(os.Getenv("DEEPZORA_REPORT_TOKEN")); token != "" {
		host := req.URL.Hostname()
		ip := net.ParseIP(host)
		loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
		if req.URL.Scheme != "https" && !(req.URL.Scheme == "http" && loopback) {
			return fmt.Errorf("report token requires HTTPS (except local loopback)")
		}
		req.Header.Set(TokenHeader, token)
	}
	// Custom headers and report bodies must not follow a collector redirect.
	collectorClient := *client
	collectorClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := collectorClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("crash endpoint returned %s", resp.Status)
	}
	return nil
}
