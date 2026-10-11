package boot

import (
	"deepzora/internal/config"
	"deepzora/internal/provider"
	"fmt"
)

func runtimeModelContinuationReader(root, modelName string, old *config.Config, settings *config.ModelRuntimeSettings, externalResolvers ...provider.Resolver) func() error {
	return func() error {
		for _, resolver := range externalResolvers {
			if resolver != nil {
				return fmt.Errorf("external model routes cannot verify continued credential access; apply the new settings first")
			}
		}
		current, err := config.LoadModelRuntimeSnapshot(root, modelName)
		if err != nil {
			return err
		}
		if err := settings.Apply(current, root); err != nil {
			return err
		}
		return config.ValidateModelRuntimeContinuation(old, current)
	}
}

func runtimeModelSettingsReader(root, modelName, modelRef string, settings *config.ModelRuntimeSettings) func() (string, error) {
	return func() (string, error) {
		current, err := config.LoadModelRuntimeSnapshot(root, modelName)
		if err != nil {
			return "", err
		}
		if err := settings.Apply(current, root); err != nil {
			return "", err
		}
		return current.ModelRuntimeFingerprint(modelRef), nil
	}
}

func runtimeImageCapabilityReader(root, modelName, snapshot string, settings *config.ModelRuntimeSettings) func() bool {
	return func() bool {
		current, err := config.LoadForRootReadOnly(root)
		if err == nil {
			err = settings.Apply(current, root)
		}
		if err != nil {
			return false
		}
		return config.ModelCapabilitySnapshot(current, config.NewModelCapabilityResolver()) != snapshot
	}
}
