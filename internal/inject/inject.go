// Package inject picks one eligible file from a real diff, asks the
// Anthropic API to plant a single subtle bug in it (scoped to the
// diff's own changed-hunk lines, in a category the caller's own RNG
// already chose), validates the result, and regenerates a real
// single-file diff to splice back in. Every failure mode falls back to
// "no injection" rather than erroring the whole review — the caller
// always gets a usable Result.
package inject

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/justinstimatze/spar/internal/gitdiff"
)

// Config bundles the tunables for one injection attempt.
type Config struct {
	APIKey          string
	Model           string
	MaxFileLines    int
	MaxFileBytes    int
	MaxMutatedLines int
	HTTPTimeout     time.Duration
}

// DefaultConfig loads the API key via loadAPIKey's env/.env-walk-up/
// global-config fallback (see api.go) and sets conservative size/scope
// caps.
func DefaultConfig() Config {
	return Config{
		APIKey:          loadAPIKey(),
		Model:           "claude-sonnet-5",
		MaxFileLines:    1500,
		MaxFileBytes:    60_000,
		MaxMutatedLines: 5,
		HTTPTimeout:     45 * time.Second,
	}
}

// RateFromEnv reads SPAR_INJECT_RATE (default 0.4), clamped to [0,1].
func RateFromEnv() float64 {
	v := os.Getenv("SPAR_INJECT_RATE")
	if v == "" {
		return 0.4
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0.4
	}
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// Result is what a trial (injected or not) renders and logs.
type Result struct {
	Injected    bool
	DisplayDiff string

	File        string
	Category    string
	Description string
	Severity    string

	FallbackReason string
}

// codeCategories are bug shapes that only make sense against something
// with real syntax and logic — a boolean, an operator, an argument
// list. proseCategories are the same underlying idea — a small, plausible,
// realistic slip a careful writer could make — restated for a document
// that argues rather than executes: spar reviews ADRs and design docs
// too, and "wrong-operator" means nothing in one. The two lists mirror
// each other one-for-one on purpose (see contentKind and the injector
// system prompt in api.go), so the taxonomy is a translation, not a
// separate invention, for whichever kind of file got picked.
var codeCategories = []string{
	"off-by-one",
	"swapped-argument-order",
	"inverted-boolean",
	"dropped-nil-check",
	"wrong-operator",
	"stale-reference",
	"wrong-config-value",
	"incorrect-default-value",
}

var proseCategories = []string{
	"flipped-recommendation",
	"misattributed-tradeoff",
	"inverted-consequence",
	"dropped-caveat",
	"misstated-comparison",
	"stale-reference",
	"wrong-constraint-value",
	"unsupported-claim",
}

// contentKind classifies a file as "code" or "prose" so category
// selection and the injector's system prompt can match what's actually
// plausible to break in it. Markdown/plaintext-shaped extensions are
// prose; everything else — including configs, which have real syntax
// and values even when they're not a programming language — defaults
// to code.
func contentKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".mdx", ".rst", ".txt", ".adoc":
		return "prose"
	default:
		return "code"
	}
}

func pickCategory(rng *rand.Rand, kind string) string {
	list := codeCategories
	if kind == "prose" {
		list = proseCategories
	}
	return list[rng.Intn(len(list))]
}

