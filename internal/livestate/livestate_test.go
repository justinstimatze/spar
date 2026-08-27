package livestate

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/justinstimatze/spar/internal/store"
)

func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

const testSession = "test-session-0123456789"

func TestWritePendingReadPendingRoundtrip(t *testing.T) {
	isolateHome(t)
	p, err := WritePending(testSession, "misordered-causality", "/tmp/fake-transcript.jsonl")
	if err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	if p.Token == "" {
		t.Fatal("WritePending did not generate a token")
	}

	got, ok, err := ReadPending(testSession)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if !ok {
		t.Fatal("ReadPending: expected a pending entry")
	}
	if got.Category != "misordered-causality" || got.Token != p.Token {
		t.Errorf("ReadPending = %+v, want it to match what WritePending wrote", got)
	}
	if got.TranscriptPath != "/tmp/fake-transcript.jsonl" {
		t.Errorf("TranscriptPath = %q, want it to round-trip from WritePending", got.TranscriptPath)
	}
}

func TestReadPendingNoneExists(t *testing.T) {
	isolateHome(t)
	_, ok, err := ReadPending(testSession)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if ok {
		t.Error("ReadPending should report ok=false with nothing written")
	}
}

func TestWritePendingRejectsDoubleFire(t *testing.T) {
	isolateHome(t)
	if _, err := WritePending(testSession, "misattributed-precedent", ""); err != nil {
		t.Fatalf("first WritePending: %v", err)
	}
	if _, err := WritePending(testSession, "inverted-tradeoff-direction", ""); err == nil {
		t.Error("a second WritePending for the same session should fail (O_CREATE|O_EXCL) rather than silently overwrite the in-flight token")
	}
	// The original entry must survive untouched.
	got, ok, err := ReadPending(testSession)
	if err != nil || !ok {
		t.Fatalf("ReadPending after failed double-write: ok=%v err=%v", ok, err)
	}
	if got.Category != "misattributed-precedent" {
		t.Errorf("Category = %q, the double-write should not have changed it", got.Category)
	}
}

func TestClosePendingRemovesEntry(t *testing.T) {
	isolateHome(t)
	if _, err := WritePending(testSession, "misattributed-constraint-source", ""); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	if err := ClosePending(testSession); err != nil {
		t.Fatalf("ClosePending: %v", err)
	}
	_, ok, err := ReadPending(testSession)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if ok {
		t.Error("ReadPending should report ok=false after ClosePending")
	}
}

func TestClosePendingMissingIsNotAnError(t *testing.T) {
	isolateHome(t)
	if err := ClosePending(testSession); err != nil {
		t.Errorf("ClosePending on a session with nothing pending should be a no-op, got %v", err)
	}
}

func TestSetCorroboratedPersists(t *testing.T) {
	isolateHome(t)
	if _, err := WritePending(testSession, "misordered-causality", ""); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	if err := SetCorroborated(testSession, "the corroborated turn's real text"); err != nil {
		t.Fatalf("SetCorroborated(non-empty): %v", err)
	}
	got, ok, err := ReadPending(testSession)
	if err != nil || !ok {
		t.Fatalf("ReadPending: ok=%v err=%v", ok, err)
	}
	if !got.Corroborated {
		t.Error("ReadPending after SetCorroborated(non-empty) should show Corroborated=true")
	}
	if got.PlantText != "the corroborated turn's real text" {
		t.Errorf("PlantText = %q, want it to round-trip from SetCorroborated", got.PlantText)
	}

	if err := SetCorroborated(testSession, ""); err != nil {
		t.Fatalf("SetCorroborated(empty): %v", err)
	}
	got, ok, err = ReadPending(testSession)
	if err != nil || !ok {
		t.Fatalf("ReadPending: ok=%v err=%v", ok, err)
	}
	if got.Corroborated {
		t.Error("ReadPending after SetCorroborated(empty) should show Corroborated=false")
	}
	if got.PlantText != "" {
		t.Errorf("PlantText = %q, want it cleared by SetCorroborated(empty)", got.PlantText)
	}
}

func TestSetCorroboratedNoPendingIsNoop(t *testing.T) {
	isolateHome(t)
	if err := SetCorroborated(testSession, "some text"); err != nil {
		t.Errorf("SetCorroborated with nothing pending should be a no-op, got %v", err)
	}
}

func TestWriteForceConsumeForceRoundtrip(t *testing.T) {
	isolateHome(t)
	if err := WriteForce(); err != nil {
		t.Fatalf("WriteForce: %v", err)
	}
	forced, err := ConsumeForce()
	if err != nil {
		t.Fatalf("ConsumeForce: %v", err)
	}
	if !forced {
		t.Error("ConsumeForce should report true right after WriteForce")
	}

	forced, err = ConsumeForce()
	if err != nil {
		t.Fatalf("second ConsumeForce: %v", err)
	}
	if forced {
		t.Error("ConsumeForce should be one-shot — the second call should report false")
	}
}

