package boot

import (
	"deepzora/internal/control"
	"errors"
)

func (opts *Options) inheritSessionBinding(old *control.Controller) {
	opts.SessionCreateService = old.SessionCreationService()
	if old.UsesExclusiveSession() {
		opts.SessionService = old.SessionService()
	}

	if service, runtime, ok := old.SessionBinding(); ok {
		opts.SessionService = service
		opts.SessionRuntime = runtime
	}
}

func (opts Options) validateSessionBinding() error {
	if opts.SessionService == nil {
		return errors.New("runtime assembly requires a host-owned session service")
	}
	if runtime := opts.SessionRuntime; runtime != nil {
		if owned, ok := opts.SessionService.Runtime(runtime.Ref()); !ok || owned != runtime {
			return errors.New("runtime assembly requires the exact runtime published by its session service")
		}
	}
	return nil
}
