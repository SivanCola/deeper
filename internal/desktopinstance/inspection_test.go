package desktopinstance

import (
	"path/filepath"
	"testing"
)

func TestStableLauncherImageExcludesShellsAndOtherInstallations(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Deepzora.exe", "deepzora-launcher.exe"} {
		if !stableLauncherImage(root, filepath.Join(root, name)) {
			t.Fatalf("stable launcher was classified as a shell: %s", name)
		}
	}
	for _, relative := range []string{"app/Deepzora.exe", "versions/v0.0.1/app/Deepzora.exe", "../other/Deepzora.exe", "deepzora-desktop.exe"} {
		if stableLauncherImage(root, filepath.Join(root, relative)) {
			t.Fatalf("non-launcher was excluded from ownership checks: %s", relative)
		}
	}
}

func TestShellOwnersRequireCurrentStatusAndVerifiedDescendants(t *testing.T) {
	for _, tc := range []struct {
		name   string
		nodes  []shellObservation
		owners map[uint32]uint32
	}{
		{"current shell and nested Chromium children", []shellObservation{
			{3, 2, "app/Deepzora.exe", 30, true, nil},
			{2, 1, "app/Deepzora.exe", 20, true, nil},
			{1, 0, "app/Deepzora.exe", 10, true, &Status{PID: 1}},
		}, map[uint32]uint32{1: 1, 2: 1, 3: 1}},
		{"missing current status", []shellObservation{
			{1, 0, "app/Deepzora.exe", 10, true, nil},
			{2, 1, "app/Deepzora.exe", 20, true, nil},
		}, map[uint32]uint32{}},
		{"different release image", []shellObservation{
			{1, 0, "v0.0.1/app/Deepzora.exe", 10, true, &Status{PID: 1}},
			{2, 1, "v0.0.2/app/Deepzora.exe", 20, true, nil},
		}, map[uint32]uint32{1: 1}},
		{"reused parent PID", []shellObservation{
			{1, 0, "app/Deepzora.exe", 30, true, &Status{PID: 1}},
			{2, 1, "app/Deepzora.exe", 20, true, nil},
		}, map[uint32]uint32{1: 1}},
		{"exited parent", []shellObservation{
			{1, 0, "app/Deepzora.exe", 10, false, &Status{PID: 1}},
			{2, 1, "app/Deepzora.exe", 20, true, nil},
		}, map[uint32]uint32{}},
		{"unrooted parent cycle", []shellObservation{
			{1, 2, "app/Deepzora.exe", 10, true, nil},
			{2, 1, "app/Deepzora.exe", 20, true, nil},
		}, map[uint32]uint32{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shellOwners(tc.nodes)
			if len(got) != len(tc.owners) {
				t.Fatalf("owners = %v, want %v", got, tc.owners)
			}
			for pid, owner := range tc.owners {
				if got[pid] != owner {
					t.Fatalf("PID %d owner = %d, want %d", pid, got[pid], owner)
				}
			}
		})
	}
}
