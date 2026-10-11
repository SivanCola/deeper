package main

import "deepzora/internal/config"

// CredentialDiagnostics exposes the shared read-only credential report to the
// Desktop diagnostics page. Probe performs a bounded temporary create/rename.
func (a *App) CredentialDiagnostics(probe bool) (config.CredentialDiagnosticReport, error) {
	return config.DiagnoseCredentials(config.CredentialDiagnosticOptions{Probe: probe})
}

// RepairCredentials applies the same conservative repair used by
// `deepzora doctor credentials --repair`. dryRun returns the action preview.
func (a *App) RepairCredentials(dryRun bool) (config.CredentialDiagnosticReport, error) {
	return config.DiagnoseCredentials(config.CredentialDiagnosticOptions{Probe: true, Repair: true, DryRun: dryRun})
}
