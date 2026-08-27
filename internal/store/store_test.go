package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendReadAllRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")

	trials := []Trial{
		{TS: time.Now(), Project: "spar", DiffHash: "abc", Injected: true, InjectedFile: "foo.go", InjectedCategory: "off-by-one", InjectedDescription: "loop bound off by one character", UserFlagged: true, Outcome: OutcomeCatch},
		{TS: time.Now(), Project: "spar", DiffHash: "def", Injected: false, UserFlagged: false, Outcome: OutcomeTrueNegative},
	}
	for _, tr := range trials {
		if err := Append(path, tr); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != len(trials) {
		t.Fatalf("got %d trials, want %d", len(got), len(trials))
	}
	if got[0].DiffHash != "abc" || got[1].DiffHash != "def" {
		t.Fatalf("unexpected order/content: %+v", got)
	}
	if got[0].InjectedDescription != "loop bound off by one character" {
		t.Errorf("InjectedDescription didn't round-trip: got %q", got[0].InjectedDescription)
	}
	if got[0].SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion not stamped: got %d", got[0].SchemaVersion)
	}
}

func TestAppendReadAllRoundtripLiveMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")

	tr := Trial{
		TS:                   time.Now(),
		Project:              "spar",
		Mode:                 ModeLive,
		SessionID:            "abc-123",
		Injected:             true,
		InjectedCategory:     "misordered-causality",
		InjectedDescription:  "claimed the retry was added before the incident",
		CaughtDegree:         "partial",
		UserFlagged:          true,
		UserFlagText:         "something about the ordering felt off",
		LiveExchangeVerified: true,
		Outcome:              OutcomeCatch,
	}
	if err := Append(path, tr); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d trials, want 1", len(got))
	}
	g := got[0]
	if g.Mode != ModeLive || g.SessionID != "abc-123" || g.CaughtDegree != "partial" || !g.LiveExchangeVerified {
		t.Errorf("live-mode fields didn't round-trip: %+v", g)
	}
	if g.DiffHash != "" {
		t.Errorf("DiffHash = %q, want empty for a live trial (omitempty, never set)", g.DiffHash)
	}
}

func TestAppendReadAllRoundtripFixupFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")

	tr := Trial{
		TS:               time.Now(),
		Project:          "spar",
		Mode:             ModeLive,
		SessionID:        "abc-123",
		Injected:         true,
		InjectedCategory: "misordered-causality",
		OriginalText:     "the retry was added after an incident in testing",
		CorrectedText:    "the retry was caught by planning, before any code existed",
		TranscriptPath:   "/home/user/.claude/projects/-home-user-spar/abc-123.jsonl",
		Outcome:          OutcomeMiss,
	}
	if err := Append(path, tr); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d trials, want 1", len(got))
	}
	g := got[0]
	if g.OriginalText != tr.OriginalText || g.CorrectedText != tr.CorrectedText || g.TranscriptPath != tr.TranscriptPath {
		t.Errorf("fixup fields didn't round-trip: %+v", g)
	}
}

func TestTrialFixupFieldsOmittedWhenEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	if err := Append(path, Trial{TS: time.Now(), Project: "spar", Outcome: OutcomeCatch}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, field := range []string{"original_text", "corrected_text", "transcript_path"} {
		if strings.Contains(string(data), field) {
			t.Errorf("logged line unexpectedly contains %q for a trial that never set it: %s", field, data)
		}
	}
}

func TestReadAllMissingFile(t *testing.T) {
	got, err := ReadAll(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("ReadAll on missing file should not error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil trials, got %v", got)
	}
}

func TestReadAllSkipsMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	if err := Append(path, Trial{TS: time.Now(), Project: "p", Outcome: OutcomeTrueNegative}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Corrupt line appended directly, bypassing Append's JSON marshal.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("not json\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()
	if err := Append(path, Trial{TS: time.Now(), Project: "p", Outcome: OutcomeCatch, Injected: true, UserFlagged: true}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected malformed line skipped, got %d trials", len(got))
	}
}

func TestComputeOutcome(t *testing.T) {
	cases := []struct {
		injected, flagged bool
		want              string
	}{
		{true, true, OutcomeCatch},
		{true, false, OutcomeMiss},
		{false, true, OutcomeFalsePositive},
		{false, false, OutcomeTrueNegative},
	}
	for _, c := range cases {
		if got := ComputeOutcome(c.injected, c.flagged); got != c.want {
			t.Errorf("ComputeOutcome(%v,%v) = %q, want %q", c.injected, c.flagged, got, c.want)
		}
	}
}

