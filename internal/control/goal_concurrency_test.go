package control

import (
	"sync"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
)

// Current Goal updates serialize through the host event log while runtime
// readers remain independent. Repeating one goal must preserve its identity.
func TestGoalStateWritesAreConcurrencySafe(t *testing.T) {
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := newCurrentOwnedTestController(t, Options{Executor: exec, Label: "test"})
	c.SetGoal("concurrent goal")

	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = c.Running()              // takes c.mu
				_ = c.RuntimeStateSnapshot() // takes c.mu
				_ = c.Goal()                 // takes c.mu
				_ = c.GoalStatus()           // takes c.mu
			}
		}
	})

	var writers sync.WaitGroup
	for w := range 8 {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for range 10 {
				c.SetGoal("concurrent goal")
			}
		}(w)
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	projection := loadDurableSessionProjection(t, c)
	machine, err := goalLifecycleFromProjection(projection.GoalState)
	if err != nil {
		t.Fatal(err)
	}
	view := machine.Get()
	if view == nil || view.Objective != "concurrent goal" {
		t.Fatalf("current durable goal = %+v", view)
	}
}
