package evidence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLedgerRecordsSuccessAndFailureReceipts(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(Receipt{
		ToolName: "bash",
		Args:     json.RawMessage(`{"command":"go test ./..."}`),
		Success:  true,
		Command:  "go test ./...",
	})
	ledger.Record(Receipt{
		ToolName: "bash",
		Args:     json.RawMessage(`{"command":"go test ./internal/..."}`),
		Success:  false,
		Command:  "go test ./internal/...",
	})

	if !ledger.HasSuccessfulCommand("go test ./...") {
		t.Fatal("successful bash command should verify")
	}
	if ledger.HasSuccessfulCommand("go test ./internal/...") {
		t.Fatal("failed bash command must not verify")
	}
}

func TestLedgerDistinguishesWorkflowSpecificMutation(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(ReceiptFromToolCall("remember", json.RawMessage(`{"body":"ORBIT-42"}`), true, false))
	if !ledger.HasSuccessfulToolReceipt("remember") {
		t.Fatal("successful remember receipt was not found")
	}
	if ledger.HasSuccessfulMutationOtherThan("remember") {
		t.Fatal("remember-only mutation was treated as an unrelated workspace mutation")
	}
	ledger.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"internal/a.go"}`), true, false))
	if !ledger.HasSuccessfulMutationOtherThan("remember") {
		t.Fatal("workspace mutation was hidden by the remember allowance")
	}
}

func TestLedgerMatchesFreshSuccessfulReviewReceipt(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(ReceiptFromToolCall("review", json.RawMessage(`{"task":"review changes"}`), true, true))
	if !ledger.HasCompletedReview() {
		t.Fatal("successful dedicated review tool should verify review evidence")
	}
	legacy := NewLedger()
	legacy.Record(ReceiptFromToolCall("task", json.RawMessage(`{"profile":"review"}`), true, true))
	if !legacy.HasCompletedReview() {
		t.Fatal("successful task(profile=review) should verify review evidence")
	}
	failed := NewLedger()
	failed.Record(ReceiptFromToolCall("task", json.RawMessage(`{"profile":"review"}`), false, true))
	if failed.HasCompletedReview() {
		t.Fatal("failed review task must not verify review evidence")
	}
	background := NewLedger()
	background.Record(ReceiptFromToolCall("task", json.RawMessage(`{"profile":"review","run_in_background":true}`), true, true))
	if background.HasCompletedReview() {
		t.Fatal("starting a background review must not count as a completed review")
	}
}

func TestLedgerRejectsReviewBeforeLatestMutation(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(ReceiptFromToolCall("review", json.RawMessage(`{"task":"review changes"}`), true, true))
	ledger.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"changed.go"}`), true, false))
	if ledger.HasCompletedReview() {
		t.Fatal("review evidence before the latest mutation must be stale")
	}
}

func TestLedgerRequiresReviewCoverageAfterMutation(t *testing.T) {
	fresh := NewLedger()
	fresh.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"changed.go"}`), true, false))
	fresh.Record(ReceiptFromToolCall("read_file", json.RawMessage(`{"path":"changed.go"}`), true, true))
	fresh.Record(ReceiptFromToolCall("review", json.RawMessage(`{"task":"review changes"}`), true, true))
	if !fresh.HasCompletedReview() {
		t.Fatal("review after reading the latest changed path should count")
	}

	unrelated := NewLedger()
	unrelated.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"changed.go"}`), true, false))
	unrelated.Record(ReceiptFromToolCall("read_file", json.RawMessage(`{"path":"other.go"}`), true, true))
	unrelated.Record(ReceiptFromToolCall("review", json.RawMessage(`{"task":"review something else"}`), true, true))
	if unrelated.HasCompletedReview() {
		t.Fatal("review that did not inspect the latest changed path must not count")
	}
}

