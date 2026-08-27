package main

import (
	"testing"

	"github.com/justinstimatze/spar/internal/livestate"
	"github.com/justinstimatze/spar/internal/store"
)

// isolateHome is defined package-level in cmd_live_fixup_test.go.

func readLoggedTrials(t *testing.T) []store.Trial {
	t.Helper()
	path, err := store.LogPath()
	if err != nil {
		t.Fatalf("store.LogPath: %v", err)
	}
	trials, err := store.ReadAll(path)
	if err != nil {
		t.Fatalf("store.ReadAll: %v", err)
	}
	return trials
}

// TestCmdLiveRevealNarrateFlow is a regression test for narrate mode's
// existing flag-driven flow — no test file for cmd_live_reveal.go existed
// before this change.
func TestCmdLiveRevealNarrateFlow(t *testing.T) {
	isolateHome(t)
	const session = "test-session-0123456789"
	pending, err := livestate.WritePending(session, "misordered-causality", "/tmp/fake-transcript.jsonl")
	if err != nil {
		t.Fatalf("WritePending: %v", err)
	}

	cmdLiveReveal([]string{
		"--session", session,
		"--token", pending.Token,
		"--caught", "yes",
		"--description", "the model's own account of what it planted",
		"--user-note", "user corrected it unprompted",
	})

	trials := readLoggedTrials(t)
	if len(trials) != 1 {
		t.Fatalf("got %d logged trials, want 1", len(trials))
	}
	tr := trials[0]
	if tr.Mode != store.ModeLive {
		t.Errorf("Mode = %q, want %q", tr.Mode, store.ModeLive)
	}
	if tr.LiveKind != store.LiveKindNarration {
		t.Errorf("LiveKind = %q, want %q (narrate mode)", tr.LiveKind, store.LiveKindNarration)
	}
	if tr.InjectedCategory != "misordered-causality" {
		t.Errorf("InjectedCategory = %q, want %q", tr.InjectedCategory, "misordered-causality")
	}
	if tr.InjectedDescription != "the model's own account of what it planted" {
		t.Errorf("InjectedDescription = %q, want the --description flag's value", tr.InjectedDescription)
	}
	if tr.InjectedFile != "" || tr.InjectedSeverity != "" || tr.DiffHash != "" {
		t.Errorf("narrate mode should never populate File/Severity/DiffHash, got %+v", tr)
	}
	if !tr.UserFlagged || tr.Outcome != store.OutcomeCatch {
		t.Errorf("caught=yes should log UserFlagged=true, Outcome=catch, got UserFlagged=%v Outcome=%q", tr.UserFlagged, tr.Outcome)
	}

	if _, ok, _ := livestate.ReadPending(session); ok {
		t.Error("a successful reveal should close the pending plant")
	}
}

// TestCmdLiveRevealNotifyAutoFillsGroundTruth confirms notify mode's
// ground truth (stored on Pending at plant time) overrides whatever the
// --description flag says, and populates File/Severity/DiffHash, which
// narrate mode never does.
func TestCmdLiveRevealNotifyAutoFillsGroundTruth(t *testing.T) {
	isolateHome(t)
	const session = "test-session-0123456789"
	gt := livestate.DiffMutationGroundTruth{
		Category:    "off-by-one",
		File:        "internal/foo/foo.go",
		Severity:    "medium",
		Description: "loop bound shifted by one — spar's own exact record",
		DiffHash:    "abc123",
	}
	pending, err := livestate.WritePendingDiffMutation(session, "/tmp/fake-transcript.jsonl", gt)
	if err != nil {
		t.Fatalf("WritePendingDiffMutation: %v", err)
	}

	cmdLiveReveal([]string{
		"--session", session,
		"--token", pending.Token,
		"--caught", "unengaged",
		// Deliberately different from gt.Description, to prove the
		// stored ground truth wins over whatever the model passes here.
		"--description", "the model's fuzzy guess at what it narrated",
	})

	trials := readLoggedTrials(t)
	if len(trials) != 1 {
		t.Fatalf("got %d logged trials, want 1", len(trials))
	}
	tr := trials[0]
	if tr.LiveKind != store.LiveKindDiffMutation {
		t.Errorf("LiveKind = %q, want %q", tr.LiveKind, store.LiveKindDiffMutation)
	}
	if tr.InjectedCategory != "off-by-one" {
		t.Errorf("InjectedCategory = %q, want %q (from pending.Category)", tr.InjectedCategory, "off-by-one")
	}
	if tr.InjectedFile != gt.File {
		t.Errorf("InjectedFile = %q, want %q (auto-filled from pending)", tr.InjectedFile, gt.File)
	}
	if tr.InjectedSeverity != gt.Severity {
		t.Errorf("InjectedSeverity = %q, want %q (auto-filled from pending)", tr.InjectedSeverity, gt.Severity)
	}
	if tr.DiffHash != gt.DiffHash {
		t.Errorf("DiffHash = %q, want %q (auto-filled from pending)", tr.DiffHash, gt.DiffHash)
	}
	if tr.InjectedDescription != gt.Description {
		t.Errorf("InjectedDescription = %q, want spar's own ground truth (%q), not the --description flag", tr.InjectedDescription, gt.Description)
	}
	if tr.Outcome != store.OutcomeUnengaged {
		t.Errorf("Outcome = %q, want %q", tr.Outcome, store.OutcomeUnengaged)
	}
}
