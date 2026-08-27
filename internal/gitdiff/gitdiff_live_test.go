package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests shell out to a real git in a scratch repo — deliberately.
// RepoRoot, Capture, FileContent, and RegenerateFileDiff exist to invoke
// and parse real git output; a synthetic-fixture-only test suite can't
// catch a mismatch with what git actually does (the added-file diff
// header bug this session fixed was invisible to the fixture tests and
// only surfaced against real git).

func newScratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeScratchFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func commitFile(t *testing.T, dir, path, content string) {
	t.Helper()
	writeScratchFile(t, dir, path, content)
	runGit(t, dir, "add", path)
	runGit(t, dir, "commit", "-q", "-m", "commit "+path)
}

func TestRepoRootFromRootAndSubdir(t *testing.T) {
	dir := newScratchRepo(t)
	commitFile(t, dir, "f.go", "package foo\n")
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	root, err := RepoRoot(dir)
	if err != nil {
		t.Fatalf("RepoRoot(root): %v", err)
	}
	fromSub, err := RepoRoot(sub)
	if err != nil {
		t.Fatalf("RepoRoot(subdir): %v", err)
	}
	if root != fromSub {
		t.Errorf("RepoRoot from root (%q) and subdir (%q) disagree", root, fromSub)
	}
}

func TestRepoRootNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := RepoRoot(dir); err == nil {
		t.Fatal("expected an error outside any git repository")
	}
}