func TestLedgerHostReviewCoverageRequiresContentForEveryPath(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"internal/a.go"}`), true, false))
	ledger.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"internal/b.go"}`), true, false))
	mutation, ok := ledger.LatestSuccessfulMutationIndex()
	if !ok {
		t.Fatal("missing mutation index")
	}
	ledger.Record(ReceiptFromToolCall("read_file", json.RawMessage(`{"path":"internal/a.go"}`), true, true))
	if ledger.HasHostReviewCoverageAfter(mutation, []string{"internal/a.go", "internal/b.go"}) {
		t.Fatal("one path read must not cover a two-path change set")
	}
	ledger.Record(ReceiptFromToolCall("read_file", json.RawMessage(`{"path":"internal/b.go"}`), true, true))
	if !ledger.HasHostReviewCoverageAfter(mutation, []string{"internal/a.go", "internal/b.go"}) {
		t.Fatal("fresh reads of every changed path should prove host review coverage")
	}

	diff := NewLedger()
	diff.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"internal/a.go"}`), true, false))
	diffMutation, ok := diff.LatestSuccessfulMutationIndex()
	if !ok {
		t.Fatal("missing diff mutation index")
	}
	diff.Record(Receipt{ToolName: "bash", Success: true, Command: "git diff", OutputBytes: 200})
	if !diff.HasHostReviewCoverageAfter(diffMutation, []string{"internal/a.go", "internal/b.go"}) {
		t.Fatal("an output-producing whole git diff should cover the current change set")
	}
	for _, command := range []string{"git status --short", "git diff --check"} {
		insufficient := NewLedger()
		insufficient.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"internal/a.go"}`), true, false))
		idx, ok := insufficient.LatestSuccessfulMutationIndex()
		if !ok {
			t.Fatal("missing insufficient mutation index")
		}
		insufficient.Record(Receipt{ToolName: "bash", Success: true, Command: command, OutputBytes: 200})
		if insufficient.HasHostReviewCoverageAfter(idx, []string{"internal/a.go"}) {
			t.Fatalf("%q must not count as content review", command)
		}
	}
}

