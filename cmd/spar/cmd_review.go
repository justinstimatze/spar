package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/justinstimatze/spar/internal/gitdiff"
	"github.com/justinstimatze/spar/internal/inject"
	"github.com/justinstimatze/spar/internal/render"
	"github.com/justinstimatze/spar/internal/store"
)

func cmdReview(args []string) {
	fl := flag.NewFlagSet("review", flag.ExitOnError)
	rate := fl.Float64("rate", inject.RateFromEnv(), "probability of injecting a bug this trial (0-1)")
	_ = fl.Parse(args)
	if *rate < 0 {
		*rate = 0
	}
	if *rate > 1 {
		*rate = 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spar:", err)
		os.Exit(1)
	}
	repoRoot, err := gitdiff.RepoRoot(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spar:", err)
		os.Exit(1)
	}

	diff, err := gitdiff.Capture(repoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spar:", err)
		os.Exit(1)
	}
	if diff.Empty() {
		fmt.Println("spar: no staged or unstaged changes to review.")
		return
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	attempted := rng.Float64() < *rate
	var result inject.Result
	if attempted {
		result = inject.Try(diff, repoRoot, inject.DefaultConfig(), rng)
	} else {
		result = inject.Result{Injected: false, DisplayDiff: diff.RawText}
	}

	if err := render.Show(os.Stdout, result.DisplayDiff); err != nil {
		fmt.Fprintln(os.Stderr, "spar: rendering diff:", err)
	}
	flagged, flagText := render.Prompt(bufio.NewReader(os.Stdin), os.Stdout)
	outcome := store.ComputeOutcome(result.Injected, flagged)
	render.Reveal(os.Stdout, result, flagged, flagText, outcome)

	trial := store.Trial{
		TS:                   time.Now(),
		Project:              store.ResolveProjectName(repoRoot),
		DiffHash:             diffHash(diff.RawText),
		Injected:             result.Injected,
		InjectedFile:         result.File,
		InjectedCategory:     result.Category,
		InjectedSeverity:     result.Severity,
		InjectedDescription:  result.Description,
		UserFlagged:          flagged,
		UserFlagText:         flagText,
		Outcome:              outcome,
		InjectAttempted:      attempted,
		InjectFallbackReason: result.FallbackReason,
	}
	path, err := store.LogPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spar: could not resolve log path:", err)
		return
	}
	if err := store.Append(path, trial); err != nil {
		fmt.Fprintln(os.Stderr, "spar: could not log trial:", err)
	}
}

func diffHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:16]
}
