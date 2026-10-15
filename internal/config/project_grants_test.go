package config

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestProjectGrantUpdateQueuesLocalWritersWithoutExpiringTheirExternalBudget(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- NewProjectGrantStore(home).Update(workspace, func(g ProjectGrant) (ProjectGrant, error) {
			close(firstEntered)
			<-releaseFirst
			g.Allow = append(g.Allow, "Read(src/*)")
			return g, nil
		})
	}()
	select {
	case <-firstEntered:
	case <-time.After(10 * time.Second):
		close(releaseFirst)
		t.Fatal("first writer did not enter its transaction")
	}

	// A second store instance must join the same path's queue. Keep the first
	// transaction open beyond the external lock's five-second budget, without
	// depending on disk speed or the scheduler to create contention.
	secondDone := make(chan error, 1)
	secondStarted := make(chan struct{})
	extraDir := filepath.Join(workspace, "output")
	go func() {
		close(secondStarted)
		secondDone <- NewProjectGrantStore(home).Update(workspace, func(g ProjectGrant) (ProjectGrant, error) {
			g.AllowWrite = append(g.AllowWrite, extraDir)
			return g, nil
		})
	}()
	<-secondStarted
	select {
	case err := <-secondDone:
		close(releaseFirst)
		<-firstDone
		t.Fatalf("queued writer returned before the local transaction finished: %v", err)
	case <-time.After(6 * time.Second):
	}
	close(releaseFirst)
	for name, done := range map[string]<-chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s writer: %v", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s writer did not finish after local release", name)
		}
	}
	grant, err := NewProjectGrantStore(home).Grant(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(grant.Allow, []string{"Read(src/*)"}) || !slices.Equal(grant.AllowWrite, []string{extraDir}) {
		t.Fatalf("serialized updates lost grant data: %+v", grant)
	}
}