// Try attempts one injection. It never returns an error — any failure
// (no key, no eligible file, API error, validation reject, diff regen
// failure) collapses to a clean Result carrying FallbackReason, so a
// caller never has to special-case injection failures separately from
// "the coin flip said clean."
func Try(d gitdiff.Diff, repoRoot string, cfg Config, rng *rand.Rand) Result {
	if cfg.APIKey == "" {
		return clean(d, "no api key")
	}

	candidates := eligibleCandidates(d, cfg)
	if len(candidates) == 0 {
		return clean(d, "no eligible file")
	}
	fc := candidates[rng.Intn(len(candidates))]

	before, after, err := gitdiff.FileContent(repoRoot, fc, d.Staged)
	if err != nil {
		return clean(d, "reading file content: "+err.Error())
	}
	if len(after) > cfg.MaxFileBytes || lineCount(after) > cfg.MaxFileLines {
		return clean(d, "candidate file exceeds size cap")
	}

	kind := contentKind(fc.Path)
	category := pickCategory(rng, kind)

	// The model is told the exact allowed line ranges but doesn't
	// always land inside them on the first try — more likely on prose,
	// where a real edit often touches several scattered regions of a
	// document, leaving small unchanged gaps between hunks the model
	// can still plausibly (but invalidly) reach for. One retry at the
	// same candidate is cheap and often enough; callAPI already retries
	// its own transient failures, so an API error here isn't retried
	// again — only a validation rejection is.
	var mr mutateResult
	valid := false
	for attempt := 0; attempt < 2; attempt++ {
		mr, err = callAPI(cfg, fc.Path, before, after, hunkContextText(fc.HunkRanges), category, kind)
		if err != nil {
			return clean(d, "api error: "+err.Error())
		}
		if _, ok := validateMutation(after, mr.MutatedContent, fc.HunkRanges, cfg); ok {
			valid = true
			break
		}
	}
	if !valid {
		return clean(d, "mutation failed validation")
	}

	fileDiff, err := gitdiff.RegenerateFileDiff(before, mr.MutatedContent, fc.Path)
	if err != nil {
		return clean(d, "diff regen failed: "+err.Error())
	}

	spliced, err := d.SpliceFile(fc.Path, fileDiff)
	if err != nil {
		return clean(d, "splice failed: "+err.Error())
	}

	return Result{
		Injected:    true,
		DisplayDiff: spliced,
		File:        fc.Path,
		Category:    mr.Category,
		Description: mr.Description,
		Severity:    mr.Severity,
	}
}

func clean(d gitdiff.Diff, reason string) Result {
	return Result{Injected: false, DisplayDiff: d.RawText, FallbackReason: reason}
}

// eligibleCandidates excludes binary files (nothing textual to mutate),
// deleted files (no after-content), and renamed files (a regenerated
// diff can't reproduce "rename from"/"rename to" headers, and splicing
// one in for a renamed file would silently drop real rename metadata
// the reviewer would otherwise see). It doesn't know file content or
// size yet — that's fetched per-candidate after one is picked, and
// checked in Try before the file is sent to the model; a candidate that
// turns out to be oversized falls this trial back to clean rather than
// retrying a different candidate.
func eligibleCandidates(d gitdiff.Diff, cfg Config) []gitdiff.FileChange {
	var out []gitdiff.FileChange
	for _, fc := range d.Files {
		if fc.Binary || fc.Status == 'D' || fc.Status == 'R' {
			continue
		}
		if len(fc.HunkRanges) == 0 {
			continue
		}
		out = append(out, fc)
	}
	return out
}

// validateMutation enforces the scope/size guarantees that make the
// injected bug plausible and bounded rather than a rewritten file: the
// mutation must be a real, non-empty change, must touch no more lines
// than cfg.MaxMutatedLines, and every touched line must fall inside one
// of the reviewer's own real changed-hunk ranges (skipped for a
// brand-new file, whose "allowed" range is the whole file — a new file
// has no established hunk boundary to respect).
func validateMutation(original, mutated string, allowed []gitdiff.LineRange, cfg Config) ([]int, bool) {
	if mutated == "" || mutated == original {
		return nil, false
	}
	if len(original) > cfg.MaxFileBytes || len(mutated) > cfg.MaxFileBytes {
		return nil, false
	}

	diff, err := gitdiff.RegenerateFileDiff(original, mutated, "validate")
	if err != nil {
		return nil, false
	}
	touched := gitdiff.LineNumbersTouched(diff)
	if len(touched) == 0 || len(touched) > cfg.MaxMutatedLines {
		return nil, false
	}
	if len(allowed) == 0 {
		return touched, true
	}
	for _, ln := range touched {
		if !withinAny(ln, allowed) {
			return nil, false
		}
	}
	return touched, true
}

func withinAny(line int, ranges []gitdiff.LineRange) bool {
	for _, r := range ranges {
		if line >= r.Start && line <= r.End {
			return true
		}
	}
	return false
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func hunkContextText(ranges []gitdiff.LineRange) string {
	if len(ranges) == 0 {
		return "(whole file — new file, no prior hunks to bound the target range)"
	}
	parts := make([]string, len(ranges))
	for i, r := range ranges {
		if r.Start == r.End {
			parts[i] = fmt.Sprintf("line %d", r.Start)
		} else {
			parts[i] = fmt.Sprintf("lines %d-%d", r.Start, r.End)
		}
	}
	return strings.Join(parts, ", ")
}
