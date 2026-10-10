package winuninstall

import "testing"

func TestPlanDoesNotPromoteLegacyOnlyRegistrationWithoutManagedDesktopInstall(t *testing.T) {
	legacy := &Registration{
		DisplayName:     "Deeper",
		DisplayVersion:  "0.53.0",
		InstallLocation: `"D:\Deeper"`,
		UninstallString: `"D:\Deeper\uninstall.exe"`,
	}

	got, err := Plan(nil, legacy, `D:\Deeper`, "v1.21.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Managed || got.DeleteLegacy {
		t.Fatalf("plan = %+v, want the full installer to migrate a legacy-only registration", got)
	}
}

func TestPlanRefreshesManagedDesktopRegistrationAndDeletesMatchingLegacyAlias(t *testing.T) {
	current := &Registration{
		DisplayName:     "Deeper",
		DisplayVersion:  "1.18.0",
		InstallLocation: `D:\Deeper`,
		UninstallString: `"D:\Deeper\uninstall.exe"`,
	}
	legacy := &Registration{
		DisplayName:     "Deeper",
		DisplayVersion:  "0.53.0",
		InstallLocation: `D:\Deeper`,
		UninstallString: `"D:\Deeper\uninstall.exe"`,
	}

	got, err := Plan(current, legacy, `d:\deeper\`, "1.21.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Managed || !got.DeleteLegacy || got.Desired.DisplayVersion != "1.21.0" {
		t.Fatalf("plan = %+v, want current registration refresh", got)
	}
	if got.Desired.InstallLocation != `d:\deeper` ||
		got.Desired.UninstallString != `"d:\deeper\uninstall.exe"` ||
		got.Desired.DisplayIcon != `d:\deeper\Deeper.exe` {
		t.Fatalf("desired registration = %+v", got.Desired)
	}
}

func TestPlanDoesNotRegisterPortableOrUnrelatedInstall(t *testing.T) {
	tests := []struct {
		name      string
		current   *Registration
		legacy    *Registration
		uninstall bool
	}{
		{name: "portable", uninstall: false},
		{
			name: "unrelated legacy install",
			legacy: &Registration{
				DisplayName:     "Deeper",
				InstallLocation: `C:\Other\Deeper`,
				UninstallString: `"C:\Other\Deeper\uninstall.exe"`,
			},
			uninstall: true,
		},
		{
			name: "foreign display name",
			legacy: &Registration{
				DisplayName:     "Another App",
				InstallLocation: `D:\Deeper`,
				UninstallString: `"D:\Deeper\uninstall.exe"`,
			},
			uninstall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Plan(tt.current, tt.legacy, `D:\Deeper`, "v1.21.0", tt.uninstall)
			if err != nil {
				t.Fatal(err)
			}
			if got.Managed || got.DeleteLegacy {
				t.Fatalf("plan = %+v, want no registry mutation", got)
			}
		})
	}
}

func TestPlanRejectsInvalidInputs(t *testing.T) {
	managed := &Registration{
		DisplayName:     "Deeper",
		InstallLocation: `D:\Deeper`,
		UninstallString: `"D:\Deeper\uninstall.exe"`,
	}
	for _, tc := range []struct {
		root    string
		version string
	}{
		{root: "", version: "v1.21.0"},
		{root: `D:\Deeper`, version: ""},
		{root: `D:\Deeper`, version: "dev"},
	} {
		if _, err := Plan(managed, nil, tc.root, tc.version, true); err == nil {
			t.Fatalf("Plan(%q, %q) succeeded, want error", tc.root, tc.version)
		}
	}
}
