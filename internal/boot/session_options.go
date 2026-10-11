package boot

import (
	"deepzora/internal/control"
	"errors"
)

func (opts *Options) inheritSessionBinding(old *control.Controller) {
	opts.SessionCreateService = old.SessionCreationService()

	if service, runtime, ok := old.SessionBinding(); ok {
		opts.SessionService = service
		opts.SessionRuntime = runtime
		opts.SessionHostID = runtime.Ref().HostID
	}
}

func (opts Options) validateSessionBinding() error {

	if opts.SessionRuntime != nil && opts.SessionService == nil {
		return errors.New("v3 session runtime requires a session service")
	}
	return nil
}