func TestParseSince(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"30d", 30 * 24 * time.Hour, false},
		{"2w", 14 * 24 * time.Hour, false},
		{"720h", 720 * time.Hour, false},
		{"", 0, true},
		{"not-a-duration", 0, true},
	}
	for _, c := range cases {
		got, err := ParseSince(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseSince(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseSince(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTrendByWeekGroupsIntoConsecutiveWindows(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	trials := []Trial{
		{TS: start, Outcome: OutcomeCatch},
		{TS: start.Add(2 * 24 * time.Hour), Outcome: OutcomeMiss},
		{TS: start.Add(9 * 24 * time.Hour), Outcome: OutcomeCatch}, // week 2
		{TS: start.Add(10 * 24 * time.Hour), Outcome: OutcomeCatch},
		{TS: start.Add(30 * time.Hour), Outcome: OutcomeTrueNegative}, // no catch/miss signal, excluded
	}

	buckets := TrendByWeek(trials)
	if len(buckets) != 2 {
		t.Fatalf("got %d buckets, want 2: %+v", len(buckets), buckets)
	}
	if buckets[0].Catches != 1 || buckets[0].Misses != 1 {
		t.Errorf("week 1: got %+v, want 1 catch 1 miss", buckets[0])
	}
	if buckets[1].Catches != 2 || buckets[1].Misses != 0 {
		t.Errorf("week 2: got %+v, want 2 catches 0 misses", buckets[1])
	}
	if !buckets[0].Start.Equal(start) {
		t.Errorf("week 1 start = %v, want %v", buckets[0].Start, start)
	}
	if !buckets[1].Start.Equal(start.Add(7 * 24 * time.Hour)) {
		t.Errorf("week 2 start = %v, want %v", buckets[1].Start, start.Add(7*24*time.Hour))
	}
}

func TestTrendByWeekEmptyOnNoInjectedTrials(t *testing.T) {
	trials := []Trial{{TS: time.Now(), Outcome: OutcomeTrueNegative}, {TS: time.Now(), Outcome: OutcomeFalsePositive}}
	if got := TrendByWeek(trials); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestTrendByWeekExcludesUnengaged guards the honesty property silent-mode
// live reveal depends on: a trial with no real signal either way must not
// quietly count as a miss just because it's not a catch.
func TestTrendByWeekExcludesUnengaged(t *testing.T) {
	trials := []Trial{
		{TS: time.Now(), Outcome: OutcomeCatch},
		{TS: time.Now(), Outcome: OutcomeUnengaged},
		{TS: time.Now(), Outcome: OutcomeUnengaged},
	}
	weeks := TrendByWeek(trials)
	if len(weeks) != 1 {
		t.Fatalf("got %d buckets, want 1: %+v", len(weeks), weeks)
	}
	if weeks[0].Catches != 1 || weeks[0].Misses != 0 {
		t.Errorf("unengaged trials leaked into catch/miss counts: %+v", weeks[0])
	}
}

func TestCategoryBreakdownSortsWorstCatchRateFirst(t *testing.T) {
	trials := []Trial{
		{InjectedCategory: "off-by-one", Outcome: OutcomeCatch},
		{InjectedCategory: "off-by-one", Outcome: OutcomeCatch},
		{InjectedCategory: "misordered-causality", Outcome: OutcomeMiss},
		{InjectedCategory: "misordered-causality", Outcome: OutcomeMiss},
		{InjectedCategory: "misordered-causality", Outcome: OutcomeCatch},
		{Outcome: OutcomeTrueNegative}, // no category, no catch/miss signal — excluded
	}

	got := CategoryBreakdown(trials)
	if len(got) != 2 {
		t.Fatalf("got %d categories, want 2: %+v", len(got), got)
	}
	if got[0].Category != "misordered-causality" || got[0].Catches != 1 || got[0].Misses != 2 {
		t.Errorf("worst category = %+v, want misordered-causality 1/2", got[0])
	}
	if got[1].Category != "off-by-one" || got[1].Catches != 2 || got[1].Misses != 0 {
		t.Errorf("best category = %+v, want off-by-one 2/0", got[1])
	}
}

func TestFilterBySinceAndProject(t *testing.T) {
	now := time.Now()
	trials := []Trial{
		{TS: now.Add(-1 * time.Hour), Project: "spar", Outcome: OutcomeCatch},
		{TS: now.Add(-72 * time.Hour), Project: "spar", Outcome: OutcomeMiss},
		{TS: now.Add(-1 * time.Hour), Project: "other", Outcome: OutcomeCatch},
	}

	got, err := Filter(trials, "24h", "")
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("since filter: got %d, want 2", len(got))
	}

	got, err = Filter(trials, "", "spar")
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("project filter: got %d, want 2", len(got))
	}
}
