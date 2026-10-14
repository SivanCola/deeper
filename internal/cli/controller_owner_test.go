package cli

import (
	"deepzora/internal/control"
	"testing"
)

func newOwnedTestController(t testing.TB, options control.Options) *control.Controller {
	t.Helper()
	controller := control.New(options)
	ownTestController(t, controller)
	return controller
}

func ownTestController(t testing.TB, controller *control.Controller) {
	t.Helper()
	t.Cleanup(func() {
		controller.Close()
		// Close requests teardown; Closed joins the final persistence work and
		// releases stores before t.TempDir removes their files (notably Windows).
		<-controller.Closed()
	})
}
