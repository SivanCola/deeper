package cli

import (
	"context"
	"deepzora/internal/historycatalog"
	"deepzora/internal/session"
	"errors"
	"flag"
	"fmt"
	"os"
)

func init() {
	registerCatalogCommand(catalogCommand{
		name: "history", path: historycatalog.DefaultPath, reindex: reindexHistoryCatalog,
		completionFlags: []cliCompletionFlag{
			completionFlag("--json", cliCompletionNoValue),
			completionFlag("--dir", cliCompletionPathValue),
		},
	})
}

func reindexHistoryCatalog(args []string) int {
	fs := flag.NewFlagSet("catalogs reindex history", flag.ContinueOnError)
	var dirs stringListFlag
	jsonOut := fs.Bool("json", false, "print status as JSON")
	fs.Var(&dirs, "dir", "session directory to index; repeat for multiple directories")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	status, err := rebuildCurrentHistoryCatalog(context.Background(), dirs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return printCatalogStatus(status, *jsonOut)
}

func rebuildCurrentHistoryCatalog(ctx context.Context, dirs []string) (status historycatalog.Status, err error) {
	var services []*session.Service
	defer func() {
		for _, service := range services {
			err = errors.Join(err, service.Shutdown(context.Background()))
		}
	}()
	var roots []historycatalog.Root
	for _, root := range currentSessionCatalogRoots(dirs) {
		service, openErr := session.NewService("local", session.NewFilesystemPersistence(root))
		if openErr != nil {
			return status, openErr
		}
		services = append(services, service)
		children, childErr := service.Query().Subagents()
		if childErr != nil {
			return status, childErr
		}
		roots = append(roots, historycatalog.CurrentRoot(service.Query()), historycatalog.CurrentRoot(children))
	}
	return historycatalog.Rebuild(ctx, historycatalog.Options{}, roots)
}
