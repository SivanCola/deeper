package main

import (
	"deepzora/internal/config"
)

// upsertDotEnv stores KEY=value in Deepzora's global .env and applies it to the
// running process so a rebuild picks it up without a restart.
func upsertDotEnv(key, value string) error {
	_, err := config.SetCredential(key, value)
	return err
}

func removeDotEnv(key string) error {
	return config.RemoveCredential(key)
}
