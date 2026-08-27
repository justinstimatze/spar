package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justinstimatze/spar/internal/inject"
	"github.com/justinstimatze/spar/internal/livestate"
)

// isolateHome is already defined in cmd_live_fixup_test.go, package-level.

func decodeCommitOutput(t *testing.T, raw []byte) commitHookOutput {
	t.Helper()
	var out commitHookOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, raw)
	}
	return out
}

func TestRunLiveHookCommitAlwaysAllows(t *testing.T) {
	const session = "test-session-0123456789"
	cases := []struct {
		name  string
		setup func(t *testing.T)
		input string
	}{
		{
			name:  "disabled",
			setup: func(t *testing.T) {},
			input: `{"session_id":"` + session + `","tool_input":{"command":"git commit -m x"}}`,
		},
		{
			name:  "malformed json",
			setup: func(t *testing.T) { t.Setenv("SPAR_LIVE_ENABLED", "1") },
			input: `not json`,
		},
		{
			name:  "empty session id",
			setup: func(t *testing.T) { t.Setenv("SPAR_LIVE_ENABLED", "1") },
			input: `{"session_id":"","tool_input":{"command":"git commit -m x"}}`,
		},
		{
			name:  "command mismatch, fail-open if-filter case",
			setup: func(t *testing.T) { t.Setenv("SPAR_LIVE_ENABLED", "1") },
			input: `{"session_id":"` + session + `","tool_input":{"command":"echo $(date)"}}`,
		},
		{
			name:  "invalid session id shape",
			setup: func(t *testing.T) { t.Setenv("SPAR_LIVE_ENABLED", "1") },
			input: `{"session_id":"x","tool_input":{"command":"git commit -m x"}}`,
		},
		{
			name:  "happy path",
			setup: func(t *testing.T) { t.Setenv("SPAR_LIVE_ENABLED", "1") },
			input: `{"session_id":"` + session + `","tool_input":{"command":"git commit -m x"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			tc.setup(t)
			var buf bytes.Buffer
			runLiveHookCommit(strings.NewReader(tc.input), &buf)
			out := decodeCommitOutput(t, buf.Bytes())
			if out.HookSpecificOutput.PermissionDecision != "allow" {
				t.Fatalf("permissionDecision = %q, want %q (case %q)", out.HookSpecificOutput.PermissionDecision, "allow", tc.name)
			}
		})
	}
}

func TestRunLiveHookCommitHappyPathPlants(t *testing.T) {
	isolateHome(t)
	t.Setenv("SPAR_LIVE_ENABLED", "1")
	const session = "test-session-0123456789"

	var buf bytes.Buffer
	runLiveHookCommit(strings.NewReader(`{"session_id":"`+session+`","transcript_path":"/tmp/fake.jsonl","tool_input":{"command":"git commit -m x"}}`), &buf)

	out := decodeCommitOutput(t, buf.Bytes())
	if out.HookSpecificOutput.AdditionalContext == "" {
		t.Fatal("expected planting instructions in additionalContext, got none")
	}

	pending, ok, err := livestate.ReadPending(session)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if !ok {
		t.Fatal("expected a pending plant to have been written")
	}
	if pending.TranscriptPath != "/tmp/fake.jsonl" {
		t.Errorf("TranscriptPath = %q, want it to round-trip", pending.TranscriptPath)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, pending.Category) {
		t.Errorf("additionalContext doesn't mention the picked category %q", pending.Category)
	}
}

func TestRunLiveHookCommitNoOpWhenCommandDoesNotMatch(t *testing.T) {
	isolateHome(t)
	t.Setenv("SPAR_LIVE_ENABLED", "1")
	const session = "test-session-0123456789"

	var buf bytes.Buffer
	runLiveHookCommit(strings.NewReader(`{"session_id":"`+session+`","tool_input":{"command":"echo $(date)"}}`), &buf)

	out := decodeCommitOutput(t, buf.Bytes())
	if out.HookSpecificOutput.AdditionalContext != "" {
		t.Error("a non-commit command should never plant")
	}
	if _, ok, _ := livestate.ReadPending(session); ok {
		t.Error("a non-commit command should never write a pending plant")
	}
}

func TestRunLiveHookCommitNoOpWhenAlreadyPending(t *testing.T) {
	isolateHome(t)
	t.Setenv("SPAR_LIVE_ENABLED", "1")
	const session = "test-session-0123456789"

	first, err := livestate.WritePending(session, "misordered-causality", "")
	if err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	var buf bytes.Buffer
	runLiveHookCommit(strings.NewReader(`{"session_id":"`+session+`","tool_input":{"command":"git commit -m x"}}`), &buf)

	out := decodeCommitOutput(t, buf.Bytes())
	if out.HookSpecificOutput.AdditionalContext != "" {
		t.Error("a session with a plant already pending reveal should never get a second plant")
	}
	pending, ok, err := livestate.ReadPending(session)
	if err != nil || !ok {
		t.Fatalf("ReadPending after no-op: ok=%v err=%v", ok, err)
	}
	if pending.Token != first.Token {
		t.Error("the original pending plant's token should be untouched")
	}
}

func TestRunLiveHookCommitNoOpWhenCooldownNotElapsed(t *testing.T) {
	isolateHome(t)
	t.Setenv("SPAR_LIVE_ENABLED", "1")
	const session = "test-session-0123456789"

	if _, err := livestate.WritePending(session, "misordered-causality", ""); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	if err := livestate.MarkFired(session); err != nil {
		t.Fatalf("MarkFired: %v", err)
	}
	if err := livestate.ClosePending(session); err != nil {
		t.Fatalf("ClosePending: %v", err)
	}
	// Pending is now closed (simulating a completed reveal), but the
	// cooldown marker MarkFired just stamped is still fresh — this isolates
	// the cooldown-specific no-op path from the pending-exists one above.

	var buf bytes.Buffer
	runLiveHookCommit(strings.NewReader(`{"session_id":"`+session+`","tool_input":{"command":"git commit -m x"}}`), &buf)

	out := decodeCommitOutput(t, buf.Bytes())
	if out.HookSpecificOutput.AdditionalContext != "" {
		t.Error("a fresh cooldown marker should prevent a second plant")
	}
	if _, ok, _ := livestate.ReadPending(session); ok {
		t.Error("no new pending plant should have been written while on cooldown")
	}
}

func TestEnvCommitMode(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"", "narrate"},
		{"narrate", "narrate"},
		{"notify", "notify"},
		{"gate", "gate"},
		{"something-else", "narrate"},
	}
	for _, c := range cases {
		t.Setenv("SPAR_LIVE_COMMIT_MODE", c.env)
		if got := envCommitMode(); got != c.want {
			t.Errorf("SPAR_LIVE_COMMIT_MODE=%q -> envCommitMode() = %q, want %q", c.env, got, c.want)
		}
	}
}

// newCommitScratchRepo creates a real git repo with one committed file,
// then modifies and stages a second change — gitdiff.Capture picks up
// exactly this staged diff, matching what a PreToolUse hook would see
// firing after `git add` but before `git commit` creates the commit
// object.
func newCommitScratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runCommitScratchGit(t, dir, "init", "-q")
	runCommitScratchGit(t, dir, "config", "user.email", "test@example.com")
	runCommitScratchGit(t, dir, "config", "user.name", "Test")

	path := filepath.Join(dir, "f.go")
	if err := os.WriteFile(path, []byte("package foo\n\nfunc F() int {\n\treturn 1\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCommitScratchGit(t, dir, "add", "f.go")
	runCommitScratchGit(t, dir, "commit", "-q", "-m", "initial")

	if err := os.WriteFile(path, []byte("package foo\n\nfunc F() int {\n\treturn 2\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCommitScratchGit(t, dir, "add", "f.go")
	return dir
}

func runCommitScratchGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestRunLiveHookCommitGateAsksWithRealDiff(t *testing.T) {
	isolateHome(t)
	dir := newCommitScratchRepo(t)
	t.Chdir(dir)
	t.Setenv("SPAR_LIVE_COMMIT_MODE", "gate")
	const session = "test-session-0123456789"

	var buf bytes.Buffer
	runLiveHookCommit(strings.NewReader(`{"session_id":"`+session+`","tool_input":{"command":"git commit -m x"}}`), &buf)

	out := decodeCommitOutput(t, buf.Bytes())
	if out.HookSpecificOutput.PermissionDecision != "ask" {
		t.Fatalf("permissionDecision = %q, want %q", out.HookSpecificOutput.PermissionDecision, "ask")
	}
	if !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "return 2") {
		t.Errorf("permissionDecisionReason doesn't contain the real staged diff content: %q", out.HookSpecificOutput.PermissionDecisionReason)
	}
}

func TestRunLiveHookCommitGateAllowsWhenDiffEmpty(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	runCommitScratchGit(t, dir, "init", "-q")
	runCommitScratchGit(t, dir, "config", "user.email", "test@example.com")
	runCommitScratchGit(t, dir, "config", "user.name", "Test")
	t.Chdir(dir)
	t.Setenv("SPAR_LIVE_COMMIT_MODE", "gate")
	const session = "test-session-0123456789"

	var buf bytes.Buffer
	runLiveHookCommit(strings.NewReader(`{"session_id":"`+session+`","tool_input":{"command":"git commit -m x"}}`), &buf)

	out := decodeCommitOutput(t, buf.Bytes())
	if out.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("permissionDecision = %q, want %q for an empty diff", out.HookSpecificOutput.PermissionDecision, "allow")
	}
}

func TestRunLiveHookCommitGateNeverTouchesLivestate(t *testing.T) {
	isolateHome(t)
	dir := newCommitScratchRepo(t)
	t.Chdir(dir)
	t.Setenv("SPAR_LIVE_COMMIT_MODE", "gate")
	const session = "test-session-0123456789"

	var buf bytes.Buffer
	runLiveHookCommit(strings.NewReader(`{"session_id":"`+session+`","tool_input":{"command":"git commit -m x"}}`), &buf)

	if _, ok, err := livestate.ReadPending(session); err != nil || ok {
		t.Errorf("gate mode wrote a pending file: ok=%v err=%v", ok, err)
	}
	if fire, err := livestate.ShouldFire(session, time.Hour); err != nil || !fire {
		t.Errorf("gate mode touched the cooldown marker: ShouldFire=%v err=%v, want true (no marker ever written)", fire, err)
	}
}

func TestRunLiveHookCommitNotifyFallsBackWithNoAPIKey(t *testing.T) {
	isolateHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := newCommitScratchRepo(t)
	t.Chdir(dir)
	t.Setenv("SPAR_LIVE_ENABLED", "1")
	t.Setenv("SPAR_LIVE_COMMIT_MODE", "notify")
	// Force the coin flip to always attempt — otherwise the default 0.4
	// rate means this test only actually reaches inject.Try (and the
	// no-key fallback this test is named for) on a minority of runs,
	// passing the rest of the time via the unrelated "coin flip said
	// clean" path instead.
	t.Setenv("SPAR_INJECT_RATE", "1")
	const session = "test-session-0123456789"

	var buf bytes.Buffer
	runLiveHookCommit(strings.NewReader(`{"session_id":"`+session+`","tool_input":{"command":"git commit -m x"}}`), &buf)

	out := decodeCommitOutput(t, buf.Bytes())
	if out.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("permissionDecision = %q, want %q", out.HookSpecificOutput.PermissionDecision, "allow")
	}
	if out.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("notify mode with no API key should never plant, got additionalContext %q", out.HookSpecificOutput.AdditionalContext)
	}
	if _, ok, _ := livestate.ReadPending(session); ok {
		t.Error("notify mode with no API key should not write a pending plant")
	}
	if fire, err := livestate.ShouldFire(session, time.Hour); err != nil || !fire {
		t.Errorf("an empty trial nobody saw shouldn't burn the cooldown window: ShouldFire=%v err=%v, want true", fire, err)
	}
}

// TestCommitNotifyInstructionsQuotesWholeCommit is a regression test for a
// review finding: an earlier version quoted only the single mutated
// file's diff (inject.Result.MutatedFileDiff), leaving the model no
// sanctioned way to narrate the rest of a multi-file commit. It must
// quote the full spliced diff (DisplayDiff) instead, so every file in the
// commit is represented, not just the mutated one.
func TestCommitNotifyInstructionsQuotesWholeCommit(t *testing.T) {
	p := livestate.Pending{SessionID: "test-session-0123456789"}
	result := inject.Result{
		Injected:        true,
		File:            "mutated.go",
		MutatedFileDiff: "diff --git a/mutated.go b/mutated.go\n+mutated-only line\n",
		DisplayDiff:     "diff --git a/mutated.go b/mutated.go\n+mutated-only line\ndiff --git a/other.go b/other.go\n+untouched-file line\n",
	}
	got := commitNotifyInstructions(p, result)
	if !strings.Contains(got, "untouched-file line") {
		t.Error("commitNotifyInstructions should quote the full commit (DisplayDiff), not just the mutated file — the other file's content is missing")
	}
	if !strings.Contains(got, "mutated-only line") {
		t.Error("commitNotifyInstructions should still include the mutated file's content")
	}
}

func TestGitCommitRe(t *testing.T) {
	shouldMatch := []string{
		"git commit -m 'x'",
		"git -C ../other commit -m x",
		"git commit --amend",
		"cd repo && git commit -m x",
	}
	for _, cmd := range shouldMatch {
		if !gitCommitRe.MatchString(cmd) {
			t.Errorf("gitCommitRe should match %q", cmd)
		}
	}
	shouldNotMatch := []string{
		"echo $(date)",
		"git status",
		"git log --oneline",
		"git log --grep=commit",
	}
	for _, cmd := range shouldNotMatch {
		if gitCommitRe.MatchString(cmd) {
			t.Errorf("gitCommitRe should not match %q", cmd)
		}
	}
}
