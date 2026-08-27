package inject

import (
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinstimatze/spar/internal/gitdiff"
)

// Try's oversized-candidate check reads real file content via
// gitdiff.FileContent, which needs a real repo — but it returns before
// ever calling the Anthropic API, so this stays network-free.

func newInjectScratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runInjectGit(t, dir, "init", "-q")
	runInjectGit(t, dir, "config", "user.email", "test@example.com")
	runInjectGit(t, dir, "config", "user.name", "Test")
	return dir
}

func runInjectGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestTryFallsBackWhenCandidateExceedsSizeCap(t *testing.T) {
	dir := newInjectScratchRepo(t)
	path := filepath.Join(dir, "big.go")
	if err := os.WriteFile(path, []byte("package foo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runInjectGit(t, dir, "add", "big.go")
	runInjectGit(t, dir, "commit", "-q", "-m", "base")

	oversized := "package foo\n" + strings.Repeat("// x\n", 100)
	if err := os.WriteFile(path, []byte(oversized), 0644); err != nil {
		t.Fatal(err)
	}

	d := gitdiff.Diff{
		RawText: "diff --git a/big.go b/big.go\n",
		Staged:  false,
		Files: []gitdiff.FileChange{
			{Path: "big.go", Status: 'M', HunkRanges: []gitdiff.LineRange{{Start: 1, End: 101}}},
		},
	}
	cfg := testConfig()
	cfg.APIKey = "unused — must never be reached"
	cfg.MaxFileBytes = 20 // smaller than the oversized candidate above

	res := Try(d, dir, cfg, rand.New(rand.NewSource(1)))
	if res.Injected {
		t.Error("Try() should never inject an oversized candidate")
	}
	if res.FallbackReason != "candidate file exceeds size cap" {
		t.Errorf("FallbackReason = %q, want %q", res.FallbackReason, "candidate file exceeds size cap")
	}
}
