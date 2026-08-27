package main

import (
	"testing"
	"time"
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
