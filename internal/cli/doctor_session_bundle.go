package cli

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/doctor"
	"deepzora/internal/pathidentity"
	"deepzora/internal/session"
)

const doctorSessionBundleSchemaVersion = 1

// doctorSessionBundleOptions names an already resolved current session. The caller
// owns the Service lifetime; bundling never acquires a session writer.
type doctorSessionBundleOptions struct {
	Version    string
	Service    *session.Service
	Ref        session.SessionRef
	OutputPath string
	Now        time.Time
}

type doctorSessionBundleResult struct{ Path string }

type doctorSessionBundleManifest struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   time.Time              `json:"generated_at"`
	Version       string                 `json:"version"`
	Snapshot      session.ExportSnapshot `json:"snapshot"`
	Files         []string               `json:"files"`
}

// writeDoctorSessionBundle exports a fixed current event prefix through the same
// diagnostic writer used by Desktop and Serve, including readable-prefix
// evidence when the durable tail is damaged. Source data is never repaired.
func writeDoctorSessionBundle(opts doctorSessionBundleOptions) (doctorSessionBundleResult, error) {
	if opts.Service == nil {
		return doctorSessionBundleResult{}, fmt.Errorf("current session service is required")
	}
	ctx := context.Background()
	sourceDir, err := opts.Service.SessionDir(ctx, opts.Ref)
	if err != nil {
		return doctorSessionBundleResult{}, err
	}
	query := opts.Service.Query()
	snapshot, err := query.CaptureDiagnosticSnapshot(ctx, opts.Ref)
	if err != nil {
		return doctorSessionBundleResult{}, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	outPath := strings.TrimSpace(opts.OutputPath)
	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), "deepzora-session-"+opts.Ref.SessionID+"-diag.zip")
	}
	outPath, err = filepath.Abs(outPath)
	if err != nil {
		return doctorSessionBundleResult{}, err
	}
	source, err := pathidentity.Resolve(sourceDir, pathidentity.Options{FollowLeaf: true})
	if err != nil {
		return doctorSessionBundleResult{}, err
	}
	destination, err := pathidentity.Resolve(outPath, pathidentity.Options{})
	if err != nil {
		return doctorSessionBundleResult{}, err
	}
	if rel, err := filepath.Rel(source.Key, destination.Key); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return doctorSessionBundleResult{}, fmt.Errorf("diagnostic output must be outside the source session directory")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0700); err != nil {
		return doctorSessionBundleResult{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(outPath), ".deepzora-diagnostic-*.zip")
	if err != nil {
		return doctorSessionBundleResult{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	writer := zip.NewWriter(file)
	defer writer.Close()
	if err := addBundleJSON(writer, "doctor.json", doctor.Collect(doctor.Options{Version: opts.Version}), now); err != nil {
		return doctorSessionBundleResult{}, err
	}
	diagnostic, err := writer.Create("session-diagnostics.json")
	if err != nil {
		return doctorSessionBundleResult{}, err
	}
	if err := control.WriteColdSessionDiagnostics(ctx, diagnostic, query, snapshot, control.GoalDiagnosticMetadata{ApplicationVersion: opts.Version}, nil); err != nil {
		return doctorSessionBundleResult{}, err
	}
	manifest := doctorSessionBundleManifest{
		SchemaVersion: doctorSessionBundleSchemaVersion, GeneratedAt: now, Version: opts.Version,
		Snapshot: snapshot, Files: []string{"doctor.json", "session-diagnostics.json"},
	}
	if err := addBundleJSON(writer, "manifest.json", manifest, now); err != nil {
		return doctorSessionBundleResult{}, err
	}
	if err := writer.Close(); err != nil {
		return doctorSessionBundleResult{}, err
	}
	if err := file.Close(); err != nil {
		return doctorSessionBundleResult{}, err
	}
	if err := os.Rename(file.Name(), outPath); err != nil {
		return doctorSessionBundleResult{}, err
	}
	return doctorSessionBundleResult{Path: outPath}, nil
}

func addBundleJSON(writer *zip.Writer, name string, value any, now time.Time) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now}
	header.SetMode(0600)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(entry)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
