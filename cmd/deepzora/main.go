// Command deepzora is a config- and plugin-driven coding agent CLI.
package main

import (
	"os"
	"runtime/debug"

	"deepzora/internal/cli"
	"deepzora/internal/config"
	"deepzora/internal/crashreport"
	"deepzora/internal/plugin"
	"deepzora/internal/skill/skillwatch"

	// Blank imports wire compile-time built-ins into their registries.
	_ "deepzora/internal/provider/anthropic"
	_ "deepzora/internal/provider/openai"
	_ "deepzora/internal/provider/responses"
	_ "deepzora/internal/tool/builtin"
)

// Build identity injected via -ldflags (see Makefile). version remains the
// single-line contract for `deepzora --version`; gitCommit/buildTimeUTC feed
// `deepzora version --verbose` / `--json` without embedding config paths.
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
	home := config.DeepzoraHomeDir()
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
