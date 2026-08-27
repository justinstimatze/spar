package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justinstimatze/spar/internal/store"
)

func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func assistantLine(text string) string {
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":  "assistant",
			"model": "claude-sonnet-5",
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
		},
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func assistantLineWithToolUse(text, toolArg string) string {
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":  "assistant",
			"model": "claude-sonnet-5",
			"content": []any{
				map[string]any{"type": "text", "text": text},
				map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": toolArg}},
			},
		},
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func syntheticLine(text string) string {
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":  "assistant",
			"model": syntheticModelMarker,
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
		},
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func apiErrorLine(text string) string {
	rec := map[string]any{
		"type":              "assistant",
		"isApiErrorMessage": true,
		"message": map[string]any{
			"role":  "assistant",
			"model": "claude-sonnet-5",
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
		},
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func userLine(text string) string {
	rec := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": text,
		},
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

func TestApplyPatchUniqueMatchPreservesOtherLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	original := "The retry was added after an incident in testing."
	corrected := "The retry was caught by planning, before any code existed."

	before := assistantLine(original)
	untouched1 := assistantLine("Some other genuinely unrelated reply.")
	untouched2 := userLine("ok thanks")

	content := strings.Join([]string{before, untouched1, untouched2}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := applyPatch(path, original, corrected); err != nil {
		t.Fatalf("applyPatch: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if strings.Contains(lines[0], original) {
		t.Error("patched line still contains the original text")
	}
	if !strings.Contains(lines[0], corrected) {
		t.Error("patched line doesn't contain the corrected text")
	}
	if lines[1] != untouched1 {
		t.Errorf("untouched line 2 changed:\n got:  %s\n want: %s", lines[1], untouched1)
	}
	if lines[2] != untouched2 {
		t.Errorf("untouched line 3 changed:\n got:  %s\n want: %s", lines[2], untouched2)
	}
}

func TestApplyPatchNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := assistantLine("Nothing matches here.") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	err := applyPatch(path, "this text does not appear anywhere", "replacement")
	if !errors.Is(err, errNotFound) {
		t.Errorf("applyPatch = %v, want errNotFound", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Error("file should be untouched on a not-found skip")
	}
}

func TestApplyPatchAmbiguousDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	text := "the exact same sentence appears twice"
	content := strings.Join([]string{
		assistantLine("First occurrence: " + text),
		assistantLine("Second occurrence: " + text),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	err := applyPatch(path, text, "replacement")
	if !errors.Is(err, errAmbiguous) {
		t.Errorf("applyPatch = %v, want errAmbiguous", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Error("file should be untouched on an ambiguous skip")
	}
}

// TestApplyPatchAmbiguousSelfQuotingDisclosure is Finding 5's known,
// accepted limitation: a disclosure that quotes the plant verbatim (which
// the reveal instructions actively encourage) makes the real, common case
// ambiguous. Refusing here is correct — never guessing which occurrence to
// patch — not a bug.
func TestApplyPatchAmbiguousSelfQuotingDisclosure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	original := "spar doctor was built before spar review"
	content := strings.Join([]string{
		assistantLine("Earlier I claimed " + original + ", which shipped first."),
		assistantLine("Correction: I said \"" + original + "\" — that's false, review shipped first in 0.1.0."),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	err := applyPatch(path, original, "review shipped first, doctor came later")
	if !errors.Is(err, errAmbiguous) {
		t.Errorf("applyPatch = %v, want errAmbiguous — a self-quoting disclosure is a documented limitation", err)
	}
}

func TestScanMatchesExcludesToolUseSyntheticAndUserBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	needle := "only the real assistant text block should count"
	content := strings.Join([]string{
		assistantLineWithToolUse("unrelated reply", "echo '"+needle+"'"),
		syntheticLine(needle),
		apiErrorLine(needle),
		userLine(needle),
		assistantLine(needle), // the one real match
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := scanMatches(path, needle)
	if err != nil {
		t.Fatalf("scanMatches: %v", err)
	}
	if n != 1 {
		t.Errorf("scanMatches = %d, want exactly 1 (tool_use/synthetic/api-error/user blocks must not count)", n)
	}
}

func TestApplyPatchPreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := assistantLine("mode test original text") + "\n"
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
	if err := applyPatch(path, "mode test original text", "replacement"); err != nil {
		t.Fatalf("applyPatch: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0640 {
		t.Errorf("file mode = %v, want 0640 preserved from before the patch", fi.Mode().Perm())
	}
}

func TestBackupTranscriptNonJSONLSuffix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := []byte("line one\nline two\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	bpath, err := backupTranscript(path)
	if err != nil {
		t.Fatalf("backupTranscript: %v", err)
	}
	if strings.HasSuffix(bpath, ".jsonl") {
		t.Errorf("backup path %q ends in .jsonl — could be picked up as a phantom session", bpath)
	}
	got, err := os.ReadFile(bpath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Error("backup content doesn't match the original file")
	}
}

func TestCheckNotRecentlyModifiedRefusesRecent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkNotRecentlyModified(path, false); err == nil {
		t.Error("a just-written file should be refused without --force")
	}
	if err := checkNotRecentlyModified(path, true); err != nil {
		t.Errorf("--force should bypass the guard, got %v", err)
	}
}

func TestCheckNotRecentlyModifiedAllowsOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := checkNotRecentlyModified(path, false); err != nil {
		t.Errorf("an hour-old file should pass the guard, got %v", err)
	}
}

func TestFixupLedgerRoundtrip(t *testing.T) {
	isolateHome(t)
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	before, err := readFixupLedger()
	if err != nil {
		t.Fatalf("readFixupLedger (empty): %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("expected an empty ledger before anything's recorded, got %v", before)
	}
	if err := appendFixupLedger("s1", ts); err != nil {
		t.Fatalf("appendFixupLedger: %v", err)
	}
	after, err := readFixupLedger()
	if err != nil {
		t.Fatalf("readFixupLedger: %v", err)
	}
	if !after[fixupLedgerKey("s1", ts)] {
		t.Errorf("ledger doesn't show the recorded trial as patched: %v", after)
	}
}

func TestEligibleFixupTrials(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	complete := store.Trial{
		Mode: store.ModeLive, Injected: true, SessionID: "s1", TS: ts,
		OriginalText: "a", CorrectedText: "b", TranscriptPath: "/x.jsonl",
	}

	cases := []struct {
		name   string
		trial  store.Trial
		ledger map[string]bool
		want   int
	}{
		{"eligible", complete, nil, 1},
		{"already ledgered", complete, map[string]bool{fixupLedgerKey("s1", ts): true}, 0},
		{"missing corrected text", func() store.Trial { c := complete; c.CorrectedText = ""; return c }(), nil, 0},
		{"missing transcript path", func() store.Trial { c := complete; c.TranscriptPath = ""; return c }(), nil, 0},
		{"not injected", func() store.Trial { c := complete; c.Injected = false; return c }(), nil, 0},
		{"review mode", func() store.Trial { c := complete; c.Mode = store.ModeReview; return c }(), nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := eligibleFixupTrials([]store.Trial{c.trial}, c.ledger)
			if len(got) != c.want {
				t.Errorf("eligibleFixupTrials = %d trials, want %d", len(got), c.want)
			}
		})
	}
}

// TestRunFixupApplyMultiTrialSession is Finding 3's regression case: a
// session with more than one eligible trial must patch all of them in one
// --apply run without the mtime guard self-tripping on its own prior write,
// and without colliding backup filenames — both bugs a per-trial guard/
// backup would have produced.
func TestRunFixupApplyMultiTrialSession(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := strings.Join([]string{
		assistantLine("first false claim here"),
		assistantLine("second false claim here"),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	trials := []store.Trial{
		{SessionID: "s1", TS: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), TranscriptPath: path,
			OriginalText: "first false claim here", CorrectedText: "first true fact here"},
		{SessionID: "s1", TS: time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC), TranscriptPath: path,
			OriginalText: "second false claim here", CorrectedText: "second true fact here"},
	}

	runFixupApply(trials, false)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "first false claim here") || strings.Contains(string(got), "second false claim here") {
		t.Errorf("both trials should have been patched: %s", got)
	}
	if !strings.Contains(string(got), "first true fact here") || !strings.Contains(string(got), "second true fact here") {
		t.Errorf("both corrected facts should be present: %s", got)
	}

	backups, err := filepath.Glob(path + ".spar-fixup-backup-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Errorf("got %d backup files, want exactly 1 — backup must be taken once per invocation, not once per trial", len(backups))
	}

	ledger, err := readFixupLedger()
	if err != nil {
		t.Fatalf("readFixupLedger: %v", err)
	}
	if len(ledger) != 2 {
		t.Errorf("ledger has %d entries, want 2 (one per patched trial)", len(ledger))
	}
}
