package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/justinstimatze/spar/internal/livestate"
	"github.com/justinstimatze/spar/internal/store"
)

// cmdLiveReveal closes out one live-mode pending plant: the model calls
// this as an ordinary Bash command once the reveal exchange with the user
// has happened. --caught is the product's entire output metric, so this
// validates structurally against the pending file rather than trusting the
// call at face value — see internal/livestate's package doc for why the
// cooldown marker this session was fired under is untouched by any of this.
func cmdLiveReveal(args []string) {
	fl := flag.NewFlagSet("live-reveal", flag.ExitOnError)
	session := fl.String("session", "", "session id from the reveal instructions")
	token := fl.String("token", "", "token from the reveal instructions")
	caught := fl.String("caught", "", "yes|no|partial|unengaged|not_planted")
	description := fl.String("description", "", "what was planted, in the model's own words")
	originalText := fl.String("original-text", "", "your exact prior wording, verbatim, if reproducible — optional, best-effort")
	correctedText := fl.String("corrected-text", "", "the true fact in its place, stated plainly — optional")
	userNote := fl.String("user-note", "", "the user's response, paraphrased")
	_ = fl.Parse(args)

	fail := func(reason string) {
		fmt.Fprintln(os.Stderr, "spar live-reveal:", reason)
		os.Exit(1)
	}

	if *session == "" || *token == "" {
		fail("--session and --token are required")
	}
	switch *caught {
	case "yes", "no", "partial", "unengaged", "not_planted":
	default:
		fail(`--caught must be one of: yes, no, partial, unengaged, not_planted`)
	}

	ttl := envDuration("SPAR_LIVE_PENDING_TTL", defaultLivePendingTTL)
	pending, ok, err := livestate.ReadPending(*session)
	if err != nil {
		fail(err.Error())
	}
	if !ok {
		fail("no pending plant for this session — already revealed, or it expired")
	}
	if livestate.Expired(pending, ttl) {
		fail("pending plant already expired past its TTL")
	}
	if pending.Token != *token {
		fail("token does not match the pending plant")
	}

	trial := store.Trial{
		TS:               time.Now(),
		Project:          store.ResolveProjectName(mustGetwd()),
		Mode:             store.ModeLive,
		SessionID:        *session,
		InjectedCategory: pending.Category,
		LiveKind:         pending.LiveKind,
	}

	switch *caught {
	case "not_planted":
		trial.Injected = false
		trial.Outcome = store.OutcomeNotPlanted
	case "unengaged":
		trial.Injected = true
		trial.InjectedDescription = *description
		trial.CaughtDegree = *caught
		trial.UserFlagText = *userNote
		trial.Outcome = store.OutcomeUnengaged
		trial.LiveExchangeVerified = pending.Corroborated
	default: // yes, no, partial
		trial.Injected = true
		trial.InjectedDescription = *description
		trial.CaughtDegree = *caught
		trial.UserFlagged = *caught != "no"
		trial.UserFlagText = *userNote
		if trial.UserFlagged {
			trial.Outcome = store.OutcomeCatch
		} else {
			trial.Outcome = store.OutcomeMiss
		}
		trial.LiveExchangeVerified = pending.Corroborated
	}

	// A notify-mode (real diff-mutation) trial already has exact ground
	// truth from the moment it was planted — spar computed it, not the
	// model. Prefer it over whatever the model self-reported: File and
	// Severity have no flag equivalent at all (notify-mode-only axes),
	// and InjectedDescription is more accurate from spar's own record
	// than from the model's memory of a diff it merely narrated.
	if trial.Injected && pending.LiveKind == store.LiveKindDiffMutation {
		trial.InjectedFile = pending.InjectedFile
		trial.InjectedSeverity = pending.InjectedSeverity
		trial.DiffHash = pending.DiffHash
		if pending.InjectedDescription != "" {
			trial.InjectedDescription = pending.InjectedDescription
		}
	}

	// Only ever trust --original-text for spar live-fixup once it's proven
	// to be a real, exact substring of the plant's own corroborated text —
	// never the model's word alone. A false or empty quote just leaves the
	// trial fixup-ineligible; it never gets recorded as "verified" by
	// accident (Go's strings.Contains(s, "") is true for any s, including
	// s == "", so the explicit non-empty guard below is required, not
	// redundant with Contains).
	if trial.Injected && *originalText != "" && strings.Contains(pending.PlantText, *originalText) {
		trial.OriginalText = *originalText
		trial.CorrectedText = *correctedText
		trial.TranscriptPath = pending.TranscriptPath
	}

	path, err := store.LogPath()
	if err != nil {
		fail(err.Error())
	}
	if err := store.Append(path, trial); err != nil {
		fail(err.Error())
	}
	if err := livestate.ClosePending(*session); err != nil {
		fail(err.Error())
	}

	fmt.Println("spar: live trial logged —", trial.Outcome)
}
