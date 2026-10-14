package main

import (
	"sync"
	"testing"

	"deepzora/internal/boot"
	"deepzora/internal/control"
	"deepzora/internal/extension"
	"deepzora/internal/skill/skillwatch"
)

func TestRuntimeDoctorEmptyApp(t *testing.T) {
	var a *App
	report := a.RuntimeDoctor()
	if report.Text == "" {
		t.Fatal("expected doctor text")
	}
	// Nil app keeps process metrics and allows resume without claiming rollback.
	if !report.AllowResume {
		t.Fatal("empty process should allow resume")
	}
	if report.CleanRollback || report.PublishedGen != 0 {
		t.Fatalf("empty app inherited runtime recovery evidence: %+v", report)
	}
}

func TestRuntimeDoctorIncludesSkillWatchResources(t *testing.T) {
	ctrl := &control.Controller{}
	watch := skillwatch.NewService(skillwatch.Options{})
	t.Cleanup(func() { _ = watch.Close() })
	tab := &WorkspaceTab{ID: "tab-watch", Ctrl: ctrl}
	a := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	a.setTabLastBuildResult(tab, &boot.BuildResult{Controller: ctrl, Owner: extension.NewRuntimeOwner(), SkillWatchService: watch})
	report := a.RuntimeDoctor()
	if report.SkillWatch == nil {
		t.Fatal("skill watch diagnostics missing")
	}
	if report.SkillWatch.PhysicalWatches != 0 || report.SkillWatch.LogicalSubscriptions != 0 {
		t.Fatalf("unexpected idle skill watch counters: %+v", report.SkillWatch)
	}
}

func TestRuntimeDoctorConcurrentWithBuildResultUpdate(t *testing.T) {
	ctrl := &control.Controller{}
	tab := &WorkspaceTab{ID: "tab-1", Ctrl: ctrl}
	a := &App{
		tabs:        map[string]*WorkspaceTab{tab.ID: tab},
		activeTabID: tab.ID,
	}
	first := &boot.BuildResult{Controller: ctrl, Owner: extension.NewRuntimeOwner()}
	second := &boot.BuildResult{Controller: ctrl, Owner: extension.NewRuntimeOwner()}
	a.setTabLastBuildResult(tab, first)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		for range 100 {
			a.setTabLastBuildResult(tab, second)
			a.setTabLastBuildResult(tab, first)
		}
	})
	close(start)
	for range 100 {
		report := a.RuntimeDoctor()
		if report.Text == "" {
			t.Fatal("expected doctor text")
		}
	}
	wg.Wait()
}

func TestTabBuildResultMustMatchCurrentController(t *testing.T) {
	current := &control.Controller{}
	stale := &control.Controller{}
	tab := &WorkspaceTab{ID: "tab-1", Ctrl: current}
	a := &App{
		tabs:        map[string]*WorkspaceTab{tab.ID: tab},
		activeTabID: tab.ID,
	}
	staleResult := &boot.BuildResult{Controller: stale, Owner: extension.NewRuntimeOwner()}
	a.setTabLastBuildResult(tab, staleResult)
	if got := a.tabBuildResultForController(tab, current); got != nil {
		t.Fatal("stale build result was reused for a replacement controller")
	}

	currentResult := &boot.BuildResult{Controller: current, Owner: extension.NewRuntimeOwner()}
	a.setTabLastBuildResult(tab, currentResult)
	if got := a.tabBuildResultForController(tab, current); got != currentResult {
		t.Fatal("current controller build result was not reused")
	}
}
