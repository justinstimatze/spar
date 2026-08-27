package main

import (
	"fmt"
	"os"

	"github.com/justinstimatze/spar/internal/livestate"
)

// cmdLiveInduce arms a one-shot marker that makes the next spar live-hook
// invocation plant regardless of cooldown — a manual "make it happen now"
// for testing/dogfooding, so you don't have to wait out
// SPAR_LIVE_COOLDOWN to see the mechanism fire. It has no effect if a plant
// is already pending reveal for the session that next prompts.
func cmdLiveInduce(args []string) {
	if err := livestate.WriteForce(); err != nil {
		fmt.Fprintf(os.Stderr, "spar: could not arm live induce: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("spar: next prompt will plant a live-mode error, cooldown ignored")
}
