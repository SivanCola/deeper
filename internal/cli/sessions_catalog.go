package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"deepzora/internal/session"
)

func sessionOrSessionsCommand(command string, args []string) int {
	if command == "sessions" {
		return sessionsCommand(args)
	}
	return sessionCommand(args)
}

func sessionsCommand(args []string) int {
	if len(args) == 0 || (args[0] != "reindex" && args[0] != "diagnose") {
		fmt.Fprintln(os.Stderr, "usage: deepzora sessions <reindex|diagnose> [--dir PATH] [--json]")
		return 2
	}
	return sessionCatalogCommand("sessions "+args[0], args[1:], args[0] == "reindex")
}

type sessionCatalogReport struct {
	State             string   `json:"state"`
	Directories       int      `json:"directories"`
	SourceSessions    int      `json:"sourceSessions"`
	IndexedSessions   int      `json:"indexedSessions"`
	UnindexedSessions int      `json:"unindexedSessions"`
	ActiveSessions    int      `json:"activeSessions"`
	RefreshedSessions int      `json:"refreshedSessions"`
	FailedSessions    int      `json:"failedSessions"`
	Errors            []string `json:"errors"`
}

func sessionCatalogCommand(name string, args []string, rebuild bool) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var dirs stringListFlag
	jsonOut := fs.Bool("json", false, "print current session catalog diagnostics as JSON")
	fs.Var(&dirs, "dir", "host session directory; repeat for multiple directories")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "usage: deepzora %s [--dir PATH] [--json]\n", name)
		return 2
	}
	report := sessionCatalogReport{State: "ready", Errors: []string{}}
	for _, root := range currentSessionCatalogRoots(dirs) {
		report.Directories++
		inspectCurrentSessionCatalog(context.Background(), root, rebuild, &report)
	}
	if len(report.Errors) > 0 || report.UnindexedSessions > 0 {
		report.State = "degraded"
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	} else {
		fmt.Printf("current sessions: %d; indexed: %d; unindexed: %d; active: %d; refreshed: %d; failed: %d\n", report.SourceSessions, report.IndexedSessions, report.UnindexedSessions, report.ActiveSessions, report.RefreshedSessions, report.FailedSessions)
		for _, message := range report.Errors {
			fmt.Fprintln(os.Stderr, "error:", message)
		}
	}
	if report.State != "ready" {
		return 1
	}
	return 0
}

// Diagnosis reads current manifests and cache metadata directly. Only an
// explicit reindex creates a query worker and refreshes derived metadata.
func inspectCurrentSessionCatalog(ctx context.Context, root string, rebuild bool, report *sessionCatalogReport) {
	persistence := session.NewFilesystemPersistence(root)
	var service *session.Service
	if rebuild {
		var err error
		service, err = session.NewService("local", persistence)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
			return
		}
		defer func() {
			if err := service.Shutdown(context.Background()); err != nil {
				report.Errors = append(report.Errors, err.Error())
			}
		}()
	}
	for cursor := ""; ; {
		page, err := persistence.List(ctx, cursor, 100)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
			return
		}
		for _, info := range page.Sessions {
			report.SourceSessions++
			if info.Error != "" {
				report.FailedSessions++
				report.Errors = append(report.Errors, fmt.Sprintf("%s: %s", info.Path, info.Error))
				continue
			}
			if session.ProbeWriterHeld(info.Path) {
				report.ActiveSessions++
			}
			if rebuild {
				info, err = service.Query().RefreshMetadata(ctx, session.SessionRef{HostID: "local", SessionID: info.SessionID})
				if err != nil {
					report.FailedSessions++
					report.Errors = append(report.Errors, err.Error())
					continue
				}
				report.RefreshedSessions++
			}
			if info.MetadataStatus == session.MetadataReady {
				report.IndexedSessions++
			} else {
				report.UnindexedSessions++
			}
		}
		if page.NextCursor == "" {
			return
		}
		if page.NextCursor == cursor || len(page.Sessions) == 0 {
			report.Errors = append(report.Errors, "session catalog cursor did not advance")
			return
		}
		cursor = page.NextCursor
	}
}
