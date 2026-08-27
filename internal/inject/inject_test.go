package inject

import (
	"math/rand"
	"testing"

	"github.com/justinstimatze/spar/internal/gitdiff"
)

func testConfig() Config {
	return Config{
		APIKey:          "unused-in-these-tests",
		Model:           "claude-sonnet-5",
		MaxFileLines:    1500,
		MaxFileBytes:    60_000,
		MaxMutatedLines: 5,
	}
}

func TestEligibleCandidatesExcludesBinaryDeletedRenamed(t *testing.T) {
	d := gitdiff.Diff{Files: []gitdiff.FileChange{
		{Path: "a.go", Status: 'M', HunkRanges: []gitdiff.LineRange{{Start: 1, End: 3}}},
		{Path: "b.png", Status: 'M', Binary: true, HunkRanges: []gitdiff.LineRange{{Start: 1, End: 1}}},
		{Path: "c.go", Status: 'D', HunkRanges: nil},
		{Path: "e.go", OldPath: "d.go", Status: 'R', HunkRanges: []gitdiff.LineRange{{Start: 1, End: 2}}},
		{Path: "f.go", Status: 'A', HunkRanges: []gitdiff.LineRange{{Start: 1, End: 5}}},
	}}
	got := eligibleCandidates(d, testConfig())
	if len(got) != 2 {
		t.Fatalf("got %d eligible, want 2 (a.go, f.go): %+v", len(got), got)
	}
	paths := map[string]bool{got[0].Path: true, got[1].Path: true}
	if !paths["a.go"] || !paths["f.go"] {
		t.Errorf("unexpected candidates: %+v", got)
	}
}

func TestEligibleCandidatesExcludesNoHunkRanges(t *testing.T) {
	d := gitdiff.Diff{Files: []gitdiff.FileChange{
		{Path: "a.go", Status: 'M', HunkRanges: nil},
	}}
	if got := eligibleCandidates(d, testConfig()); len(got) != 0 {
		t.Fatalf("expected no candidates without hunk ranges, got %+v", got)
	}
}

func TestValidateMutationRejectsNoOp(t *testing.T) {
	original := "line1\nline2\nline3\n"
	if _, ok := validateMutation(original, original, nil, testConfig()); ok {
		t.Error("identical content should fail validation")
	}
	if _, ok := validateMutation(original, "", nil, testConfig()); ok {
		t.Error("empty mutation should fail validation")
	}
}

func TestValidateMutationAcceptsWithinRange(t *testing.T) {
	original := "a\nb\nc\nd\ne\n"
	mutated := "a\nb\nCHANGED\nd\ne\n"
	allowed := []gitdiff.LineRange{{Start: 1, End: 5}}
	touched, ok := validateMutation(original, mutated, allowed, testConfig())
	if !ok {
		t.Fatal("expected valid mutation within allowed range")
	}
	if len(touched) != 1 || touched[0] != 3 {
		t.Errorf("touched = %v, want [3]", touched)
	}
}

func TestValidateMutationRejectsOutsideRange(t *testing.T) {
	original := "a\nb\nc\nd\ne\n"
	mutated := "a\nb\nCHANGED\nd\ne\n" // touches line 3
	allowed := []gitdiff.LineRange{{Start: 10, End: 20}}
	if _, ok := validateMutation(original, mutated, allowed, testConfig()); ok {
		t.Error("mutation outside the allowed hunk range should be rejected")
	}
}

func TestValidateMutationRejectsTooManyLines(t *testing.T) {
	original := "1\n2\n3\n4\n5\n6\n7\n8\n"
	mutated := "A\nB\nC\nD\nE\nF\n7\n8\n" // 6 changed lines
	cfg := testConfig()
	cfg.MaxMutatedLines = 5
	if _, ok := validateMutation(original, mutated, nil, cfg); ok {
		t.Error("mutation exceeding MaxMutatedLines should be rejected")
	}
}

func TestValidateMutationEmptyAllowedMeansWholeFile(t *testing.T) {
	original := "a\nb\nc\n"
	mutated := "a\nCHANGED\nc\n"
	if _, ok := validateMutation(original, mutated, nil, testConfig()); !ok {
		t.Error("nil allowed ranges should permit a mutation anywhere in the file")
	}
}

func TestPickCategoryReturnsKnownCategory(t *testing.T) {
	for _, tc := range []struct {
		kind string
		list []string
	}{
		{"code", codeCategories},
		{"prose", proseCategories},
	} {
		known := map[string]bool{}
		for _, c := range tc.list {
			known[c] = true
		}
		rng := rand.New(rand.NewSource(1))
		seen := map[string]bool{}
		for i := 0; i < len(tc.list)*50; i++ {
			got := pickCategory(rng, tc.kind)
			if !known[got] {
				t.Fatalf("pickCategory(%q) returned %q, not one of that kind's declared categories", tc.kind, got)
			}
			seen[got] = true
		}
		if len(seen) != len(tc.list) {
			t.Errorf("pickCategory(%q) only produced %d of %d categories over %d draws: %v", tc.kind, len(seen), len(tc.list), len(tc.list)*50, seen)
		}
	}
}

func TestPickCategoryUnknownKindDefaultsToCode(t *testing.T) {
	known := map[string]bool{}
	for _, c := range codeCategories {
		known[c] = true
	}
	rng := rand.New(rand.NewSource(1))
	if got := pickCategory(rng, "something-else"); !known[got] {
		t.Errorf("pickCategory with an unrecognized kind = %q, want a code category (the safe default)", got)
	}
}

