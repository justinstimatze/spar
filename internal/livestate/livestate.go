// Package livestate holds live mode's ephemeral per-session coordination
// state: a store, distinct from internal/store's durable trial history.
//
// Two independent pieces of state live here, and they must stay independent
// — this was a real bug caught during planning. If "when did this session
// last fire" were tracked only inside the pending file, deleting that file
// at reveal time would reset the cooldown clock to zero immediately, and the
// very next eligible prompt could fire again, breaking the anti-clustering
// goal the cooldown exists for. So:
//
//   - The pending file (~/.claude/spar/live-pending/<session>.json) exists
//     only between a plant and its reveal or TTL expiry.
//   - The cooldown marker (~/.claude/spar/live-cooldown/<session>) is
//     restamped every time a plant fires, is read on every hook invocation
//     regardless of pending state, and is never touched by a reveal.
package livestate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/justinstimatze/spar/internal/store"
)

// Pending is one live-mode plant awaiting reveal.
type Pending struct {
	SessionID      string    `json:"session_id"`
	Category       string    `json:"category"`
	Token          string    `json:"token"`
	PlantedAt      time.Time `json:"planted_at"`
	TranscriptPath string    `json:"transcript_path,omitempty"`

	// Corroborated is set once, by SetCorroborated, at the moment
	// live-hook emits reveal instructions — the only point it still has
	// the transcript available to check the plant turn actually
	// happened. A soft audit signal, not a validity gate.
	Corroborated bool `json:"corroborated"`

	// PlantText is the plant turn's real text, captured by the same
	// SetCorroborated call as Corroborated — spar's own record of what
	// was actually written, not the model's later recollection of it.
	// Reveal instructions quote a capped snippet of it so the model has
	// its own real words in front of it, and cmd_live_reveal.go verifies
	// --original-text against it before trusting it for spar live-fixup.
	PlantText string `json:"plant_text,omitempty"`

	// Asked is set once, by MarkAsked, the first time live-hook emits the
	// "did anything seem off" question. cmdLiveHook checks it to decide
	// which half of the reveal to emit: the ask (Asked still false) or
	// the disclose-and-log instructions (Asked already true). This is
	// what guarantees the model can never compose the disclose Bash
	// call — the one whose --description argument names what was
	// planted — on the same turn as the question, before a real user
	// answer exists to disclose against.
	Asked bool `json:"asked"`

	// LiveKind, InjectedFile, InjectedSeverity, InjectedDescription, and
	// DiffHash are additive and omitempty — set only by
	// WritePendingDiffMutation (spar live-hook-commit's notify mode),
	// never by WritePending (narrate mode, or the chat-triggered hook).
	// A pending file written before these fields existed still decodes
	// correctly: they simply come back as their zero values. LiveKind
	// empty means narration, matching store.Mode's existing "" == review
	// idiom; store.LiveKindDiffMutation is the only other value.
	//
	// Unlike Category (reused as-is for both flavors — it already holds
	// whichever taxonomy's name is relevant), these five carry ground
	// truth spar itself already knows exactly at plant time for a real
	// diff mutation, so cmd_live_reveal.go doesn't need to trust the
	// model's memory of it the way narrate mode's --description flag
	// does.
	LiveKind            string `json:"live_kind,omitempty"`
	InjectedFile        string `json:"injected_file,omitempty"`
	InjectedSeverity    string `json:"injected_severity,omitempty"`
	InjectedDescription string `json:"injected_description,omitempty"`
	DiffHash            string `json:"diff_hash,omitempty"`
}

// DiffMutationGroundTruth is the real, spar-computed injection result
// WritePendingDiffMutation stores on a Pending — a struct rather than
// more positional string args on WritePending, since five same-typed
// strings stacked on an existing 3-arg call is a real order-of-args risk.
// Deliberately its own type, not internal/inject.Result reused directly:
// this package's whole job is local JSON file I/O, and pulling in
// inject's full API surface (including its network-calling Config type)
// for five string fields would be real, unwanted coupling growth.
type DiffMutationGroundTruth struct {
	// Category is the internal/inject taxonomy name (e.g. "off-by-one")
	// — a different, disjoint pool from internal/livetaxonomy's narration
	// categories. Reused as-is for Pending.Category; see the field
	// comment above.
	Category string
	// File is the path of the one mutated candidate.
	File string
	// Severity is the model's own rough low/medium/high estimate.
	Severity string
	// Description is spar's own exact record of what changed and why —
	// not the model's self-report, unlike narrate mode's equivalent.
	Description string
	// DiffHash identifies the real (unmutated) diff this trial ran
	// against, matching store.Trial.DiffHash's existing convention.
	DiffHash string
}

