// reportingestprobe uploads synthetic reports through the shared client transport.
// It prints event IDs only; report tokens and real user reports are never logged.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"deepzora/internal/crashreport"
	"deepzora/internal/reporttransport"
	"github.com/joho/godotenv"
)

type statusTransport struct {
	statuses []int
}

func (t *statusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(req)
	if err == nil {
		t.statuses = append(t.statuses, response.StatusCode)
	}
	return response, err
}

func main() {
	envFile := flag.String("env-file", "supabase/report-client.env.local", "local report endpoint/token file")
	flag.Parse()
	if err := run(*envFile); err != nil {
		fmt.Fprintln(os.Stderr, "report ingest probe:", err)
		os.Exit(1)
	}
}

func run(envFile string) error {
	values, err := godotenv.Read(envFile)
	if err != nil {
		return err
	}
	endpoint := values["DEEPZORA_REPORT_ENDPOINT"]
	if endpoint == "" || values["DEEPZORA_REPORT_TOKEN"] == "" {
		return fmt.Errorf("endpoint and submission token must be configured")
	}
	if err := os.Setenv("DEEPZORA_REPORT_TOKEN", values["DEEPZORA_REPORT_TOKEN"]); err != nil {
		return err
	}
	newID := func() (string, error) {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", err
		}
		return hex.EncodeToString(id[:]), nil
	}
	feedbackID, err := newID()
	if err != nil {
		return err
	}
	crashID, err := newID()
	if err != nil {
		return err
	}
	// Use a distinct grouping key per probe so it never joins real incidents.
	group := feedbackID + crashID
	feedback := map[string]any{
		"eventId": feedbackID, "dedupKey": group, "schemaVersion": 4,
		"kind": "feedback", "version": "ingest-probe", "os": "test", "arch": "test",
		"source": "probe", "message": "[ingest-probe] Synthetic feedback; no user data.",
	}
	cli := crashreport.Report{
		EventID: crashID, DedupKey: group, SchemaVersion: 2,
		Kind: "crash", Version: "ingest-probe", OS: "test", Arch: "test",
		Message: "[ingest-probe] Synthetic CLI crash; no user data.",
	}
	preview, err := crashreport.Preview(cli)
	if err != nil {
		return err
	}
	var sanitizedCLI map[string]any
	if err := json.Unmarshal(preview, &sanitizedCLI); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	transport := &statusTransport{}
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport}
	for _, report := range []any{feedback, sanitizedCLI, feedback} {
		if err := reporttransport.Post(ctx, client, endpoint, report); err != nil {
			return err
		}
	}
	if !slices.Equal(transport.statuses, []int{202, 202, 200}) {
		return fmt.Errorf("unexpected collector statuses %v, want [202 202 200]", transport.statuses)
	}
	fmt.Printf("Synthetic feedback, CLI crash and retry returned %v. Verify exactly two rows:\nfeedback eventId=%s\ncrash eventId=%s\n", transport.statuses, feedbackID, crashID)
	return nil
}