func TestContentKind(t *testing.T) {
	cases := map[string]string{
		"main.go":       "code",
		"script.py":     "code",
		"config.yaml":   "code",
		"noextension":   "code",
		"README.md":     "prose",
		"notes.MD":      "prose",
		"decision.adoc": "prose",
		"plan.txt":      "prose",
		"draft.rst":     "prose",
		"post.mdx":      "prose",
	}
	for path, want := range cases {
		if got := contentKind(path); got != want {
			t.Errorf("contentKind(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q, want claude-sonnet-5", cfg.Model)
	}
	if cfg.MaxFileLines != 1500 || cfg.MaxFileBytes != 60_000 || cfg.MaxMutatedLines != 5 {
		t.Errorf("unexpected caps: %+v", cfg)
	}
	if cfg.HTTPTimeout <= 0 {
		t.Errorf("HTTPTimeout = %v, want a positive timeout", cfg.HTTPTimeout)
	}
}

func TestRateFromEnvDefault(t *testing.T) {
	t.Setenv("SPAR_INJECT_RATE", "")
	if got := RateFromEnv(); got != 0.4 {
		t.Errorf("RateFromEnv() = %v, want 0.4 with no env var set", got)
	}
}

func TestRateFromEnvValid(t *testing.T) {
	t.Setenv("SPAR_INJECT_RATE", "0.7")
	if got := RateFromEnv(); got != 0.7 {
		t.Errorf("RateFromEnv() = %v, want 0.7", got)
	}
}

func TestRateFromEnvInvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("SPAR_INJECT_RATE", "not-a-number")
	if got := RateFromEnv(); got != 0.4 {
		t.Errorf("RateFromEnv() = %v, want the 0.4 default for an unparseable value", got)
	}
}

func TestRateFromEnvClamps(t *testing.T) {
	t.Setenv("SPAR_INJECT_RATE", "5")
	if got := RateFromEnv(); got != 1 {
		t.Errorf("RateFromEnv() = %v, want clamped to 1", got)
	}
	t.Setenv("SPAR_INJECT_RATE", "-3")
	if got := RateFromEnv(); got != 0 {
		t.Errorf("RateFromEnv() = %v, want clamped to 0", got)
	}
}

func TestCleanResult(t *testing.T) {
	d := gitdiff.Diff{RawText: "diff --git a/f.go b/f.go\n"}
	res := clean(d, "no api key")
	if res.Injected {
		t.Error("clean() result should never be Injected")
	}
	if res.DisplayDiff != d.RawText {
		t.Errorf("DisplayDiff = %q, want the real diff text unmodified", res.DisplayDiff)
	}
	if res.FallbackReason != "no api key" {
		t.Errorf("FallbackReason = %q, want %q", res.FallbackReason, "no api key")
	}
}

func TestTryNoAPIKey(t *testing.T) {
	d := gitdiff.Diff{RawText: "diff --git a/f.go b/f.go\n", Files: []gitdiff.FileChange{
		{Path: "f.go", Status: 'M', HunkRanges: []gitdiff.LineRange{{Start: 1, End: 1}}},
	}}
	cfg := testConfig()
	cfg.APIKey = ""
	res := Try(d, "/unused-repo-root", cfg, rand.New(rand.NewSource(1)))
	if res.Injected {
		t.Error("Try() should never inject without an API key")
	}
	if res.FallbackReason != "no api key" {
		t.Errorf("FallbackReason = %q, want %q", res.FallbackReason, "no api key")
	}
}

func TestTryNoEligibleFile(t *testing.T) {
	d := gitdiff.Diff{RawText: "diff --git a/b.png b/b.png\n", Files: []gitdiff.FileChange{
		{Path: "b.png", Status: 'M', Binary: true, HunkRanges: []gitdiff.LineRange{{Start: 1, End: 1}}},
	}}
	res := Try(d, "/unused-repo-root", testConfig(), rand.New(rand.NewSource(1)))
	if res.Injected {
		t.Error("Try() should never inject with no eligible candidate")
	}
	if res.FallbackReason != "no eligible file" {
		t.Errorf("FallbackReason = %q, want %q", res.FallbackReason, "no eligible file")
	}
}

func TestLineCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\nb", 2},
		// a trailing newline still counts as one more line by this
		// function's convention — harmless for a soft cap (it only ever
		// makes lineCount an undercount-safe overcount by one).
		{"a\nb\n", 3},
	}
	for _, c := range cases {
		if got := lineCount(c.in); got != c.want {
			t.Errorf("lineCount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestHunkContextText(t *testing.T) {
	if got := hunkContextText(nil); got != "(whole file — new file, no prior hunks to bound the target range)" {
		t.Errorf("hunkContextText(nil) = %q", got)
	}
	if got := hunkContextText([]gitdiff.LineRange{{Start: 5, End: 5}}); got != "line 5" {
		t.Errorf("hunkContextText single line = %q, want %q", got, "line 5")
	}
	if got := hunkContextText([]gitdiff.LineRange{{Start: 5, End: 8}}); got != "lines 5-8" {
		t.Errorf("hunkContextText single range = %q, want %q", got, "lines 5-8")
	}
	got := hunkContextText([]gitdiff.LineRange{{Start: 5, End: 5}, {Start: 8, End: 10}})
	if want := "line 5, lines 8-10"; got != want {
		t.Errorf("hunkContextText multiple ranges = %q, want %q", got, want)
	}
}
