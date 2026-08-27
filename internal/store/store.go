// Package store owns spar's on-disk trial log: a single flat JSONL file
// at ~/.claude/spar/log.jsonl, one record per `spar review` trial.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion tags every appended Trial so a future reader can tell
// old records from new if the shape ever changes.
const SchemaVersion = 1

// Outcome values. Injected × Flagged is a plain 2x2 for review-mode trials.
// OutcomeUnrevealed and OutcomeNotPlanted are live-mode-only: a live trial
// has no "flagged" signal to compute a 2x2 outcome from, so it needs its own
// terminal states instead of being forced into the four above.
const (
	OutcomeCatch         = "catch"
	OutcomeMiss          = "miss"
	OutcomeFalsePositive = "false_positive"
	OutcomeTrueNegative  = "true_negative"

	// OutcomeUnrevealed is a live-mode plant whose pending file expired
	// past its TTL before a reveal exchange happened — the deception
	// occurred but was never disclosed or caught. Logged rather than
	// silently dropped, since an unrevealed plant is a real event.
	OutcomeUnrevealed = "unrevealed"
	// OutcomeNotPlanted is a live-mode turn where the model didn't
	// comply with the planting instruction — a non-alarming, expected
	// outcome, not an error.
	OutcomeNotPlanted = "not_planted"
	// OutcomeUnengaged is silent-mode live reveal's honest "no signal"
	// result: nothing in the user's behavior since the plant bore on the
	// planted claim either way, so it's neither a catch nor a miss.
	// Distinct from OutcomeMiss on purpose — collapsing genuine silence
	// into a miss would fabricate a signal that was never actually
	// observed.
	OutcomeUnengaged = "unengaged"
)

// Mode values distinguish which of spar's two injection surfaces produced a
// trial. The zero value ("") means review mode, for backward compatibility
// with every trial logged before live mode existed.
const (
	ModeReview = ""
	ModeLive   = "live"
)

// LiveKind values distinguish which live-mode trigger flavor produced a
// Mode==ModeLive trial. The zero value ("") means narration — a
// fabricated verbal claim the model invented, category drawn from
// internal/livetaxonomy — for backward compatibility with every live
// trial logged before notify mode existed. LiveKindDiffMutation means a
// real internal/inject mutation, category drawn from that package's own,
// disjoint taxonomy. Needed because under Mode==ModeLive, InjectedCategory
// can now come from either pool and nothing else disambiguates which —
// the two pools happen to be disjoint today by inspection only, not by
// any enforced contract.
const (
	LiveKindNarration    = ""
	LiveKindDiffMutation = "diff-mutation"
)