// sessionIDRe bounds what spar will ever use as a filename component,
// matching the shape Claude Code actually sends (a UUID) with room to
// spare — defense-in-depth so a malformed or hostile session_id can't be
// read as a path.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func validateSessionID(sessionID string) error {
	if !sessionIDRe.MatchString(sessionID) {
		return fmt.Errorf("livestate: invalid session id %q", sessionID)
	}
	return nil
}

func pendingDir() (string, error) {
	dir, err := store.SparDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "live-pending"), nil
}

func cooldownDir() (string, error) {
	dir, err := store.SparDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "live-cooldown"), nil
}

func pendingPath(sessionID string) (string, error) {
	dir, err := pendingDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionID+".json"), nil
}

func cooldownPath(sessionID string) (string, error) {
	dir, err := cooldownDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionID), nil
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// WritePending creates a new pending plant for sessionID and returns it
// (including the generated token the reveal instructions must echo back).
// Uses O_CREATE|O_EXCL so a double-fire race — two live-hook invocations
// somehow overlapping for the same session — can't silently overwrite an
// in-flight plant's token. transcriptPath is stashed for spar live-fixup,
// which needs to know which transcript file a trial's plant lives in
// without re-deriving Claude Code's own project-slug path convention.
func WritePending(sessionID, category, transcriptPath string) (Pending, error) {
	if err := validateSessionID(sessionID); err != nil {
		return Pending{}, err
	}
	token, err := newToken()
	if err != nil {
		return Pending{}, err
	}
	p := Pending{SessionID: sessionID, Category: category, Token: token, PlantedAt: time.Now(), TranscriptPath: transcriptPath}
	return writePending(p)
}

// WritePendingDiffMutation is WritePending's counterpart for spar
// live-hook-commit's notify mode: category is reused via the same
// Pending.Category field narrate mode already uses (it already holds
// whichever taxonomy's name applies), and gt carries the real,
// spar-computed ground truth for a genuine inject.Try mutation, stored
// so cmd_live_reveal.go can auto-fill from it later instead of trusting
// the model to recall it. Same O_CREATE|O_EXCL atomicity as WritePending
// — a losing race fails closed, no plant, exactly like WritePending's
// existing callers already handle.
func WritePendingDiffMutation(sessionID, transcriptPath string, gt DiffMutationGroundTruth) (Pending, error) {
	if err := validateSessionID(sessionID); err != nil {
		return Pending{}, err
	}
	token, err := newToken()
	if err != nil {
		return Pending{}, err
	}
	p := Pending{
		SessionID:           sessionID,
		Category:            gt.Category,
		Token:               token,
		PlantedAt:           time.Now(),
		TranscriptPath:      transcriptPath,
		LiveKind:            store.LiveKindDiffMutation,
		InjectedFile:        gt.File,
		InjectedSeverity:    gt.Severity,
		InjectedDescription: gt.Description,
		DiffHash:            gt.DiffHash,
	}
	return writePending(p)
}

