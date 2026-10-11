package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWindowsReleaseSignsPayloadBeforeRepackaging(t *testing.T) {
	workflow := readTestFile(t, "../.github/workflows/release-desktop.yml")
	finalizer := readTestFile(t, "../scripts/finalize-windows-signed-candidate.sh")
	orderedSteps := []string{
		"name: Build and package",
		"name: Checkout protected release verifier",
		"name: Smoke-test packaged Electron startup",
		"name: Upload Windows signing inputs",
		"name: Restore both native-tested Windows payloads",
		"name: Connect to Certum",
		"name: Sign both payloads in the shared Certum session",
		"name: Package both architectures in parallel",
		"name: Seal amd64 in the shared Certum session",
		"name: Seal arm64 in the shared Certum session",
		"name: Upload signed package size reports",
	}
	last := -1
	for _, step := range orderedSteps {
		relativeIndex := strings.Index(workflow[last+1:], step)
		index := last + 1 + relativeIndex
		if relativeIndex < 0 {
			t.Fatalf("desktop release workflow is missing %q", step)
		}
		if index <= last {
			t.Fatalf("desktop release workflow step %q is out of order", step)
		}
		last = index
	}
	for _, want := range []string{
		`uses: ./release-control/.github/actions/setup-certum`,
		`github.repository == 'SivanCola/deepzora'`,
		`Certum credentials are required for public Windows releases`,
		`RELEASE_SIGNING_ATTESTATION does not match the current protected signing contract`,
		`needs.build.result == 'success'`,
		`needs.windows-sign.result == 'success'`,
		`go run ./cmd/release-signing-contract fingerprint`,
		`ref: ${{ github.workflow_sha }}`,
		`path: release-control`,
		`node desktop/packaging/smoke.mjs`,
		`FINALIZE_PHASE=sign bash release-control/scripts/finalize-windows-signed-candidate.sh`,
		`FINALIZE_PHASE=package bash`,
		`FINALIZE_PHASE=seal bash`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("desktop release workflow is missing signing contract %q", want)
		}
	}
	for _, want := range []string{
		`sign-certum.ps1" -PayloadDirectory`,
		`go run ./cmd/sign windows-payload`,
		`go run ./cmd/sign sign`,
		`go run ./cmd/sign verify`,
		`DEEPZORA_REQUIRE_PAYLOAD_MANIFEST=1`,
		`sign-certum.ps1" -FilePath "$installer"`,
		`verify-windows-authenticode.ps1`,
		`-ExpectedThumbprint "$CERTUM_KEY_ID"`,
		`go run ./cmd/sign sign "$dist"/*`,
	} {
		if !strings.Contains(finalizer, want) {
			t.Errorf("Windows signing finalizer is missing contract %q", want)
		}
	}
	ciWorkflow := readTestFile(t, "../.github/workflows/ci.yml")
	if !strings.Contains(workflow, `Smoke-test packaged Electron startup`) {
		t.Error("Windows release qualification must smoke the packaged Electron shell startup")
	}
	if strings.Contains(ciWorkflow, `node packaging/package.mjs`) {
		t.Error("ordinary CI must keep packaged acceptance in the release workflow")
	}
	if strings.Contains(ciWorkflow, "webview2") || strings.Contains(ciWorkflow, "WebView2") {
		t.Error("Windows CI must not reference the retired WebView2 smoke harness")
	}
	for _, forbidden := range []string{
		`signing-policy-slug: test-signing`,
		`artifact-configuration-slug: windows-installer-test-v2`,
		`steps.ver.outputs.channel == 'canary'`,
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("public desktop release workflow contains legacy Canary signing contract %q", forbidden)
		}
	}

	packager := readTestFile(t, "../scripts/package-windows-desktop.sh")
	copyMain := strings.Index(packager, `cp "$PAYLOAD/$BINNAME.exe" "$INSTALLER_DIR/$BINNAME.exe"`)
	makeNSIS := strings.Index(packager, "makensis \\\n")
	portable := strings.Index(packager, `cp "$PAYLOAD/$BINNAME.exe" "$portable_staging/versions/$version_label/$BINNAME.exe"`)
	bundle := strings.Index(packager, `installer_bundle="$DESKTOP/build/windows/installer-signing-bundle"`)
	if copyMain < 0 || makeNSIS < 0 || portable < 0 || bundle < 0 {
		t.Fatal("Windows packager is missing the signed-payload packaging stages")
	}
	if !(copyMain < makeNSIS && makeNSIS < portable && portable < bundle) {
		t.Fatalf("Windows package order must be payload copy -> NSIS -> portable -> signing bundle (copy=%d nsis=%d portable=%d bundle=%d)", copyMain, makeNSIS, portable, bundle)
	}
	for _, want := range []string{
		`node "$DESKTOP/packaging/signing-files.mjs" "$PAYLOAD" --check`,
		`cp "$PAYLOAD/$LAYOUT_INSTALLER.exe" "$INSTALLER_DIR/$LAYOUT_INSTALLER.exe"`,
		`cp "$PAYLOAD/$LAUNCHERNAME.exe" "$INSTALLER_DIR/$LAUNCHERNAME.exe"`,
		`cp "$PAYLOAD/$UPDATE_HELPER" "$INSTALLER_DIR/$UPDATE_HELPER"`,
		`cp "$PAYLOAD/$WINDOWS_CLINAME.exe" "$INSTALLER_DIR/$WINDOWS_CLINAME.exe"`,
		`cp -R "$PAYLOAD/app" "$INSTALLER_DIR/app"`,
		`rm -f -- "$INSTALLER_DIR/$PAYLOAD_MANIFEST" "$INSTALLER_DIR/$PAYLOAD_SIGNATURE"`,
		`cp "$PAYLOAD/$PAYLOAD_MANIFEST" "$INSTALLER_DIR/$PAYLOAD_MANIFEST"`,
		`cp "$PAYLOAD/$PAYLOAD_SIGNATURE" "$INSTALLER_DIR/$PAYLOAD_SIGNATURE"`,
		`DEEPZORA_REQUIRE_PAYLOAD_MANIFEST`,
		`"-DARG_DEEPZORA_SIGNED_UNINSTALLER=${uninstaller_path}"`,
		`cp "$PAYLOAD/$LAUNCHERNAME.exe" "$portable_staging/$APPNAME.exe"`,
		`cp -R "$PAYLOAD/app" "$portable_staging/versions/$version_label/app"`,
		`"$ROOT/scripts/verify-windows-portable.sh" "$portable_staging"`,
		`cp -R "$PAYLOAD/app" "$installer_bundle/app"`,
	} {
		if !strings.Contains(packager, want) {
			t.Errorf("Windows packager is missing payload contract %q", want)
		}
	}

	verifier := readTestFile(t, "../scripts/verify-windows-authenticode.ps1")
	for _, want := range []string{
		"Get-AuthenticodeSignature",
		"$signature.SignerCertificate",
		"$signature.Status -ne \"Valid\"",
		"Expand-Archive",
		`Get-ChildItem -LiteralPath $extractRoot -Recurse -File`,
		`$activeDir.Replace("\", "/") -ne "versions/$activeVersion"`,
		`Portable = (Join-Path $activeDir "deepzora-desktop.exe")`,
		`Portable = "Deepzora.exe"; Payload = "deepzora-launcher.exe"`,
		`Compare-Object $expectedPE $actualPE`,
		`[ValidateSet("canonical")]`,
		"Get-FileHash -Algorithm SHA256",
	} {
		if !strings.Contains(verifier, want) {
			t.Errorf("Windows Authenticode verifier is missing %q", want)
		}
	}

}

