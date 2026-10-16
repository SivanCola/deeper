package serve

import (
	"context"
	"os"
	"strings"
	"testing"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
)

func TestAutoClearsSessionEffortWithUnknownMetadata(t *testing.T) {
	writeServeModelConfig(t)
	path := config.UserConfigPath()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.ReplaceAll(string(body), "supported_efforts = [\"low\", \"high\"]", ""))
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	s, _, _, _ := newExclusiveSessionServeWithOptions(t, func(opts *control.Options) { opts.ModelRef = "alternate/shared-chat" })
	defer s.Close()
	high := "high"
	s.buildOptions.EffortOverride = &high
	builds := 0
	s.buildControllerWithOptions = func(_ context.Context, ref string, opts boot.Options) (*control.Controller, error) {
		builds++
		if opts.EffortOverride == nil || *opts.EffortOverride != "" || opts.EffortModel != ref {
			t.Fatalf("auto lost its override before boot: %+v", opts.EffortOverride)
		}
		cfg := config.LoadForEdit(path)
		if err := boot.ValidateReasoningSnapshot(cfg, opts); err != nil {
			return nil, err
		}
		return serveModelReplacement(opts, nil), nil
	}
	if err := s.switchEffort(context.Background(), "auto"); err != nil {
		t.Fatal(err)
	}
	if builds != 1 || s.buildOptions.EffortOverride == nil || *s.buildOptions.EffortOverride != "" {
		t.Fatal("auto did not commit")
	}
}