func TestCaptureNoChanges(t *testing.T) {
	dir := newScratchRepo(t)
	commitFile(t, dir, "f.go", "package foo\n")
	d, err := Capture(dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if !d.Empty() {
		t.Errorf("expected an empty diff with no changes, got %q", d.RawText)
	}
}

func TestCaptureUnstagedOnly(t *testing.T) {
	dir := newScratchRepo(t)
	commitFile(t, dir, "f.go", "line1\n")
	writeScratchFile(t, dir, "f.go", "line1\nline2\n")

	d, err := Capture(dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if d.Staged {
		t.Error("expected an unstaged diff, got Staged=true")
	}
	if !strings.Contains(d.RawText, "+line2") {
		t.Errorf("unstaged diff missing the real change:\n%s", d.RawText)
	}
	if len(d.Files) != 1 || d.Files[0].Path != "f.go" {
		t.Errorf("unexpected Files: %+v", d.Files)
	}
}

func TestCaptureStagedPreferredOverUnstaged(t *testing.T) {
	dir := newScratchRepo(t)
	commitFile(t, dir, "f.go", "A\n")
	writeScratchFile(t, dir, "f.go", "B\n")
	runGit(t, dir, "add", "f.go")
	// A further, unstaged edit on top of the staged change — Capture
	// must report the staged (A->B) diff, not the unstaged (B->C) one.
	writeScratchFile(t, dir, "f.go", "C\n")

	d, err := Capture(dir)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if !d.Staged {
		t.Fatal("expected the staged diff to win")
	}
	if !strings.Contains(d.RawText, "+B") || strings.Contains(d.RawText, "+C") {
		t.Errorf("expected the staged A->B diff, got:\n%s", d.RawText)
	}
}

func TestFileContentModifiedStaged(t *testing.T) {
	dir := newScratchRepo(t)
	commitFile(t, dir, "f.go", "A\n")
	writeScratchFile(t, dir, "f.go", "B\n")
	runGit(t, dir, "add", "f.go")

	fc := FileChange{Path: "f.go", Status: 'M'}
	before, after, err := FileContent(dir, fc, true)
	if err != nil {
		t.Fatalf("FileContent: %v", err)
	}
	if before != "A\n" || after != "B\n" {
		t.Errorf("got before=%q after=%q, want A/B", before, after)
	}
}

func TestFileContentModifiedUnstaged(t *testing.T) {
	dir := newScratchRepo(t)
	commitFile(t, dir, "f.go", "A\n")
	writeScratchFile(t, dir, "f.go", "B\n")

	fc := FileChange{Path: "f.go", Status: 'M'}
	before, after, err := FileContent(dir, fc, false)
	if err != nil {
		t.Fatalf("FileContent: %v", err)
	}
	if before != "A\n" || after != "B\n" {
		t.Errorf("got before=%q after=%q, want A/B", before, after)
	}
}

func TestFileContentAddedFile(t *testing.T) {
	dir := newScratchRepo(t)
	commitFile(t, dir, "existing.go", "package foo\n")
	writeScratchFile(t, dir, "new.go", "package foo\nfunc New() {}\n")
	runGit(t, dir, "add", "new.go")

	fc := FileChange{Path: "new.go", Status: 'A'}
	before, after, err := FileContent(dir, fc, true)
	if err != nil {
		t.Fatalf("FileContent: %v", err)
	}
	if before != "" {
		t.Errorf("added file should have no before-content, got %q", before)
	}
	if after != "package foo\nfunc New() {}\n" {
		t.Errorf("after = %q, want the new file's content", after)
	}
}

func TestFileContentDeletedFile(t *testing.T) {
	dir := newScratchRepo(t)
	commitFile(t, dir, "f.go", "A\n")
	runGit(t, dir, "rm", "-q", "f.go")

	fc := FileChange{Path: "f.go", Status: 'D'}
	before, after, err := FileContent(dir, fc, true)
	if err != nil {
		t.Fatalf("FileContent: %v", err)
	}
	if before != "A\n" {
		t.Errorf("before = %q, want the last committed content", before)
	}
	if after != "" {
		t.Errorf("after = %q, want empty for a deleted file", after)
	}
}

func TestRegenerateFileDiffModified(t *testing.T) {
	out, err := RegenerateFileDiff("line1\nline2\n", "line1\nCHANGED\n", "pkg/file.go")
	if err != nil {
		t.Fatalf("RegenerateFileDiff: %v", err)
	}
	for _, want := range []string{
		"diff --git a/pkg/file.go b/pkg/file.go",
		"--- a/pkg/file.go",
		"+++ b/pkg/file.go",
		"-line2",
		"+CHANGED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "new file mode") {
		t.Errorf("a modified-file diff shouldn't carry a new file mode line, got:\n%s", out)
	}
}

// Regression test for the added-file header bug: RegenerateFileDiff must
// match real git byte-for-byte for a newly-added file, not just "look
// like a diff" — a mismatched header (missing "new file mode", the
// wrong --- line) was the tool's most obvious tell.
func TestRegenerateFileDiffAddedFileMatchesRealGit(t *testing.T) {
	const content = "line1\nline2\n"
	got, err := RegenerateFileDiff("", content, "added.go")
	if err != nil {
		t.Fatalf("RegenerateFileDiff: %v", err)
	}

	dir := newScratchRepo(t)
	writeScratchFile(t, dir, "added.go", content)
	runGit(t, dir, "add", "added.go")
	want := runGit(t, dir, "diff", "--staged", "--no-color")

	if got != want {
		t.Errorf("regenerated added-file diff doesn't match real git.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRegenerateFileDiffIdenticalIsError(t *testing.T) {
	if _, err := RegenerateFileDiff("same\n", "same\n", "f.go"); err == nil {
		t.Fatal("expected an error for identical before/after")
	}
}

// Regression test for the relative-$TMPDIR safety fix: a non-absolute
// scratch directory must fail closed rather than risk writing a
// mutation's temp files inside whatever the caller's cwd happens to be.
func TestRegenerateFileDiffRelativeTMPDIRFailsClosed(t *testing.T) {
	t.Chdir(t.TempDir()) // contain any stray relative-path write to a disposable dir
	// "." exists (it's cwd), so os.MkdirTemp succeeds and returns a
	// genuinely non-absolute path — the exact scenario a relative
	// $TMPDIR misconfiguration produces in practice.
	t.Setenv("TMPDIR", ".")

	_, err := RegenerateFileDiff("a\n", "b\n", "f.go")
	if err == nil {
		t.Fatal("expected an error for a relative $TMPDIR, got nil")
	}
	if !strings.Contains(err.Error(), "not absolute") {
		t.Errorf("error = %q, want it to mention the temp dir isn't absolute", err.Error())
	}
}