// Trial is one spar trial, from either `spar review` (Mode == ModeReview)
// or live mode (Mode == ModeLive).
type Trial struct {
	SchemaVersion int       `json:"schema_version"`
	TS            time.Time `json:"ts"`
	Project       string    `json:"project"`
	DiffHash      string    `json:"diff_hash,omitempty"`

	Mode string `json:"mode,omitempty"`

	// LiveKind distinguishes which live-mode trigger flavor produced a
	// Mode==ModeLive trial (see the LiveKindX constants above) — empty
	// for every review-mode trial and every live trial logged before
	// notify mode existed.
	LiveKind string `json:"live_kind,omitempty"`

	// Injected/InjectedCategory/InjectedDescription are shared by both
	// modes: "was something injected, and what/where." A live trial
	// reuses them rather than duplicating storage — InjectedCategory
	// holds a category name from whichever taxonomy LiveKind selects
	// (internal/livetaxonomy for narration, internal/inject for
	// diff-mutation), InjectedDescription holds either the model's
	// self-reported description (narration) or spar's own exact ground
	// truth (diff-mutation). InjectedFile and InjectedSeverity were
	// review-mode-only until notify mode started populating them too —
	// still empty for narration-flavor live trials, which have no real
	// file or severity axis.
	Injected            bool   `json:"injected"`
	InjectedFile        string `json:"injected_file,omitempty"`
	InjectedCategory    string `json:"injected_category,omitempty"`
	InjectedSeverity    string `json:"injected_severity,omitempty"`
	InjectedDescription string `json:"injected_description,omitempty"`

	UserFlagged  bool   `json:"user_flagged"`
	UserFlagText string `json:"user_flag_text,omitempty"`

	Outcome string `json:"outcome"`

	// InjectAttempted/InjectFallbackReason distinguish "coin flip said
	// clean" from "coin flip said inject but the pipeline fell back" —
	// without them both collapse to Injected==false and a real failure
	// (bad API key, oversized file, validation reject) silently reads
	// as a clean trial in the stats. Review-mode-only.
	InjectAttempted      bool   `json:"inject_attempted,omitempty"`
	InjectFallbackReason string `json:"inject_fallback_reason,omitempty"`

	// SessionID/CaughtDegree/LiveExchangeVerified are live-mode-only.
	// CaughtDegree is the three-way reveal-time result ("yes"|"no"|
	// "partial"); UserFlagged is derived from it (CaughtDegree != "no")
	// so stats code written against the review-mode boolean still works.
	// LiveExchangeVerified is a soft, non-enforced audit signal — whether
	// live-hook could corroborate the plant against the real transcript
	// text at reveal-trigger time, not a validity gate.
	SessionID            string `json:"session_id,omitempty"`
	CaughtDegree         string `json:"caught_degree,omitempty"`
	LiveExchangeVerified bool   `json:"live_exchange_verified,omitempty"`

	// OriginalText/CorrectedText/TranscriptPath are live-mode-only and set
	// only when cmd_live_reveal.go verified --original-text against the
	// plant's own corroborated text at reveal time (see
	// livestate.Pending.PlantText) — never trusted on the model's word
	// alone. spar live-fixup uses these three, together, to find and patch
	// the one exact spot in TranscriptPath that OriginalText came from.
	// Empty unless verified; a trial missing any of the three simply isn't
	// fixup-eligible.
	OriginalText   string `json:"original_text,omitempty"`
	CorrectedText  string `json:"corrected_text,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
}

// ComputeOutcome maps the injected/flagged 2x2 to an outcome label.
func ComputeOutcome(injected, flagged bool) string {
	switch {
	case injected && flagged:
		return OutcomeCatch
	case injected && !flagged:
		return OutcomeMiss
	case !injected && flagged:
		return OutcomeFalsePositive
	default:
		return OutcomeTrueNegative
	}
}

// SparDir returns ~/.claude/spar, spar's local-state directory.
func SparDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "spar"), nil
}

// LogPath returns ~/.claude/spar/log.jsonl.
func LogPath() (string, error) {
	dir, err := SparDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "log.jsonl"), nil
}

// Append writes one trial as a JSONL line, creating the parent directory
// and file as needed. No rotation or sharding — a single flat file is
// enough at personal dogfood volume.
func Append(path string, t Trial) error {
	t.SchemaVersion = SchemaVersion
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// ReadAll reads every trial in the log, skipping malformed lines rather
// than failing the whole read.
func ReadAll(path string) ([]Trial, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var out []Trial
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var t Trial
		if err := json.Unmarshal(line, &t); err != nil {
			continue
		}
		out = append(out, t)
	}
	return out, sc.Err()
}

// Filter narrows trials by a "since" duration string (see ParseSince) and
// an optional project name. Either filter is skipped when empty.
func Filter(trials []Trial, since, project string) ([]Trial, error) {
	var cutoff time.Time
	if since != "" {
		d, err := ParseSince(since)
		if err != nil {
			return nil, err
		}
		cutoff = time.Now().Add(-d)
	}
	var out []Trial
	for _, t := range trials {
		if since != "" && t.TS.Before(cutoff) {
			continue
		}
		if project != "" && t.Project != project {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// ParseSince accepts a trailing "d" (days) or "w" (weeks) suffix in
// addition to whatever time.ParseDuration already understands (h, m, s).
func ParseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	last := s[len(s)-1]
	if last == 'd' || last == 'w' {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, err
		}
		unit := 24 * time.Hour
		if last == 'w' {
			unit = 7 * 24 * time.Hour
		}
		return time.Duration(n) * unit, nil
	}
	return time.ParseDuration(s)
}

// TrendBucket summarizes catch/miss outcomes for one 7-day window — the
// data spar needs to answer whether catch rate is actually moving, which
// it previously couldn't (see docs/DESIGN.md's open question on Frey's
// obsolescence litmus test).
type TrendBucket struct {
	Start   time.Time
	Catches int
	Misses  int
}

// Rate returns the bucket's catch rate, or 0 for an empty bucket.
func (b TrendBucket) Rate() float64 {
	total := b.Catches + b.Misses
	if total == 0 {
		return 0
	}
	return float64(b.Catches) / float64(total)
}

// TrendByWeek buckets injected trials (catch or miss only — an unrevealed
// or not-planted live trial has no catch/miss signal) into consecutive
// 7-day windows anchored to the earliest trial's timestamp, oldest first.
// Plain 7-day windows rather than calendar weeks: no ISO-week or timezone
// arithmetic needed, and bucket width is all that matters for a trend.
func TrendByWeek(trials []Trial) []TrendBucket {
	var injected []Trial
	for _, t := range trials {
		if t.Outcome == OutcomeCatch || t.Outcome == OutcomeMiss {
			injected = append(injected, t)
		}
	}
	if len(injected) == 0 {
		return nil
	}
	sort.Slice(injected, func(i, j int) bool { return injected[i].TS.Before(injected[j].TS) })

	const week = 7 * 24 * time.Hour
	start := injected[0].TS
	byIndex := make(map[int]*TrendBucket)
	var order []int
	for _, t := range injected {
		idx := int(t.TS.Sub(start) / week)
		b, ok := byIndex[idx]
		if !ok {
			b = &TrendBucket{Start: start.Add(time.Duration(idx) * week)}
			byIndex[idx] = b
			order = append(order, idx)
		}
		if t.Outcome == OutcomeCatch {
			b.Catches++
		} else {
			b.Misses++
		}
	}
	sort.Ints(order)
	out := make([]TrendBucket, len(order))
	for i, idx := range order {
		out[i] = *byIndex[idx]
	}
	return out
}

// CategoryStat summarizes catch/miss outcomes for one injected category.
type CategoryStat struct {
	Category string
	Catches  int
	Misses   int
}

// Rate returns the category's catch rate, or 0 for an empty bucket.
func (c CategoryStat) Rate() float64 {
	total := c.Catches + c.Misses
	if total == 0 {
		return 0
	}
	return float64(c.Catches) / float64(total)
}

// CategoryBreakdown groups catch/miss outcomes by InjectedCategory, sorted
// worst catch rate first — answers the other half of the trend question:
// not just whether catch rate is moving, but whether the same category
// keeps getting missed.
func CategoryBreakdown(trials []Trial) []CategoryStat {
	byCategory := make(map[string]*CategoryStat)
	var order []string
	for _, t := range trials {
		if t.Outcome != OutcomeCatch && t.Outcome != OutcomeMiss {
			continue
		}
		cat := t.InjectedCategory
		if cat == "" {
			cat = "(uncategorized)"
		}
		s, ok := byCategory[cat]
		if !ok {
			s = &CategoryStat{Category: cat}
			byCategory[cat] = s
			order = append(order, cat)
		}
		if t.Outcome == OutcomeCatch {
			s.Catches++
		} else {
			s.Misses++
		}
	}
	out := make([]CategoryStat, 0, len(order))
	for _, cat := range order {
		out = append(out, *byCategory[cat])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rate() < out[j].Rate() })
	return out
}

// ResolveProjectName names a repo for the log: the short name from its
// origin remote if it has one, else the repo directory's own basename.
func ResolveProjectName(repoRoot string) string {
	out, err := exec.Command("git", "-C", repoRoot, "remote", "get-url", "origin").Output()
	if err == nil {
		url := strings.TrimSpace(string(out))
		url = strings.TrimSuffix(url, ".git")
		if i := strings.LastIndexAny(url, "/:"); i >= 0 && i+1 < len(url) {
			return url[i+1:]
		}
	}
	return filepath.Base(repoRoot)
}