// requireRealBash skips when PATH resolves bash to the System32 WSL relay
// stub: LookPath finds it, but it cannot run scripts, so the packager dies
// with a WSL error instead of its own validation output.
func requireRealBash(t *testing.T) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, bash, "-c", "true").Run(); err != nil {
		t.Skipf("bash on PATH cannot run scripts: %v", err)
	}
}

func TestWindowsPackagerRejectsMissingOrPartialRequiredPayloadManifest(t *testing.T) {
	requireRealBash(t)
	for _, tc := range []struct {
		name      string
		manifest  bool
		signature bool
		want      string
	}{
		{name: "missing", want: "signed Windows packaging requires"},
		{name: "manifest only", manifest: true, want: "must be provided together"},
		{name: "signature only", signature: true, want: "must be provided together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := t.TempDir()
			for _, name := range []string{
				"deepzora-desktop.exe",
				"deepzora-layout-installer.exe",
				"deepzora-launcher.exe",
				"deepzora-update-helper.exe",
				"deepzora-cli.exe",
				"deepzora-uninstall.exe",
			} {
				if err := os.WriteFile(filepath.Join(payload, name), []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// The packager validates the Electron app/ tree and signing-files.txt
			// before the manifest gate, so the fixture must carry both.
			if err := os.MkdirAll(filepath.Join(payload, "app"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(payload, "app", "Deepzora.exe"), []byte("shell"), 0o600); err != nil {
				t.Fatal(err)
			}
			signingList := "app/Deepzora.exe\ndeepzora-cli.exe\ndeepzora-desktop.exe\ndeepzora-layout-installer.exe\ndeepzora-launcher.exe\ndeepzora-uninstall.exe\ndeepzora-update-helper.exe\n"
			if err := os.WriteFile(filepath.Join(payload, "signing-files.txt"), []byte(signingList), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.manifest {
				if err := os.WriteFile(filepath.Join(payload, "deepzora-payload.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.signature {
				if err := os.WriteFile(filepath.Join(payload, "deepzora-payload.json.minisig"), []byte("sig"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "../scripts/package-windows-desktop.sh", "amd64", payload)
			cmd.Env = append(os.Environ(), "DEEPZORA_REQUIRE_PAYLOAD_MANIFEST=1")
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), tc.want) {
				t.Fatalf("packager error = %v, output = %q, want %q", err, output, tc.want)
			}
		})
	}
}

func TestProductionSigningRunsOnlyFromProtectedControlPlane(t *testing.T) {
	candidate := readTestFile(t, "../.github/workflows/release-candidate.yml")
	promote := readTestFile(t, "../.github/workflows/release-promote.yml")
	desktop := readTestFile(t, "../.github/workflows/release-desktop.yml")
	if strings.Contains(promote, "\n  push:\n") || strings.Contains(desktop, "\n  push:\n") {
		t.Fatal("production workflows must not run directly with a tag-triggered signing origin")
	}
	if strings.Contains(candidate, "\n    tags:") || strings.Contains(candidate, "\n  pull_request") ||
		!strings.Contains(candidate, "\n  push:\n    branches: [main]\n    paths:\n      - release-notes/releases.json") {
		t.Fatal("automatic preparation must use the protected Notes push, never tags or PR heads")
	}
	activation := readTestFile(t, "../scripts/release-candidate-tags.sh")
	if !regexp.MustCompile(`(?m)actions/attest-build-provenance@[0-9a-f]{40} # v4\.2\.2$`).MatchString(candidate + "\n" + promote + "\n" + activation) {
		t.Error("sealed release control plane must attest with actions/attest-build-provenance v4.2.2 pinned to a commit")
	}
	for _, want := range []string{
		`candidate_preparation: true`,
		`git push --atomic "$remote"`,
		`environment: release`,
		`candidate_verified: true`,
	} {
		if !strings.Contains(candidate+"\n"+promote+"\n"+activation, want) {
			t.Errorf("sealed release control plane is missing %q", want)
		}
	}
	if _, err := os.Stat("../.github/workflows/release-stable-trigger.yml"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("retired tag relay still exists or cannot be checked: %v", err)
	}

	for _, path := range []string{
		"../.github/workflows/release-preview.yml",
		"../.github/workflows/release-cli-trigger.yml",
		"../.github/workflows/release-desktop-trigger.yml",
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("retired public prerelease workflow %s still exists or cannot be checked: %v", path, err)
		}
	}
}
