package boot

import (
	"testing"

	"deepzora/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.RunWithIsolatedUserState(m)
}