func TestConsumeForceNotArmedReturnsFalse(t *testing.T) {
	isolateHome(t)
	forced, err := ConsumeForce()
	if err != nil {
		t.Fatalf("ConsumeForce: %v", err)
	}
	if forced {
		t.Error("ConsumeForce with nothing armed should report false")
	}
}

func TestWritePendingStartsUnasked(t *testing.T) {
	isolateHome(t)
	if _, err := WritePending(testSession, "misordered-causality", ""); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	got, ok, err := ReadPending(testSession)
	if err != nil || !ok {
		t.Fatalf("ReadPending: ok=%v err=%v", ok, err)
	}
	if got.Asked {
		t.Error("a freshly-planted entry should not be Asked yet")
	}
}

func TestMarkAskedPersists(t *testing.T) {
	isolateHome(t)
	if _, err := WritePending(testSession, "misordered-causality", ""); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	if err := MarkAsked(testSession); err != nil {
		t.Fatalf("MarkAsked: %v", err)
	}
	got, ok, err := ReadPending(testSession)
	if err != nil || !ok {
		t.Fatalf("ReadPending: ok=%v err=%v", ok, err)
	}
	if !got.Asked {
		t.Error("ReadPending after MarkAsked should show Asked=true")
	}
	if got.Category != "misordered-causality" || got.Token == "" {
		t.Errorf("MarkAsked should not disturb the rest of the pending entry: %+v", got)
	}
}

func TestMarkAskedNoPendingIsNoop(t *testing.T) {
	isolateHome(t)
	if err := MarkAsked(testSession); err != nil {
		t.Errorf("MarkAsked with nothing pending should be a no-op, got %v", err)
	}
}

func TestShouldNudgeStatsNeverFiredYet(t *testing.T) {
	isolateHome(t)
	due, err := ShouldNudgeStats(24 * time.Hour)
	if err != nil {
		t.Fatalf("ShouldNudgeStats: %v", err)
	}
	if !due {
		t.Error("a marker that's never been stamped should be due")
	}
}

func TestShouldNudgeStatsRespectsInterval(t *testing.T) {
	isolateHome(t)
	if err := MarkStatsNudged(); err != nil {
		t.Fatalf("MarkStatsNudged: %v", err)
	}
	due, err := ShouldNudgeStats(24 * time.Hour)
	if err != nil {
		t.Fatalf("ShouldNudgeStats: %v", err)
	}
	if due {
		t.Error("a marker just stamped should not be due again inside the interval")
	}
}

func TestExpired(t *testing.T) {
	fresh := Pending{PlantedAt: time.Now()}
	if Expired(fresh, time.Hour) {
		t.Error("a freshly-planted entry should not be expired")
	}
	old := Pending{PlantedAt: time.Now().Add(-2 * time.Hour)}
	if !Expired(old, time.Hour) {
		t.Error("a 2h-old entry should be expired against a 1h ttl")
	}
}

