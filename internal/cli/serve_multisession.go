package cli

import (
	"context"
	"flag"
	"os"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/remote/bootstrap"
	"deepzora/internal/serve"
)

func registerServeCapabilityFlags(fs *flag.FlagSet) {
	_ = fs.Bool("session-events", false, "tag session events and finish switched-away turns in background ("+bootstrap.ServeCapsToken+")")
	_ = fs.Bool("detached-heal", false, "retire background sessions after provider credential-channel repair")
	// browser-broker is the capability marker a desktop bootstrap greps for;
	// the broker itself is configured through the environment only.
	_ = fs.Bool("browser-broker", false, "use the desktop browser broker from "+bootstrap.BrowserBrokerEnv+"/"+bootstrap.BrowserTokenEnv+" when set ("+bootstrap.ServeBrowserBrokerMarker+")")
}

func newServeBootstrap() (*serve.Broadcaster, *serve.SessionTagSink, *config.Config) {
	bc := serve.NewBroadcaster()
	cfg, _ := config.Load()
	return bc, serve.NewSessionTagSink(bc), cfg
}

func setupCLIMultiSessionProfile(ctx context.Context, model string, maxSteps int, tag *serve.SessionTagSink, overrides cliBuildOverrides) (*control.Controller, boot.Options, error) {
	broker, err := serveBrowserBrokerFromEnv(os.Getenv)
	if err != nil {
		return nil, boot.Options{}, err
	}
	opts := cliProfileBuildOptions(model, maxSteps, false, tag, overrides)
	if broker != nil {
		// The initial controller's tools go through the session-scoped view;
		// the raw broker is what SetControllerBuildOptions keeps so later
		// controllers get their own session scope.
		opts.BrowserExecutor = broker.ForSession(tag)
	}
	ctrl, err := boot.Build(ctx, opts)
	if broker != nil {
		opts.BrowserExecutor = broker
	}
	return ctrl, opts, err
}

func newCLIMultiSessionServer(ctrl *control.Controller, bc *serve.Broadcaster, tag *serve.SessionTagSink, cfg config.ServeConfig, buildOpts boot.Options) *serve.Server {
	// Exclusive identities have no legacy path; without the session id the
	// boot tag stamps live frames path-less and identity-routed subscribers
	// cannot attribute them after the first rebind.
	if ref, bound := ctrl.SessionRef(); bound {
		tag.SetIdentity(ref.SessionID)
	}
	srv := serve.New(ctrl, bc, cfg)
	srv.SetControllerBuildOptions(buildOpts)
	srv.RegisterSessionTag(ctrl, tag)
	return srv
}
