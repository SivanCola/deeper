package main

import (
	"fmt"
	"runtime"
	"time"
)

func (a *App) recordPreviousRunDiagnostics() {
	for _, lifecycle := range a.lifecycle.previousRuns {
		persisted := writePendingReport(desktopLifecycleReport(lifecycle))
		a.lifecycle.tracker.finalizeObservation(lifecycle, persisted)
		if !persisted {
			continue
		}
		if m := a.metrics.Load(); m != nil {
			m.inc("desktop_exit", "abnormal")
			m.inc("desktop_exit_phase", metricBucket(lifecycle.Phase))
			m.persist()
		}
	}
}

func desktopLifecycleReport(previous desktopLifecycleObservation) crashReport {
	phase := metricBucket(previous.Phase)
	category := "unclean_exit"
	processRole := previous.ProcessRole
	if processRole == "" {
		processRole = "service"
	}
	if previous.TerminationReason == shutdownReasonStartupFailure {
		category = "startup_failure"
	}
	message := fmt.Sprintf(`[desktop.abnormal_exit.v2]

Deepzora found a per-process lifecycle record whose desktop process was no longer running.

--- lifecycle context ---
phase: %s
previous version: %s
previous channel: %s
started at: %s
last phase update: %s`,
		phase,
		sanitizeCrashField(previous.Version, 64),
		sanitizeCrashField(previous.Channel, 32),
		sanitizeCrashField(previous.StartedAt, 64),
		sanitizeCrashField(previous.UpdatedAt, 64),
	)
	report := baseCrashReport("crash")
	report.SchemaVersion = 4
	report.Source = "native.lifecycle"
	report.Label = "desktop.abnormal_exit.v2"
	report.ErrorType = "AbnormalDesktopExit"
	report.ErrorMessage = "A per-process lifecycle record remained after its desktop process stopped."
	report.TopFrame = "desktop.lifecycle.v2." + phase
	report.FingerprintHint = "desktop.abnormal_exit.v2." + runtime.GOOS + "." + phase
	report.Diagnostics = &crashDiagnostics{
		SubjectVersion: previous.Version, SubjectBuildCommit: previous.BuildCommit, SubjectChannel: previous.Channel,
		ObserverVersion: version, ObserverBuildCommit: buildCommit(), RunID: previous.RunID, IncidentID: previous.IncidentID,
		ProcessRole: processRole, LastPhase: phase, LastPhaseAt: previous.UpdatedAt,
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), TerminationReason: previous.TerminationReason,
		CleanupOutcome: previous.CleanupOutcome, Evidence: "observed", Category: category,
	}
	report.Message = sanitizeCrashText(message, maxCrashDetailBytes)
	return report
}
