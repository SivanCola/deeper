package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/event"
	"deepzora/internal/jobs"
	"deepzora/internal/session"
	"deepzora/internal/subagentpersist"
)

func TestTaskMachineListUsesContentFreePersistedMetadata(t *testing.T) {
	identityKey := installMachineTestIdentity(t)
	dir := t.TempDir()
	path := saveMachineTestSession(t, dir, "session")
	manager := jobs.NewManager(event.Discard)
	manager.BindSession("session", path)
	job := manager.StartForSession("session", "task", "PRIVATE TASK LABEL", func(context.Context, io.Writer) (string, error) {
		return "PRIVATE TASK OUTPUT", nil
	})
	manager.WaitForSession(context.Background(), "session", []string{job.ID}, 1)
	manager.Close()

	var out bytes.Buffer
	if code := runTaskCommand([]string{"list", "--json", "--dir", dir}, &out); code != 0 {
		t.Fatalf("task list exit code = %d, output = %s", code, out.String())
	}
	var response machineTaskList
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode task list: %v", err)
	}
	if len(response.Tasks) != 1 || response.Tasks[0].ID != job.ID || response.Tasks[0].Status != "done" {
		t.Fatalf("tasks = %+v", response.Tasks)
	}
	if response.Tasks[0].Kind != "background" || response.Tasks[0].SessionID != machineSessionIDWithKey("session", identityKey) {
		t.Fatalf("task projection = %+v", response.Tasks[0])
	}
	if !response.Tasks[0].ArtifactComplete {
		t.Fatalf("persisted task artifact should be complete: %+v", response.Tasks[0])
	}
	if strings.Contains(out.String(), "PRIVATE") || strings.Contains(out.String(), dir) {
		t.Fatalf("task output leaked private data: %s", out.String())
	}

	if err := os.Remove(filepath.Join(jobs.ArtifactDir(path), job.ID+".log")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runTaskCommand([]string{"list", "--json", "--dir", dir}, &out); code != 0 {
		t.Fatalf("task list after artifact removal exit code = %d, output = %s", code, out.String())
	}
	response = machineTaskList{}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode task list after artifact removal: %v", err)
	}
	if len(response.Tasks) != 1 || response.Tasks[0].ArtifactComplete {
		t.Fatalf("task projection after artifact removal = %+v", response.Tasks)
	}
}

func TestTaskMachineProjectsSubagentLifecycleAndArtifactCompleteness(t *testing.T) {
	identityKey := installMachineTestIdentity(t)
	dir := t.TempDir()
	saveMachineTestSession(t, dir, "session")
	children, err := cliSessionService(dir).Subagents()
	if err != nil {
		t.Fatal(err)
	}
	transcripts := subagentpersist.New(children)
	subDir := transcripts.Root()
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	metas := []agent.SubagentMeta{
		{Ref: "sa_running", CreatedAt: now, UpdatedAt: now, Status: agent.SubagentRunning, Kind: "task", ParentSession: "session"},
		{Ref: "sa_complete", CreatedAt: now.Add(-time.Minute), UpdatedAt: now, Status: agent.SubagentCompleted, Kind: "task", ParentSession: "session"},
		{Ref: "sa_missing", CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now, Status: agent.SubagentCompleted, Kind: "task", ParentSession: "session"},
	}
	for _, meta := range metas {
		data, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subDir, meta.Ref+".meta.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	complete, err := transcripts.Create(t.Context(), metas[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := complete.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	tasks, err := machineTasks(dir, machineSessionIDWithKey("session", identityKey), identityKey)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]machineTask, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	if got := byID["sa_running"]; got.Status != string(agent.SubagentInterrupted) || got.FinishedAt != "" || got.ArtifactComplete {
		t.Fatalf("stale running projection = %+v", got)
	}
	if got := byID["sa_complete"]; got.Status != string(agent.SubagentCompleted) || got.FinishedAt == "" || !got.ArtifactComplete {
		t.Fatalf("completed projection = %+v", got)
	}
	if got := byID["sa_missing"]; got.FinishedAt == "" || got.ArtifactComplete {
		t.Fatalf("missing artifact projection = %+v", got)
	}

	service := testCLISessionService(t, dir)
	binding, err := service.Open(t.Context(), session.SessionRef{HostID: "local", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release(t.Context())
	tasks, err = machineTasks(dir, machineSessionIDWithKey("session", identityKey), identityKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == "sa_running" && (task.Status != string(agent.SubagentRunning) || task.FinishedAt != "" || task.ArtifactComplete) {
			t.Fatalf("live running projection = %+v", task)
		}
	}
}

func TestTaskMachineProjectRootUsesProjectStore(t *testing.T) {
	identityKey := installMachineTestIdentity(t)
	projectRoot := t.TempDir()
	sessionDir := config.ProjectSessionDir(projectRoot)
	path := saveMachineTestSession(t, sessionDir, "session")
	manager := jobs.NewManager(event.Discard)
	manager.BindSession("session", path)
	job := manager.StartForSession("session", "task", "PRIVATE TASK LABEL", func(context.Context, io.Writer) (string, error) {
		return "PRIVATE TASK OUTPUT", nil
	})
	manager.WaitForSession(context.Background(), "session", []string{job.ID}, 1)
	manager.Close()

	var out bytes.Buffer
	if code := runTaskCommand([]string{"list", "--json", "--project-root", projectRoot}, &out); code != 0 {
		t.Fatalf("task list exit code = %d, output = %s", code, out.String())
	}
	var response machineTaskList
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode task list: %v", err)
	}
	if len(response.Tasks) != 1 || response.Tasks[0].ID != job.ID || response.Tasks[0].SessionID != machineSessionIDWithKey("session", identityKey) {
		t.Fatalf("tasks = %+v, want project task", response.Tasks)
	}
}

func TestTaskMachineShowRequiresNonZeroForMissingTask(t *testing.T) {
	installMachineTestIdentity(t)
	dir := t.TempDir()
	var out bytes.Buffer
	if code := runTaskCommand([]string{"show", "--json", "missing", "--dir", dir}, &out); code != 1 {
		t.Fatalf("exit code = %d, output = %s", code, out.String())
	}
	var response machineErrorResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if response.Error.Code != "task_not_found" {
		t.Fatalf("response = %+v", response)
	}
}

func TestTaskMachineEmptyListUsesAnArray(t *testing.T) {
	installMachineTestIdentity(t)
	var out bytes.Buffer
	if code := runTaskCommand([]string{"list", "--json", "--dir", t.TempDir()}, &out); code != 0 {
		t.Fatalf("task list exit code = %d, output = %s", code, out.String())
	}
	var response machineTaskList
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Tasks == nil {
		t.Fatalf("tasks must be [] in empty response: %s", out.String())
	}
}

func TestTaskMachineRejectsConflictingSessionSources(t *testing.T) {
	installMachineTestIdentity(t)
	dir := t.TempDir()
	var out bytes.Buffer
	if code := runTaskCommand([]string{"list", "--json", "--dir", dir, "--project-root", dir}, &out); code != 2 {
		t.Fatalf("exit code = %d, output = %s", code, out.String())
	}
	var response machineErrorResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "invalid_argument" {
		t.Fatalf("response = %+v", response)
	}
}
