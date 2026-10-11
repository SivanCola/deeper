package winuninstall

import "testing"

func TestPlanDoesNotPromoteLegacyOnlyRegistrationWithoutManagedDesktopInstall(t *testing.T) {
	legacy := &Registration{
		DisplayName:     "Deepzora",
		DisplayVersion:  "0.53.0",
		InstallLocation: `"D:\Deepzora"`,
		UninstallString: `"D:\Deepzora\uninstall.exe"`,
	}

	got, err := Plan(nil, legacy, `D:\Deepzora`, "v1.21.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Managed || got.DeleteLegacy {
		t.Fatalf("plan = %+v, want the full installer to migrate a legacy-only registration", got)
	}
}

func TestPlanRefreshesManagedDesktopRegistrationAndDeletesMatchingLegacyAlias(t *testing.T) {
	current := &Registration{
		DisplayName:     "Deepzora",
		DisplayVersion:  "1.18.0",
		InstallLocation: `D:\Deepzora`,
		UninstallString: `"D:\Deepzora\uninstall.exe"`,
	}
	legacy := &Registration{
		DisplayName:     "Deepzora",
		DisplayVersion:  "0.53.0",
		InstallLocation: `D:\Deepzora`,
		UninstallString: `"D:\Deepzora\uninstall.exe"`,
	}

	got, err := Plan(current, legacy, `d:\deepzora\`, "1.21.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Managed || !got.DeleteLegacy || got.Desired.DisplayVersion != "1.21.0" {
		t.Fatalf("plan = %+v, want current registration refresh", got)
	}
	if got.Desired.InstallLocation != `d:\deepzora` ||
		got.Desired.UninstallString != `"d:\deepzora\uninstall.exe"` ||
		got.Desired.DisplayIcon != `d:\deepzora\Deepzora.exe` {
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
				DisplayName:     "Deepzora",
				InstallLocation: `C:\Other\Deepzora`,
				UninstallString: `"C:\Other\Deepzora\uninstall.exe"`,
			},
			uninstall: true,
		},
		{
			name: "foreign display name",
			legacy: &Registration{
				DisplayName:     "Another App",
				InstallLocation: `D:\Deepzora`,
				UninstallString: `"D:\Deepzora\uninstall.exe"`,
			},
			uninstall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Plan(tt.current, tt.legacy, `D:\Deepzora`, "v1.21.0", tt.uninstall)
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
		DisplayName:     "Deepzora",
		InstallLocation: `D:\Deepzora`,
		UninstallString: `"D:\Deepzora\uninstall.exe"`,
	}
	for _, tc := range []struct {
		root    string
		version string
	}{
		{root: "", version: "v1.21.0"},
		{root: `D:\Deepzora`, version: ""},
		{root: `D:\Deepzora`, version: "dev"},
	} {
		if _, err := Plan(managed, nil, tc.root, tc.version, true); err == nil {
			t.Fatalf("Plan(%q, %q) succeeded, want error", tc.root, tc.version)
		}
	}
}