// writePending is the shared O_CREATE|O_EXCL atomic write both
// WritePending and WritePendingDiffMutation use — a double-fire race
// (two hook invocations somehow overlapping for the same session) can't
// silently overwrite an in-flight plant's token, regardless of which
// constructor lost the race.
func writePending(p Pending) (Pending, error) {
	dir, err := pendingDir()
	if err != nil {
		return Pending{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Pending{}, err
	}
	path, err := pendingPath(p.SessionID)
	if err != nil {
		return Pending{}, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return Pending{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Pending{}, err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return Pending{}, err
	}
	return p, nil
}

// ReadPending returns the pending plant for sessionID, or ok=false if none
// exists. It does not check TTL expiry — see Expired.
func ReadPending(sessionID string) (Pending, bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return Pending{}, false, err
	}
	path, err := pendingPath(sessionID)
	if err != nil {
		return Pending{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Pending{}, false, nil
		}
		return Pending{}, false, err
	}
	var p Pending
	if err := json.Unmarshal(data, &p); err != nil {
		return Pending{}, false, err
	}
	return p, true, nil
}

// SetCorroborated updates the pending plant's soft-corroboration flag and
// its real captured text in one write. Called once by live-hook, at the
// moment it emits reveal instructions — the only point it still has the
// transcript available to check the plant turn actually happened. text is
// the corroborated turn's own content (empty means uncorroborated); Not
// O_EXCL: this updates an already-created pending file rather than
// claiming a new one.
func SetCorroborated(sessionID, text string) error {
	p, ok, err := ReadPending(sessionID)
	if err != nil || !ok {
		return err
	}
	p.Corroborated = text != ""
	p.PlantText = text
	path, err := pendingPath(sessionID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// MarkAsked records that live-hook has emitted the "did anything seem off"
// question for sessionID's pending plant. Not O_EXCL: this updates an
// already-created pending file rather than claiming a new one.
func MarkAsked(sessionID string) error {
	p, ok, err := ReadPending(sessionID)
	if err != nil || !ok {
		return err
	}
	p.Asked = true
	path, err := pendingPath(sessionID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Expired reports whether a pending plant is older than ttl.
func Expired(p Pending, ttl time.Duration) bool {
	return time.Since(p.PlantedAt) > ttl
}

// ClosePending deletes the pending file for sessionID — called once a
// reveal succeeds, or by SweepExpired for an expired one. Deleting the
// pending file never touches the separate cooldown marker.
func ClosePending(sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	path, err := pendingPath(sessionID)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// SweepExpired scans every pending plant, closes out (deletes) any older
// than ttl, and returns them so the caller can log an OutcomeUnrevealed
// trial for each — an unrevealed plant is a deception that already
// happened and must not vanish untracked.
func SweepExpired(ttl time.Duration) ([]Pending, error) {
	dir, err := pendingDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var expired []Pending
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // a single unreadable entry shouldn't block the sweep
		}
		var p Pending
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		if !Expired(p, ttl) {
			continue
		}
		if err := ClosePending(p.SessionID); err != nil {
			continue
		}
		expired = append(expired, p)
	}
	return expired, nil
}

// ShouldFire reports whether enough time has passed since sessionID's
// cooldown marker was last stamped (or true if it was never stamped). It
// does not restamp — call MarkFired separately once a plant actually
// happens, so a hold decision doesn't itself reset the clock.
func ShouldFire(sessionID string, cooldown time.Duration) (bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	path, err := cooldownPath(sessionID)
	if err != nil {
		return false, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	return time.Since(fi.ModTime()) >= cooldown, nil
}

// MarkFired restamps sessionID's cooldown marker to now.
func MarkFired(sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	dir, err := cooldownDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path, err := cooldownPath(sessionID)
	if err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0600)
}

// forcePath returns the one global force-fire marker, not scoped to a
// session — spar live-induce runs from a plain terminal, outside any hook
// invocation, so it has no session_id to key off. Whichever session's
// live-hook next runs consumes it.
func forcePath() (string, error) {
	dir, err := store.SparDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "live-force"), nil
}

// WriteForce arms the one-shot force marker consumed by the next live-hook
// invocation, bypassing its cooldown check. It does not bypass the
// pending-already-open branch — a session with an unrevealed plant still
// gets reveal instructions, never a second plant.
func WriteForce() error {
	path, err := forcePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0600)
}

// ConsumeForce reports whether the force marker is armed and clears it —
// one-shot, so only the single next live-hook invocation is affected.
func ConsumeForce() (bool, error) {
	path, err := forcePath()
	if err != nil {
		return false, err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// statsNudgePath returns the one global stats-nudge marker, not scoped to
// a session — "once a day" means once a day across every project you work
// in, not once per session, so this deliberately mirrors forcePath rather
// than the per-session cooldown markers.
func statsNudgePath() (string, error) {
	dir, err := store.SparDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "live-stats-nudge"), nil
}

// ShouldNudgeStats reports whether enough time has passed since the stats
// nudge last fired (or true if it never has). It does not restamp — call
// MarkStatsNudged separately once the nudge actually fires, so a turn that
// skips it (e.g. a reveal is also pending) doesn't reset the clock.
func ShouldNudgeStats(interval time.Duration) (bool, error) {
	path, err := statsNudgePath()
	if err != nil {
		return false, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	return time.Since(fi.ModTime()) >= interval, nil
}

// MarkStatsNudged restamps the stats-nudge marker to now.
func MarkStatsNudged() error {
	path, err := statsNudgePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0600)
}

// PruneCooldowns removes cooldown markers older than maxAge, so
// live-cooldown/ doesn't grow forever across many past sessions.
func PruneCooldowns(maxAge time.Duration) error {
	dir, err := cooldownDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(fi.ModTime()) > maxAge {
			os.Remove(path)
		}
	}
	return nil
}
