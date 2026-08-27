package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/justinstimatze/spar/internal/store"
)

func cmdStats(args []string) {
	fl := flag.NewFlagSet("stats", flag.ExitOnError)
	since := fl.String("since", "", "only include trials within this window, e.g. 30d, 2w, 720h")
	project := fl.String("project", "", "only include trials for this project name")
	trend := fl.Bool("trend", false, "show catch rate by week and by category")
	_ = fl.Parse(args)

	path, err := store.LogPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spar:", err)
		os.Exit(1)
	}
	trials, err := store.ReadAll(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spar:", err)
		os.Exit(1)
	}
	trials, err = store.Filter(trials, *since, *project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spar:", err)
		os.Exit(1)
	}

	if len(trials) == 0 {
		fmt.Println("spar: no trials logged yet. Run `spar review` in a repo with uncommitted changes.")
		return
	}

	var catches, misses, falsePositives, trueNegatives, unrevealed, notPlanted, unengaged int
	var liveTrials int
	for _, t := range trials {
		if t.Mode == store.ModeLive {
			liveTrials++
		}
		switch t.Outcome {
		case store.OutcomeCatch:
			catches++
		case store.OutcomeMiss:
			misses++
		case store.OutcomeFalsePositive:
			falsePositives++
		case store.OutcomeTrueNegative:
			trueNegatives++
		case store.OutcomeUnrevealed:
			unrevealed++
		case store.OutcomeNotPlanted:
			notPlanted++
		case store.OutcomeUnengaged:
			unengaged++
		}
	}

	fmt.Printf("spar stats — %d trial(s) (%d review, %d live)\n", len(trials), len(trials)-liveTrials, liveTrials)
	total := catches + misses
	if total > 0 {
		fmt.Printf("  catch rate: %.0f%% (%d catches / %d injected trials)\n", 100*float64(catches)/float64(total), catches, total)
	} else {
		fmt.Println("  catch rate: n/a (no injected trials yet)")
	}
	fmt.Printf("  catches: %d   misses: %d   false positives: %d   true negatives: %d\n",
		catches, misses, falsePositives, trueNegatives)
	if unrevealed > 0 || notPlanted > 0 || unengaged > 0 {
		fmt.Printf("  live-only: %d unrevealed   %d not planted   %d unengaged\n", unrevealed, notPlanted, unengaged)
	}

	if *trend {
		printTrend(trials)
	}
}

// printTrend answers the question spar previously couldn't: whether catch
// rate is actually moving, and whether the same category keeps getting
// missed (docs/DESIGN.md's open question on Frey's obsolescence litmus
// test). Weekly buckets and category breakdown both draw from the same
// filtered trial set cmdStats already computed.
func printTrend(trials []store.Trial) {
	weeks := store.TrendByWeek(trials)
	if len(weeks) == 0 {
		fmt.Println("\n  trend: n/a (no injected trials yet)")
		return
	}
	fmt.Println("\n  catch rate by week:")
	for _, b := range weeks {
		fmt.Printf("    %s: %.0f%% (%d/%d)\n", b.Start.Format("2006-01-02"), 100*b.Rate(), b.Catches, b.Catches+b.Misses)
	}
	fmt.Println("\n  catch rate by category:")
	for _, c := range store.CategoryBreakdown(trials) {
		fmt.Printf("    %-32s %.0f%% (%d/%d)\n", c.Category, 100*c.Rate(), c.Catches, c.Catches+c.Misses)
	}
}