func TestLedgerAcceptsCollectedStructuredReviewReport(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"changed.go"}`), true, false))
	ledger.Record(Receipt{ToolName: "review_report", Success: true, Args: json.RawMessage(`{
		"kind":"review",
		"verdict":"block",
		"reviewed_paths":["changed.go"],
		"findings":[{"severity":"critical","summary":"must fix","path":"changed.go"}]
	}`)})
	if !ledger.HasCompletedReview() {
		t.Fatal("a structured report completes the review activity even when its verdict requires follow-up")
	}
}

func TestLedgerHasWriteOrCommandSince(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(Receipt{ToolName: "todo_write", Success: true, Todos: []TodoItem{{Content: "edit", Status: "in_progress"}}})
	ledger.Record(Receipt{ToolName: "write_file", Success: true, Write: true, Paths: []string{"a.go"}})
	ledger.Record(Receipt{ToolName: "bash", Success: false, Command: "go test ./..."})
	ledger.Record(Receipt{ToolName: "todo_write", Success: true, Todos: []TodoItem{{Content: "edit", Status: "completed"}}})

	if got := ledger.Len(); got != 4 {
		t.Fatalf("Len() = %d, want 4", got)
	}
	if !ledger.HasWriteOrCommandSince(0) {
		t.Fatal("write receipt at index 1 should count from index 0")
	}
	if !ledger.HasWriteOrCommandSince(-1) {
		t.Fatal("negative index should behave like 0")
	}
	if ledger.HasWriteOrCommandSince(2) {
		t.Fatal("failed command and bookkeeping receipts must not count as progress")
	}
	ledger.Record(Receipt{ToolName: "bash", Success: true, Command: "go test ./..."})
	if !ledger.HasWriteOrCommandSince(2) {
		t.Fatal("successful command receipt after index should count as progress")
	}
	var nilLedger *Ledger
	if nilLedger.HasWriteOrCommandSince(0) {
		t.Fatal("nil ledger must report no progress")
	}
}

func TestLedgerMatchesFileReadAndWriteReceipts(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(Receipt{ToolName: "read_file", Success: true, Paths: []string{`internal/tool/builtin/completestep.go`}, Read: true})
	ledger.Record(Receipt{ToolName: "write_file", Success: true, Paths: []string{`internal/evidence/evidence.go`}, Write: true})
	ledger.Record(Receipt{ToolName: "edit_file", Success: false, Paths: []string{`failed.go`}, Write: true})

	if !ledger.HasSuccessfulReadOrWrite([]string{`internal\tool\builtin\completestep.go`}) {
		t.Fatal("read receipt should verify the same path across separators")
	}
	if !ledger.HasSuccessfulWrite([]string{`internal/evidence/evidence.go`}) {
		t.Fatal("write receipt should verify written path")
	}
	if ledger.HasSuccessfulWrite([]string{`failed.go`}) {
		t.Fatal("failed write receipt must not verify")
	}
}

func TestLedgerReportsFinalReadinessReceiptsAfterWriter(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(Receipt{ToolName: "bash", Success: true, Command: "go test ./..."})
	ledger.Record(Receipt{ToolName: "write_file", Success: true, Paths: []string{"changed.go"}, Write: true})
	ledger.Record(Receipt{ToolName: "bash", Success: false, Command: "git diff --check"})
	ledger.Record(Receipt{ToolName: "todo_write", Success: true, Todos: []TodoItem{{Content: "Edit code", Status: "in_progress"}}})

	writer, ok := ledger.LatestSuccessfulWriterIndex()
	if !ok {
		t.Fatal("expected latest successful writer")
	}
	if ledger.HasSuccessfulCommandAfter("go test ./...", writer) {
		t.Fatal("command before latest writer must not satisfy final readiness")
	}
	if ledger.HasSuccessfulCommandAfter("git diff --check", writer) {
		t.Fatal("failed command must not satisfy final readiness")
	}
	if !ledger.HasSuccessfulTodoWrite() {
		t.Fatal("successful todo_write receipt should be reported")
	}

	ledger.Record(Receipt{ToolName: "bash", Success: true, Command: "git diff --check"})
	ledger.Record(Receipt{ToolName: "todo_write", Success: true, Todos: []TodoItem{{Content: "Edit code", Status: "completed"}}})
	if !ledger.HasSuccessfulCommandAfter("git diff --check", writer) {
		t.Fatal("command after latest writer should satisfy final readiness")
	}
}

func TestLedgerResetClearsTurnReceipts(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(Receipt{ToolName: "bash", Success: true, Command: "go test ./..."})

	ledger.Reset()

	if ledger.HasSuccessfulCommand("go test ./...") {
		t.Fatal("reset should clear prior-turn evidence")
	}
}

func TestContextCarriesLedger(t *testing.T) {
	ledger := NewLedger()
	ctx := WithLedger(context.Background(), ledger)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("ledger missing from context")
	}
	if got != ledger {
		t.Fatal("context returned a different ledger")
	}
}

func TestReceiptFromToolCallExtractsEvidenceFields(t *testing.T) {
	bash := ReceiptFromToolCall("bash", json.RawMessage(`{"command":"git diff --check"}`), true, false)
	if bash.Command != "git diff --check" {
		t.Fatalf("bash command = %q", bash.Command)
	}
	if bash.Write {
		t.Fatal("bash should not be treated as a verified file writer")
	}

	write := ReceiptFromToolCall("write_file", json.RawMessage(`{"path":"internal/evidence/evidence.go","content":"x"}`), true, false)
	if !write.Write || len(write.Paths) != 1 || write.Paths[0] != `internal/evidence/evidence.go` {
		t.Fatalf("write receipt not extracted: %+v", write)
	}

	read := ReceiptFromToolCall("read_file", json.RawMessage(`{"path":"internal/tool/builtin/completestep.go"}`), true, true)
	if !read.Read || len(read.Paths) != 1 {
		t.Fatalf("read receipt not extracted: %+v", read)
	}

	glob := ReceiptFromToolCall("glob", json.RawMessage(`{"pattern":"**/*.go"}`), true, true)
	if !glob.Read {
		t.Fatalf("generic read-only tool should be treated as read context: %+v", glob)
	}
}

func TestReceiptFromToolCallExtractsTodoWriteItems(t *testing.T) {
	receipt := ReceiptFromToolCall("todo_write", json.RawMessage(`{"todos":[
		{"content":"Add parser","status":"in_progress"},
		{"content":"Wire parser","status":"in_progress"}
	]}`), true, true)

	if len(receipt.Todos) != 2 {
		t.Fatalf("todos not extracted: %+v", receipt)
	}
	if receipt.Todos[0].Content != "Add parser" || receipt.Todos[0].Status != "in_progress" {
		t.Fatalf("first todo not extracted: %+v", receipt.Todos[0])
	}
	if receipt.Todos[1].Content != "Wire parser" || receipt.Todos[1].Status != "in_progress" {
		t.Fatalf("concurrent in-progress todo not extracted: %+v", receipt.Todos[1])
	}
}

func TestRetiredStepProofCannotAdvanceGoalOrSurviveCheckpoint(t *testing.T) {
	var receipt Receipt
	if err := json.Unmarshal([]byte(`{"tool_name":"complete_step","success":true,"step":"done","step_proof":true,"todo_step":{"Found":true},"args":{"result":"claimed done","evidence":[{"kind":"verification","command":"go test ./..."}]}}`), &receipt); err != nil {
		t.Fatal(err)
	}
	ledger := NewLedger()
	ledger.Record(receipt)
	if signatures := ledger.SuccessfulProgressSignaturesSince(0); len(signatures) != 0 {
		t.Fatalf("retired signoff advanced Goal progress: %v", signatures)
	}
	checkpoint := ledger.FinalReadinessCheckpoint()
	if len(checkpoint.Receipts) != 1 || len(checkpoint.Receipts[0].Args) != 0 {
		t.Fatalf("retired proof arguments retained: %+v", checkpoint)
	}
	body, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"step"`, `"step_proof"`, `"todo_step"`} {
		if strings.Contains(string(body), field) {
			t.Fatalf("retired field %s survived: %s", field, body)
		}
	}
}

