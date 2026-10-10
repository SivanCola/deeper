// Command deeper is a config- and plugin-driven coding agent CLI.
package main

import (
	"os"
	"runtime/debug"

	"deeper/internal/cli"
	"deeper/internal/config"
	"deeper/internal/crashreport"
	"deeper/internal/plugin"
	"deeper/internal/skill/skillwatch"

	// Blank imports wire compile-time built-ins into their registries.
	_ "deeper/internal/provider/anthropic"
	_ "deeper/internal/provider/openai"
	_ "deeper/internal/provider/responses"
	_ "deeper/internal/tool/builtin"
)

// Build identity injected via -ldflags (see Makefile). version remains the
// single-line contract for `deeper --version`; gitCommit/buildTimeUTC feed
// `deeper version --verbose` / `--json` without embedding config paths.
var (
	version      = "dev"
	gitCommit    = ""
	buildTimeUTC = ""
)

// runCLI is the CLI entry; tests may stub it. Production routes through
// RunWithBuildInfo so ldflags metadata is available to version --verbose/--json.
var runCLI = func(args []string, buildVersion string) int {
	return cli.RunWithBuildInfo(args, cli.BuildInfo{
		Version:      buildVersion,
		GitCommit:    gitCommit,
		BuildTimeUTC: buildTimeUTC,
	})
}

func main() {
	// Internal watcher-helper entry: the host-shared skill watch service
	// re-enters this executable so Windows directory watching never runs
	// in-process. Dispatch before any application initialization.
	if skillwatch.MaybeRunHelper() {
		return
	}
	plugin.SetMCPClientVersion(version)
	os.Exit(runWithCrashCapture(os.Args[1:], version))
}

func runWithCrashCapture(args []string, buildVersion string) (exitCode int) {
	home := config.DeeperHomeDir()
	crashreport.CaptureFatalDumps(home, buildVersion)
	releaseFatalOutput := crashreport.InstallFatalOutput(home)
	defer func() {
		// A panic recovered here is already reported, so the runtime's copy of
		// the re-raise must not queue a second one.
		releaseFatalOutput()
		if recovered := recover(); recovered != nil {
			_ = crashreport.CapturePanic(home, buildVersion, recovered, debug.Stack())
			panic(recovered)
		}
	}()
	return runCLI(args, buildVersion)
}
