package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"

	"deepzora/internal/config"
	"deepzora/internal/session"
)

func resumeTargetService(target cliResumeTarget) *session.Service {
	if target.service != nil {
		return target.service
	}
	return cliSessionService(resolveCLISessionDir())
}

func applyResumeTargetModel(model *string, target cliResumeTarget, cfg *config.Config) error {
	if strings.TrimSpace(*model) != "" || target.empty() {
		return nil
	}
	service := resumeTargetService(target)
	if service == nil {
		return errors.New("session service unavailable")
	}
	snapshot, err := service.Query().Snapshot(context.Background(), target.ref)
	if err != nil {
		return err
	}
	selection := snapshot.Projection.ModelRef
	if selection == "" {
		return nil
	}
	if cfg != nil {
		selection, err = cfg.ResolveSavedModel(selection, snapshot.Projection.ModelIdentity)
		if err != nil {
			return err
		}
	}
	*model = selection
	return nil
}

func copyResumeTarget(model string, target cliResumeTarget, cfg *config.Config) (cliResumeTarget, error) {
	if target.empty() {
		return cliResumeTarget{}, errors.New("copy requires a session identity")
	}
	if err := applyResumeTargetModel(&model, target, cfg); err != nil {
		return cliResumeTarget{}, err
	}
	service := resumeTargetService(target)
	if service == nil {
		return cliResumeTarget{}, errors.New("session service unavailable")
	}
	result, err := service.CopySession(context.Background(), session.CopyRequest{Source: target.ref, OperationID: "cli-copy:" + rand.Text()})
	if err != nil {
		return cliResumeTarget{}, err
	}
	return cliResumeTarget{ref: result.Child, service: service}, nil
}
