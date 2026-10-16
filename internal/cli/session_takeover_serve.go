package cli

// Discovery side of CLI takeover: find the resident serve processes recorded
// under <Deepzora home>/remote, ask one of them for the session through POST
// /handoff, and prompt before taking a held session over.

import (
	"bytes"
	"context"
	"deepzora/internal/processidentity"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/remote/bootstrap"
	"deepzora/internal/store"
)

// cliTakeoverTimeout bounds the drain window of a wait-mode takeover.
const cliTakeoverTimeout = 2 * time.Minute

type cliServeRecord struct {
	pid   int
	base  string
	token string
}

type cliTakeoverGrant struct {
	SessionPath     string `json:"sessionPath"`
	MirrorID        string `json:"mirrorId"`
	HandoffID       string `json:"handoffId,omitempty"`
	ReturnHandoffID string `json:"returnHandoffId"`
	SourceWriterID  string `json:"sourceWriterId"`
	TargetWriterID  string `json:"targetWriterId"`
}

// cliServeProcessAlive is the PID probe used to prune serve state files
// whose process is gone. Variable so tests can model dead and live records.
var cliServeProcessAlive = webInstanceProcessAlive

// discoverCLIServes enumerates resident serve processes recorded under
// <Deepzora home>/remote. This machine is the SSH target in the takeover
// scenario, so the bootstrap's SFTP-written state files are local files here.
// Records whose PID no longer exists are skipped: a restarted desktop
// respawns the serve on a new port, and dialing the stale address only
// produces connection-refused noise that can shadow a live record's result.
func discoverCLIServes() []cliServeRecord {
	dir := config.RemoteStateDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []cliServeRecord
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "serve-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		state, err := bootstrap.UnmarshalState(data)
		if err != nil || state.PID <= 0 || !cliServeProcessAlive(state.PID) {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(name, "serve-"), ".json")
		addr := state.Addr
		if port, err := os.ReadFile(filepath.Join(dir, store.RemoteServePortName(slug))); err == nil {
			if trimmed := strings.TrimSpace(string(port)); trimmed != "" {
				addr = trimmed
			}
		}
		if addr == "" {
			continue
		}
		token := ""
		if data, err := os.ReadFile(filepath.Join(dir, store.RemoteServeTokenName(slug))); err == nil {
			token = strings.TrimSpace(string(data))
		}
		if token == "" {
			continue
		}
		out = append(out, cliServeRecord{pid: state.PID, base: "http://" + addr, token: token})
	}
	return out
}

var discoverCLIServesForTakeover = discoverCLIServes

func cliServeClient(ctx context.Context, record cliServeRecord) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar}
	auth, _ := json.Marshal(map[string]string{"token": record.token})
	authReq, err := http.NewRequestWithContext(ctx, http.MethodPost, record.base+"/auth/token", bytes.NewReader(auth))
	if err != nil {
		return nil, err
	}
	authReq.Header.Set("Content-Type", "application/json")
	authResp, err := client.Do(authReq)
	if err != nil {
		return nil, &cliServeUnreachableError{err: err}
	}
	_, _ = io.Copy(io.Discard, authResp.Body)
	authResp.Body.Close()
	if authResp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("serve auth: status %d", authResp.StatusCode)
	}
	return client, nil
}

// postCLITakeoverHandoff exchanges one serve's /handoff for a grant. Both the
// legacy path-lease flow and the final-format identity flow validate the same
// wire contract; only the target key and post-grant reservation differ.
func postCLITakeoverHandoff(ctx context.Context, client *http.Client, base, sessionPath, errPrefix string) (cliTakeoverGrant, error) {
	body, _ := json.Marshal(map[string]any{
		"sessionPath": sessionPath, "targetWriterId": processidentity.WriterID(),
		"force": true, "mode": "wait", "timeoutMs": cliTakeoverTimeout.Milliseconds(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/handoff", bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err != nil {
		return cliTakeoverGrant{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return cliTakeoverGrant{}, fmt.Errorf("%s: %w", errPrefix, &cliServeUnreachableError{err: err})
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return cliTakeoverGrant{}, fmt.Errorf("%s: %s", errPrefix, strings.TrimSpace(string(respBody)))
	}
	var grant cliTakeoverGrant
	if json.Unmarshal(respBody, &grant) != nil || grant.MirrorID == "" || grant.HandoffID == "" ||
		grant.ReturnHandoffID == "" || grant.SourceWriterID == "" || grant.TargetWriterID != processidentity.WriterID() {
		return cliTakeoverGrant{}, fmt.Errorf("%s: invalid handoff grant", errPrefix)
	}
	return grant, nil
}

// promptSessionTakeover asks on the terminal (pre-TUI startup) whether to take
// the held session over. Non-interactive sessions answer no.
func promptSessionTakeover() bool {
	if !isInteractive() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s\n", control.SessionInUseMessage+"; close the other Deepzora window or process, or rerun with --copy to continue in a duplicated session")
	fmt.Fprint(os.Stderr, "take over the session from this machine's resident serve? [y/N] ")
	answer, err := readCLITakeoverAnswer()
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func readCLITakeoverAnswer() (string, error) {
	buf := make([]byte, 64)
	n, err := os.Stdin.Read(buf)
	if n > 0 {
		return string(buf[:n]), nil
	}
	return "", err
}
