package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/config"
)

func TestRunPresetReachesProviderRequest(t *testing.T) {
	isolateCLIConfigHome(t)
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		select {
		case requests <- request:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	t.Setenv("DEEPZORA_TEST_RUN_KEY", "test-key")
	path := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "default_model = \"fake/model-a\"\n\n[[providers]]\nname = \"fake\"\nkind = \"openai\"\nbase_url = \"" + server.URL + "/v1\"\nmodels = [\"model-a\"]\ndefault = \"model-a\"\napi_key_env = \"DEEPZORA_TEST_RUN_KEY\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	writeCLITestPreset(t, root, "review", "CLI PRESET PERSONA", []string{"read_file"})
	var rc int
	var stderr string
	captureStdout(t, func() {
		stderr = captureStderr(t, func() { rc = runAgent([]string{"--preset", "review", "-p", "review the code"}, "test") })
	})
	if rc != 0 {
		t.Fatalf("run preset exit=%d stderr=%s", rc, stderr)
	}
	var request map[string]any
	select {
	case request = <-requests:
	default:
		t.Fatal("provider received no request")
	}
	rawMessages, err := json.Marshal(request["messages"])
	if err != nil || !strings.Contains(string(rawMessages), "CLI PRESET PERSONA") {
		t.Fatalf("provider messages omit selected persona: %s, %v", rawMessages, err)
	}
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("provider tools = %#v", request["tools"])
	}
	seenRead := false
	for _, raw := range tools {
		entry := raw.(map[string]any)["function"].(map[string]any)
		name := entry["name"].(string)
		if name == "read_file" {
			seenRead = true
		} else if name != "use_capability" {
			t.Fatalf("provider request contains excluded tool %q", name)
		}
	}
	if !seenRead {
		t.Fatal("provider request omitted admitted read_file tool")
	}
}
