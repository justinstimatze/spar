package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