func TestSuccessfulProgressSignaturesIgnoreExactRepeatsAndTrackTodoClear(t *testing.T) {
	ledger := NewLedger()
	read := ReceiptFromToolCall("read_file", json.RawMessage(`{"path":"a.go"}`), true, true)
	read.OutputBytes = 10
	ledger.Record(read)
	ledger.Record(read)
	ledger.Record(ReceiptFromToolCall("todo_write", json.RawMessage(`{"todos":[]}`), true, true))
	ledger.Record(ReceiptFromToolCall("edit_file", json.RawMessage(`{"path":"a.go","old_string":"a","new_string":"b"}`), true, false))
	ledger.Record(ReceiptFromToolCall("bash", json.RawMessage(`{"command":"go test ./..."}`), true, false))

	sigs := ledger.SuccessfulProgressSignaturesSince(0)
	if len(sigs) != 5 {
		t.Fatalf("progress signatures = %d, want two reads plus todo clear, mutation, and command", len(sigs))
	}
	if sigs[0] != sigs[1] {
		t.Fatalf("exact repeated reads should have the same signature: %q != %q", sigs[0], sigs[1])
	}
	if sigs[1] == sigs[2] || sigs[2] == sigs[3] || sigs[3] == sigs[4] {
		t.Fatalf("distinct host work collapsed to one signature: %v", sigs)
	}
}

