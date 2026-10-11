package config

import (
	"deepzora/internal/testenv"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("DEEPZORA_CONFIG_LOCK_HELPER") == "1" {
		os.Exit(m.Run())
	}
	testenv.RunWithIsolatedUserState(m)
}
