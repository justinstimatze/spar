package main

import (
	"testing"
	"time"

	"github.com/justinstimatze/spar/internal/livestate"
	"github.com/justinstimatze/spar/internal/store"
)

func TestEnvBool(t *testing.T) {
	cases := map[string]bool{"1": true, "true": true, "yes": true, "0": false, "false": false, "": false, "nonsense": false}
	for v, want := range cases {
		t.Setenv("SPAR_TEST_BOOL", v)
		if got := envBool("SPAR_TEST_BOOL"); got != want {
			t.Errorf("envBool(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestEnvDuration(t *testing.T) {
	t.Setenv("SPAR_TEST_DUR", "")
	if got := envDuration("SPAR_TEST_DUR", 45*time.Minute); got != 45*time.Minute {
		t.Errorf("envDuration with unset env = %v, want the default", got)
	}
	t.Setenv("SPAR_TEST_DUR", "2h")
	if got := envDuration("SPAR_TEST_DUR", 45*time.Minute); got != 2*time.Hour {
		t.Errorf("envDuration(2h) = %v, want 2h", got)
	}
	t.Setenv("SPAR_TEST_DUR", "not-a-duration")
	if got := envDuration("SPAR_TEST_DUR", 45*time.Minute); got != 45*time.Minute {
		t.Errorf("envDuration with an unparseable value = %v, want the default", got)
	}
}

func TestEnvRevealMode(t *testing.T) {
	cases := map[string]string{"": "silent", "ask": "ask", "silent": "silent", "nonsense": "silent"}
	for v, want := range cases {
		t.Setenv("SPAR_LIVE_REVEAL_MODE", v)
		if got := envRevealMode(); got != want {
			t.Errorf("envRevealMode() with SPAR_LIVE_REVEAL_MODE=%q = %q, want %q", v, got, want)
		}
	}
}

// TestLogExpiredPropagatesDiffMutationGroundTruth is a regression test for
// a review finding: an expired notify-mode (diff-mutation) plant's ground
// truth was silently dropped when logExpired logged its OutcomeUnrevealed
// trial — the one point that ground truth can never be recovered, since
// the pending file (its only copy) is deleted by the sweep.
func TestLogExpiredPropagatesDiffMutationGroundTruth(t *testing.T) {
	isolateHome(t)
	const session = "test-session-0123456789"
	gt := livestate.DiffMutationGroundTruth{
		Category:    "off-by-one",
		File:        "internal/foo/foo.go",
		Severity:    "medium",
		Description: "loop bound shifted by one",
		DiffHash:    "abc123",
	}
	if _, err := livestate.WritePendingDiffMutation(session, "", gt); err != nil {
		t.Fatalf("WritePendingDiffMutation: %v", err)
	}

	// A near-zero TTL means the plant is already "expired" the instant
	// it's written, without needing to hand-edit the pending file's
	// PlantedAt on disk.
	logExpired(1 * time.Nanosecond)

	trials := readLoggedTrials(t)
	if len(trials) != 1 {
		t.Fatalf("got %d logged trials, want 1", len(trials))
	}
	tr := trials[0]
	if tr.Outcome != store.OutcomeUnrevealed {
		t.Fatalf("Outcome = %q, want %q", tr.Outcome, store.OutcomeUnrevealed)
	}
	if tr.LiveKind != store.LiveKindDiffMutation {
		t.Errorf("LiveKind = %q, want %q", tr.LiveKind, store.LiveKindDiffMutation)
	}
	if tr.InjectedFile != gt.File || tr.InjectedSeverity != gt.Severity || tr.InjectedDescription != gt.Description || tr.DiffHash != gt.DiffHash {
		t.Errorf("ground truth was not propagated into the unrevealed trial: %+v", tr)
	}
}

func TestCorroborateEmptyPath(t *testing.T) {
	if corroborate("") != "" {
		t.Error("corroborate with no transcript path should return empty")
	}
}

func TestCorroborateMissingFile(t *testing.T) {
	if corroborate("/nonexistent/transcript.jsonl") != "" {
		t.Error("corroborate against a missing transcript should return empty, not error out")
	}
}