func TestToolCallMutatesForDeliveryProfile(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     string
		readOnly bool
		want     bool
	}{
		{name: "trusted reader", toolName: "read_file", args: `{"path":"a.go"}`, readOnly: true},
		{name: "file writer", toolName: "edit_file", args: `{"path":"a.go"}`, want: true},
		{name: "delegated task meta", toolName: "task", args: `{"prompt":"fix it"}`},
		{name: "run_skill meta", toolName: "run_skill", args: `{"name":"review"}`},
		{name: "review meta", toolName: "review", args: `{"task":"review changes"}`},
		{name: "security_review meta", toolName: "security_review", args: `{"task":"security"}`},
		{name: "use_capability meta", toolName: "use_capability", args: `{"action":"inspect","capability_id":"mcp-server:github"}`},
		{name: "test command", toolName: "bash", args: `{"command":"go test ./..."}`},
		{name: "npx vitest pipeline", toolName: "bash", args: `{"command":"npx vitest run src/lib/foo.test.ts 2>&1 | tail -40"}`},
		{name: "node syntax check", toolName: "bash", args: `{"command":"node --check app.js"}`},
		{name: "node syntax check pipeline", toolName: "bash", args: `{"command":"tail -n +2 app.html | head -n 20 | node --check"}`},
		{name: "node eval stays opaque", toolName: "bash", args: `{"command":"node -e 'console.log(1)'"}`, want: true},
		{name: "node conditions flag stays opaque", toolName: "bash", args: `{"command":"node -C production server.js"}`, want: true},
		{name: "node test runner", toolName: "bash", args: `{"command":"node --test"}`},
		{name: "node test snapshot update stays opaque", toolName: "bash", args: `{"command":"node --test --test-update-snapshots"}`, want: true},
		{name: "node test reporter file stays opaque", toolName: "bash", args: `{"command":"node --test --test-reporter=junit --test-reporter-destination=result.txt"}`, want: true},
		{name: "node test rerun state stays opaque", toolName: "bash", args: `{"command":"node --test --test-rerun-failures=state.json"}`, want: true},
		{name: "node test cpu profile stays opaque", toolName: "bash", args: `{"command":"node --test --cpu-prof"}`, want: true},
		{name: "diff review", toolName: "bash", args: `{"command":"git diff --check"}`},
		{name: "PowerShell network probe does not write workspace", toolName: "bash", args: `{"command":"Test-NetConnection -ComputerName example.com -Port 443"}`},
		{name: "resolved read-only substitution", toolName: "bash", args: `{"command":"basename \"$(pwd)\""}`},
		{name: "writer in substitution", toolName: "bash", args: `{"command":"basename \"$(touch out)\""}`, want: true},
		{name: "formatter write", toolName: "bash", args: `{"command":"gofmt -w internal/a.go"}`, want: true},
		{name: "file redirect", toolName: "bash", args: `{"command":"printf x > generated.txt"}`, want: true},
		{name: "compound verification", toolName: "bash", args: `{"command":"go test ./... 2>&1 | tail -20"}`},
		{name: "pytest snapshot update stays opaque", toolName: "bash", args: `{"command":"pytest --snapshot-update"}`, want: true},
		{name: "pytest junitxml report stays opaque", toolName: "bash", args: `{"command":"pytest --junitxml=report.xml"}`, want: true},
		{name: "gotestsum junitfile stays opaque", toolName: "bash", args: `{"command":"gotestsum --junitfile out.xml ./..."}`, want: true},
		{name: "go test coverprofile stays opaque", toolName: "bash", args: `{"command":"go test -coverprofile=cover.out ./..."}`, want: true},
		{name: "go test blockprofile stays opaque", toolName: "bash", args: `{"command":"go test -blockprofile=block.out ./..."}`, want: true},
		{name: "go test trace stays opaque", toolName: "bash", args: `{"command":"go test -trace trace.out ./..."}`, want: true},
		{name: "go test compile binary stays opaque", toolName: "bash", args: `{"command":"go test -c ./internal/evidence"}`, want: true},
		{name: "go test dotted cpuprofile stays opaque", toolName: "bash", args: `{"command":"go test ./internal/evidence -test.cpuprofile=cpu.out -count=1"}`, want: true},
		{name: "go test artifacts stays opaque", toolName: "bash", args: `{"command":"go test -artifacts ./..."}`, want: true},
		{name: "jest output file stays opaque", toolName: "bash", args: `{"command":"npm test -- --json --outputFile=result.json"}`, want: true},
		{name: "mypy report stays opaque", toolName: "bash", args: `{"command":"mypy --txt-report reports src/"}`, want: true},
		{name: "plain pytest", toolName: "bash", args: `{"command":"pytest"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToolCallMutates(tt.toolName, json.RawMessage(tt.args), tt.readOnly); got != tt.want {
				t.Fatalf("ToolCallMutates(%q, %s) = %v, want %v", tt.toolName, tt.args, got, tt.want)
			}
		})
	}
}

func TestRunnerWriteOutputFlagsCannotMasqueradeAsVerification(t *testing.T) {
	// Snapshot flags rewrite checked-in fixtures and report/profile flags
	// write explicit output paths; both must stay opaque mutations so the
	// files they produce still require review and sign-off.
	for _, command := range []string{
		"pytest --snapshot-update",
		"pytest --junitxml=report.xml",
		"mypy --junit-xml report.xml src/",
		"gotestsum --junitfile out.xml ./...",
		"go test -coverprofile=cover.out ./...",
		"go test --coverprofile cover.out ./...",
		"go test -blockprofile=block.out ./...",
		"go test -mutexprofile mutex.out ./...",
		"go test -trace trace.out ./...",
		"go test -c ./internal/evidence",
		"go test -o evidence.test -c ./internal/evidence",
		"go test ./internal/evidence -test.cpuprofile=cpu.out -count=1",
		"go test -test.trace trace.out ./...",
		"go test -artifacts ./...",
		"go test ./... -args -test.testlogfile=log.txt",
		"go test -test.gocoverdir=covdir ./...",
		"gotestsum -- -test.coverprofile=cover.out ./...",
		"npm test -- --updateSnapshot",
		"npx vitest run --update",
		"npx vitest run --update=true",
		"npx vitest run -u=true",
		"npx jest --updateSnapshot=true",
		"npx vitest run --coverage",
		"npx vitest run --outputFile=result.json",
		"npx --yes vitest run",
		"npm test -- --json --outputFile=result.json",
		"yarn test --outputFile.json=result.json",
		"pytest --report-log=log.jsonl",
		"mypy --txt-report reports src/",
		"mypy --html-report html src/",
		"mypy --xml-report=reports src/",
		"mypy --cobertura-xml-report reports src/",
	} {
		if bashCommandIsVerification(command) {
			t.Fatalf("%q writes files and must not be classified as verification", command)
		}
		if !ToolCallMutates("bash", json.RawMessage(`{"command":"`+command+`"}`), false) {
			t.Fatalf("%q must remain an opaque mutation", command)
		}
	}
	for _, command := range []string{
		"pytest",
		"gotestsum ./...",
		"go test -cover ./...",
		"go test -count=1 ./...",
		"go test -test.v -test.run TestFoo ./...",
		"pytest --trace",
		"npm test -- --json",
		"npx vitest run src/lib/foo.test.ts 2>&1 | tail -40",
		"npx jest src/lib/foo.test.ts",
		"mypy src/",
		"mypy --strict src/",
	} {
		if !bashCommandIsVerification(command) {
			t.Fatalf("%q should remain a verification command", command)
		}
	}
}

func TestToolCallRequiresAcceptanceCriteriaForExecutionCommands(t *testing.T) {
	if !ToolCallRequiresAcceptanceCriteria("bash", json.RawMessage(`{"command":"go test ./..."}`), false) {
		t.Fatal("verification command should require delivery acceptance criteria")
	}
	if !ToolCallRequiresAcceptanceCriteria("bash", json.RawMessage(`{"command":"npm run test"}`), false) {
		t.Fatal("npm run test should require delivery acceptance criteria")
	}
	if !ToolCallRequiresAcceptanceCriteria("bash", json.RawMessage(`{"command":"git diff --check"}`), false) {
		t.Fatal("git diff --check is a verification command and should require acceptance criteria")
	}
	if !ToolCallRequiresAcceptanceCriteria("bash", json.RawMessage(`{"command":"node --check app.js"}`), false) {
		t.Fatal("node --check is a verification command and should require acceptance criteria")
	}
}

func TestBashToolCallUsesOpaqueInlineInterpreter(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: `node -e 'require("fs").readFileSync("snake.html")'`, want: true},
		{command: `node --input-type=module --eval 'console.log(1)'`, want: true},
		{command: `python3 -c 'print(open("snake.html").read())'`, want: true},
		{command: `ruby -e 'puts 1'`, want: true},
		{command: `php -r 'echo 1;'`, want: true},
		{command: `deno eval 'console.log(1)'`, want: true},
		{command: "tail -n +2 snake.html | node --check -"},
		{command: "node --test"},
		{command: "python3 -m pytest"},
		{command: "node scripts/check.js"},
	}
	for _, tt := range tests {
		args, err := json.Marshal(map[string]string{"command": tt.command})
		if err != nil {
			t.Fatal(err)
		}
		if got := BashToolCallUsesOpaqueInlineInterpreter(args); got != tt.want {
			t.Errorf("BashToolCallUsesOpaqueInlineInterpreter(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestNodeEvalCannotMasqueradeAsDeliveryVerification(t *testing.T) {
	command := `node -e 'require("fs").readFileSync("app.js")'`
	if IsVerificationCommand(command) {
		t.Fatal("arbitrary node eval must not be recognized as delivery verification")
	}
	if !ToolCallMutates("bash", json.RawMessage(`{"command":"node -e 'require(\"fs\").readFileSync(\"app.js\")'"}`), false) {
		t.Fatal("arbitrary node eval must remain an opaque mutation")
	}
}

func TestNodeConditionsFlagCannotMasqueradeAsDeliveryVerification(t *testing.T) {
	// Node CLI flags are case-sensitive: -C is --conditions and executes the
	// target script, unlike the syntax-only -c/--check.
	command := "node -C production server.js"
	if IsVerificationCommand(command) {
		t.Fatal("node -C (--conditions) executes the script and must not be recognized as delivery verification")
	}
	if !ToolCallMutates("bash", json.RawMessage(`{"command":"node -C production server.js"}`), false) {
		t.Fatal("node -C (--conditions) must remain an opaque mutation")
	}
}

func TestNodeTestRunnerWriteFlagsCannotMasqueradeAsDeliveryVerification(t *testing.T) {
	if !IsVerificationCommand("node --test") {
		t.Fatal("plain node --test should be recognized as a delivery verification")
	}
	// Test-runner state/report flags and Node runtime profiling/tracing flags
	// create or update files. They must stay opaque mutations so those files
	// still require review and sign-off.
	for _, command := range []string{
		"node --test --test-update-snapshots",
		"node --test --test-reporter=junit --test-reporter-destination=result.txt",
		"node --test --test-reporter junit --test-reporter-destination result.txt",
		"node --test --test-rerun-failures=state.json",
		"node --test --test-rerun-failures state.json",
		"node --test --cpu-prof",
		"node --test --heap-prof",
		"node --test --heapsnapshot-near-heap-limit=1",
		"node --test --heapsnapshot-signal=SIGUSR2",
		"node --test --localstorage-file=localstorage.json",
		"node --test --perf-basic-prof",
		"node --test --perf-basic-prof-only-functions",
		"node --test --perf-prof",
		"node --test --prof",
		"node --test --redirect-warnings=warnings.log",
		"node --test --report-on-fatalerror",
		"node --test --report-on-signal",
		"node --test --report-uncaught-exception",
		"node --test --tls-keylog=tls.log",
		"node --test --trace-events-enabled",
	} {
		if IsVerificationCommand(command) {
			t.Fatalf("%q writes files and must not be recognized as delivery verification", command)
		}
	}
}

func TestLedgerReviewAfterRestoredCheckpointBaseline(t *testing.T) {
	// A negative index is the restored-checkpoint baseline: the mutation
	// happened before a controller rebuild or cold resume, so its receipt (and
	// touched paths) are not in this ledger. Fresh review-shaped receipts must
	// still be able to satisfy the review gate.
	if NewLedger().HasSuccessfulReviewAfter(-1) {
		t.Fatal("an empty ledger must not satisfy the checkpoint-baseline review")
	}

	read := NewLedger()
	read.Record(ReceiptFromToolCall("read_file", json.RawMessage(`{"path":"internal/parser.go"}`), true, true))
	if !read.HasSuccessfulReviewAfter(-1) {
		t.Fatal("a successful read must satisfy review for a restored mutation baseline")
	}

	diff := NewLedger()
	diff.Record(ReceiptFromToolCall("bash", json.RawMessage(`{"command":"git diff"}`), true, false))
	if !diff.HasSuccessfulReviewAfter(-1) {
		t.Fatal("a git diff inspection must satisfy review for a restored mutation baseline")
	}

	failed := NewLedger()
	failed.Record(ReceiptFromToolCall("read_file", json.RawMessage(`{"path":"internal/parser.go"}`), false, true))
	if failed.HasSuccessfulReviewAfter(-1) {
		t.Fatal("a failed read must not satisfy the checkpoint-baseline review")
	}

	opaque := NewLedger()
	opaque.Record(ReceiptFromToolCall("bash", json.RawMessage(`{"command":"echo done"}`), true, false))
	if opaque.HasSuccessfulReviewAfter(-1) {
		t.Fatal("a non-review command must not satisfy the checkpoint-baseline review")
	}
}
