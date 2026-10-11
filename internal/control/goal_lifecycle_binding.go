package control

import (
	"encoding/json"
	"strings"

	goaldomain "deepzora/internal/goal"
	"deepzora/internal/session"
)

// goalLifecycleFromProjection restores only the current goal document.
func goalLifecycleFromProjection(raw json.RawMessage) (*goaldomain.Machine, error) {
	machine := goaldomain.NewMachine(nil, nil)
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return machine, nil
	}
	_, err := machine.Restore(raw)
	return machine, err
}

func (c *Controller) installGoalLifecycle(runtime *session.Runtime) {
	machine := goaldomain.NewMachine(nil, nil)
	var loadErr error
	if runtime != nil && runtime.Session() != nil {
		snapshot := runtime.Session().ExecutionSnapshot()
		machine, loadErr = goalLifecycleFromProjection(snapshot.Projection.GoalState)
	}
	c.goalLifecycleMu.Lock()
	c.goalLifecycle = machine
	c.goalLifecycleLoadErr = loadErr
	c.goalLifecycleMu.Unlock()
}

func (c *Controller) goalLifecycleView() (*goaldomain.View, error) {
	if c == nil {
		return nil, nil
	}
	c.goalLifecycleMu.RLock()
	machine, loadErr := c.goalLifecycle, c.goalLifecycleLoadErr
	c.goalLifecycleMu.RUnlock()
	if loadErr != nil {
		return nil, loadErr
	}
	if machine == nil {
		return nil, nil
	}
	return machine.Get(), nil
}