// backdatePending rewrites the on-disk pending entry's PlantedAt, since
// WritePending always stamps "now" and SweepExpired/Expired tests need an
// entry that's actually old.
func backdatePending(t *testing.T, sessionID string, age time.Duration) {
	t.Helper()
	path, err := pendingPath(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p Pending
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p.PlantedAt = time.Now().Add(-age)
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSweepExpiredClosesOldEntriesOnly(t *testing.T) {
	isolateHome(t)
	if _, err := WritePending("old-session-0123456789", "misordered-causality", ""); err != nil {
		t.Fatal(err)
	}
	backdatePending(t, "old-session-0123456789", 10*time.Hour)
	if _, err := WritePending("fresh-session-0123456789", "misattributed-precedent", ""); err != nil {
		t.Fatal(err)
	}

	expired, err := SweepExpired(8 * time.Hour)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if len(expired) != 1 || expired[0].SessionID != "old-session-0123456789" {
		t.Fatalf("SweepExpired returned %+v, want exactly the old session", expired)
	}

	if _, ok, _ := ReadPending("old-session-0123456789"); ok {
		t.Error("the expired entry should have been closed by the sweep")
	}
	if _, ok, _ := ReadPending("fresh-session-0123456789"); !ok {
		t.Error("the fresh entry should survive the sweep")
	}
}

func TestShouldFireNeverFiredYet(t *testing.T) {
	isolateHome(t)
	fire, err := ShouldFire(testSession, time.Hour)
	if err != nil {
		t.Fatalf("ShouldFire: %v", err)
	}
	if !fire {
		t.Error("a session with no cooldown marker should be eligible to fire")
	}
}

func TestShouldFireRespectsCooldown(t *testing.T) {
	isolateHome(t)
	if err := MarkFired(testSession); err != nil {
		t.Fatalf("MarkFired: %v", err)
	}
	fire, err := ShouldFire(testSession, time.Hour)
	if err != nil {
		t.Fatalf("ShouldFire: %v", err)
	}
	if fire {
		t.Error("a session that just fired should not be eligible again inside the cooldown window")
	}
}

// TestCooldownSurvivesReveal is the regression test for the bug plan-check
// caught: the cooldown clock must not reset just because the pending file
// backing a plant was deleted at reveal time. If it did, the very next
// eligible prompt could fire again, breaking the anti-clustering guarantee.
func TestCooldownSurvivesReveal(t *testing.T) {
	isolateHome(t)
	if _, err := WritePending(testSession, "misordered-causality", ""); err != nil {
		t.Fatal(err)
	}
	if err := MarkFired(testSession); err != nil {
		t.Fatal(err)
	}
	if err := ClosePending(testSession); err != nil {
		t.Fatal(err)
	}

	fire, err := ShouldFire(testSession, time.Hour)
	if err != nil {
		t.Fatalf("ShouldFire: %v", err)
	}
	if fire {
		t.Error("closing the pending file at reveal must not reset the cooldown marker")
	}
}

func TestPruneCooldownsRemovesOnlyOldMarkers(t *testing.T) {
	isolateHome(t)
	if err := MarkFired("old-session-0123456789"); err != nil {
		t.Fatal(err)
	}
	path, err := cooldownPath("old-session-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := MarkFired("fresh-session-0123456789"); err != nil {
		t.Fatal(err)
	}

	if err := PruneCooldowns(30 * 24 * time.Hour); err != nil {
		t.Fatalf("PruneCooldowns: %v", err)
	}

	if fire, _ := ShouldFire("old-session-0123456789", time.Hour); !fire {
		t.Error("the pruned session's marker should be gone, so it should be eligible to fire again")
	}
	if fire, _ := ShouldFire("fresh-session-0123456789", time.Hour); fire {
		t.Error("the fresh marker should have survived the prune")
	}
}

func TestWritePendingDiffMutationRoundtrip(t *testing.T) {
	isolateHome(t)
	gt := DiffMutationGroundTruth{
		Category:    "off-by-one",
		File:        "internal/foo/foo.go",
		Severity:    "medium",
		Description: "loop bound shifted by one",
		DiffHash:    "abc123",
	}
	p, err := WritePendingDiffMutation(testSession, "/tmp/fake-transcript.jsonl", gt)
	if err != nil {
		t.Fatalf("WritePendingDiffMutation: %v", err)
	}
	if p.Token == "" {
		t.Fatal("WritePendingDiffMutation did not generate a token")
	}

	got, ok, err := ReadPending(testSession)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if !ok {
		t.Fatal("ReadPending: expected a pending entry")
	}
	if got.LiveKind != store.LiveKindDiffMutation {
		t.Errorf("LiveKind = %q, want %q", got.LiveKind, store.LiveKindDiffMutation)
	}
	if got.Category != gt.Category {
		t.Errorf("Category = %q, want it to reuse gt.Category (%q)", got.Category, gt.Category)
	}
	if got.InjectedFile != gt.File || got.InjectedSeverity != gt.Severity || got.InjectedDescription != gt.Description || got.DiffHash != gt.DiffHash {
		t.Errorf("ground truth didn't round-trip: got %+v, want it to match %+v", got, gt)
	}
	if got.TranscriptPath != "/tmp/fake-transcript.jsonl" {
		t.Errorf("TranscriptPath = %q, want it to round-trip", got.TranscriptPath)
	}
}

func TestWritePendingDiffMutationRejectsDoubleFireAgainstWritePending(t *testing.T) {
	isolateHome(t)
	if _, err := WritePending(testSession, "misordered-causality", ""); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	if _, err := WritePendingDiffMutation(testSession, "", DiffMutationGroundTruth{Category: "off-by-one"}); err == nil {
		t.Error("WritePendingDiffMutation should fail (O_CREATE|O_EXCL) against an already-pending narrate plant, same as two WritePending calls would")
	}
}

// TestReadPendingBackwardCompatWithoutNewFields confirms a pending file
// written before LiveKind/InjectedFile/InjectedSeverity/
// InjectedDescription/DiffHash existed still decodes correctly — every
// new field is omitempty and must come back as its zero value, not break
// decoding.
func TestReadPendingBackwardCompatWithoutNewFields(t *testing.T) {
	isolateHome(t)
	old := `{"session_id":"` + testSession + `","category":"misordered-causality","token":"abc123","planted_at":"2026-01-01T00:00:00Z","asked":false}`
	dir, err := pendingDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := pendingPath(testSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}

	got, ok, err := ReadPending(testSession)
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if !ok {
		t.Fatal("ReadPending: expected a pending entry")
	}
	if got.Category != "misordered-causality" || got.Token != "abc123" {
		t.Errorf("pre-existing fields didn't decode correctly: %+v", got)
	}
	if got.LiveKind != "" || got.InjectedFile != "" || got.InjectedSeverity != "" || got.InjectedDescription != "" || got.DiffHash != "" {
		t.Errorf("new fields should all be zero-valued for a pre-existing pending file, got %+v", got)
	}
}

func TestValidateSessionIDRejectsMalformed(t *testing.T) {
	isolateHome(t)
	for _, bad := range []string{"", "short", "../../etc/passwd", "has a space", "has/slash"} {
		if _, err := WritePending(bad, "misordered-causality", ""); err == nil {
			t.Errorf("WritePending(%q, ...) should reject a malformed session id", bad)
		}
	}
}
