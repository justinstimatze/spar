package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/justinstimatze/spar/internal/store"
)

// syntheticModelMarker mirrors internal/transcript's own unexported
// syntheticModel constant — what Claude Code writes in an assistant record
// it composed itself (API error banners, interrupt notices), never
// something the model actually wrote. Kept as a separate literal here
// rather than exporting the other package's constant, since this is the
// only place outside internal/transcript that needs it.
const syntheticModelMarker = "<synthetic>"

var (
	errNotFound  = errors.New("not found")
	errAmbiguous = errors.New("ambiguous")
)

// fixupMtimeGuard is a soft "does this look like it's still an open
// session" heuristic, not a real liveness check — spar live-fixup is a
// manual command meant to run between sessions, not during one.
const fixupMtimeGuard = 5 * time.Minute

// cmdLiveFixup patches a live-mode plant's corrected fact into a closed
// Claude Code transcript. Never runs automatically — a person invokes this
// by hand, "once in a while," against one named session at a time. See
// README's "Live mode" section for the full safety rationale.
func cmdLiveFixup(args []string) {
	fl := flag.NewFlagSet("live-fixup", flag.ExitOnError)
	session := fl.String("session", "", "session id to fix up — required to see or apply a patch")
	apply := fl.Bool("apply", false, "actually patch the transcript (default: dry run, no writes)")
	force := fl.Bool("force", false, "bypass the recently-modified soft guard")
	_ = fl.Parse(args)

	fail := func(reason string) {
		fmt.Fprintln(os.Stderr, "spar live-fixup:", reason)
		os.Exit(1)
	}

	logPath, err := store.LogPath()
	if err != nil {
		fail(err.Error())
	}
	trials, err := store.ReadAll(logPath)
	if err != nil {
		fail(err.Error())
	}

	ledger, err := readFixupLedger()
	if err != nil {
		fail(err.Error())
	}

	eligible := eligibleFixupTrials(trials, ledger)

	if *session == "" {
		printFixupSessions(eligible)
		return
	}

	var forSession []store.Trial
	for _, t := range eligible {
		if t.SessionID == *session {
			forSession = append(forSession, t)
		}
	}
	if len(forSession) == 0 {
		fmt.Println("spar live-fixup: no eligible, unpatched trials for session", *session)
		return
	}

	if !*apply {
		runFixupDryRun(forSession)
		return
	}
	runFixupApply(forSession, *force)
}

// eligibleFixupTrials narrows to injected live-mode trials with a verified
// original/corrected/transcript-path triple (see cmd_live_reveal.go's
// substring check against pending.PlantText) that the ledger doesn't
// already show as patched. Every injected live trial has a real false
// statement in the transcript regardless of catch/miss/unengaged outcome —
// catching it just means the user noticed on their own.
func eligibleFixupTrials(trials []store.Trial, ledger map[string]bool) []store.Trial {
	var out []store.Trial
	for _, t := range trials {
		if t.Mode != store.ModeLive || !t.Injected {
			continue
		}
		if t.OriginalText == "" || t.CorrectedText == "" || t.TranscriptPath == "" {
			continue
		}
		if ledger[fixupLedgerKey(t.SessionID, t.TS)] {
			continue
		}
		out = append(out, t)
	}
	return out
}

func printFixupSessions(eligible []store.Trial) {
	if len(eligible) == 0 {
		fmt.Println("spar live-fixup: no fixup-eligible trials found")
		return
	}
	counts := map[string]int{}
	var order []string
	for _, t := range eligible {
		if counts[t.SessionID] == 0 {
			order = append(order, t.SessionID)
		}
		counts[t.SessionID]++
	}
	fmt.Println("spar live-fixup: sessions with fixup-eligible trials")
	for _, s := range order {
		fmt.Printf("  %s  (%d eligible)\n", s, counts[s])
	}
	fmt.Println("\nRun `spar live-fixup --session ID` to see what would change.")
}

func runFixupDryRun(trials []store.Trial) {
	fmt.Printf("spar live-fixup: dry run, %d eligible trial(s), nothing written\n", len(trials))
	for _, t := range trials {
		n, err := scanMatches(t.TranscriptPath, t.OriginalText)
		switch {
		case err != nil:
			fmt.Printf("  [error] %s: %v\n", t.TranscriptPath, err)
		case n == 0:
			fmt.Printf("  [not found] %q -> %q\n", shortText(t.OriginalText), shortText(t.CorrectedText))
		case n > 1:
			fmt.Printf("  [ambiguous (%dx)] %q -> %q\n", n, shortText(t.OriginalText), shortText(t.CorrectedText))
		default:
			fmt.Printf("  [would patch] %q -> %q\n", shortText(t.OriginalText), shortText(t.CorrectedText))
		}
	}
}

// runFixupApply implements the guard and backup once per invocation, not
// per trial — a session with multiple eligible trials would otherwise trip
// its own mtime guard on the file it just wrote, and risk colliding backup
// filenames at second resolution. --session already narrows to one
// transcript file, so "once per invocation" and "once per file" coincide.
func runFixupApply(trials []store.Trial, force bool) {
	path := trials[0].TranscriptPath

	if err := checkNotRecentlyModified(path, force); err != nil {
		fmt.Fprintln(os.Stderr, "spar live-fixup:", err)
		os.Exit(1)
	}
	backup, err := backupTranscript(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spar live-fixup: backup failed, nothing patched:", err)
		os.Exit(1)
	}
	fmt.Println("spar live-fixup: backed up to", backup)
	fmt.Println("spar live-fixup: to undo any patch below, mv the backup back over the transcript path")

	patched, skipped := 0, 0
	for _, t := range trials {
		err := applyPatch(t.TranscriptPath, t.OriginalText, t.CorrectedText)
		switch {
		case err == nil:
			if lerr := appendFixupLedger(t.SessionID, t.TS); lerr != nil {
				fmt.Fprintln(os.Stderr, "spar live-fixup: patched but failed to record in the ledger:", lerr)
			}
			fmt.Printf("  [patched] %q -> %q\n", shortText(t.OriginalText), shortText(t.CorrectedText))
			patched++
		case errors.Is(err, errNotFound):
			fmt.Printf("  [not found, skipped] %q\n", shortText(t.OriginalText))
			skipped++
		case errors.Is(err, errAmbiguous):
			fmt.Printf("  [%v, skipped] %q\n", err, shortText(t.OriginalText))
			skipped++
		default:
			fmt.Printf("  [error, skipped] %q: %v\n", shortText(t.OriginalText), err)
			skipped++
		}
	}
	fmt.Printf("spar live-fixup: %d patched, %d skipped\n", patched, skipped)
}

func shortText(s string) string {
	const max = 60
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func checkNotRecentlyModified(path string, force bool) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	age := time.Since(fi.ModTime())
	if !force && age < fixupMtimeGuard {
		return fmt.Errorf("transcript was modified %s ago — make sure the session is closed before running this, or pass --force", age.Round(time.Second))
	}
	return nil
}

// backupTranscript copies path to a sibling file before any patch is
// written. The suffix is deliberately not .jsonl so nothing that
// enumerates *.jsonl in this directory picks the backup up as a phantom
// session.
func backupTranscript(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	bpath := fmt.Sprintf("%s.spar-fixup-backup-%d", path, time.Now().Unix())
	if err := os.WriteFile(bpath, data, 0600); err != nil {
		return "", err
	}
	return bpath, nil
}

// scanMatches counts exact occurrences of original across every genuine
// assistant text block in path — never inside a tool_use block, a
// tool_result, a user message, or a harness-composed record (API error
// banners, the synthetic-model marker) — matching internal/transcript's
// own exclusions. Used both for dry-run reporting and, inside applyPatch,
// to refuse anything but exactly one match.
func scanMatches(path, original string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		total += matchesInLine(line, original)
	}
	return total, nil
}

// matchesInLine returns how many times original appears across every
// "text" content block of a genuine assistant reply on this one line, or 0
// for anything else (a different role, a harness-composed record, a line
// that doesn't even parse as JSON — never a hard error, just not a
// candidate).
func matchesInLine(line []byte, original string) int {
	var top map[string]json.RawMessage
	if json.Unmarshal(line, &top) != nil {
		return 0
	}
	var typ string
	json.Unmarshal(top["type"], &typ)
	if typ != "assistant" {
		return 0
	}
	if raw, ok := top["isApiErrorMessage"]; ok {
		var apiErr bool
		json.Unmarshal(raw, &apiErr)
		if apiErr {
			return 0
		}
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(top["message"], &msg) != nil {
		return 0
	}
	var role, model string
	json.Unmarshal(msg["role"], &role)
	json.Unmarshal(msg["model"], &model)
	if role != "assistant" || model == syntheticModelMarker {
		return 0
	}
	var content []json.RawMessage
	if json.Unmarshal(msg["content"], &content) != nil {
		return 0
	}
	count := 0
	for _, raw := range content {
		var block map[string]json.RawMessage
		if json.Unmarshal(raw, &block) != nil {
			continue
		}
		var btype string
		json.Unmarshal(block["type"], &btype)
		if btype != "text" {
			continue
		}
		var text string
		if json.Unmarshal(block["text"], &text) != nil {
			continue
		}
		count += strings.Count(text, original)
	}
	return count
}

// applyPatch requires exactly one match across the whole file (errNotFound
// or errAmbiguous otherwise — never guesses), then rewrites only that one
// line: the matched text string is replaced, and re-marshaled back outward
// through its content block, the content array, the message, and the
// top-level record — every sibling field, known or not, passes through
// untouched as json.RawMessage rather than being decoded into a partial Go
// struct that could silently drop it. Writes via a temp file in the same
// directory (same filesystem, for an atomic rename) with the original
// file's mode preserved, then an atomic rename over the original.
func applyPatch(path, original, corrected string) error {
	n, err := scanMatches(path, original)
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	if n > 1 {
		return fmt.Errorf("%w (%dx)", errAmbiguous, n)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := bytes.Split(data, []byte("\n"))
	patched := false
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 || matchesInLine(line, original) != 1 {
			continue
		}
		newLine, err := patchLineText(line, original, corrected)
		if err != nil {
			return err
		}
		lines[i] = newLine
		patched = true
		break
	}
	if !patched {
		return errNotFound // defensive: scanMatches already found exactly one
	}

	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return writeAtomic(path, bytes.Join(lines, []byte("\n")), fi.Mode())
}

// patchLineText replaces original with corrected inside the one "text"
// content block of line that contains it, re-marshaling outward at every
// level via map[string]json.RawMessage so every field this design doesn't
// need to touch — a message's uuid, model metadata, usage/cache token
// counts, whatever a future Claude Code version adds — passes through
// byte-identical rather than being silently dropped by a partial struct.
// Re-marshaling a map alphabetizes its keys, so the patched line's byte
// layout won't match the original's beyond content — harmless to any JSON
// reader, just worth knowing if you ever diff the file by eye.
func patchLineText(line []byte, original, corrected string) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(line, &top); err != nil {
		return nil, fmt.Errorf("decode line: %w", err)
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(top["message"], &msg); err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}
	var content []json.RawMessage
	if err := json.Unmarshal(msg["content"], &content); err != nil {
		return nil, fmt.Errorf("decode content: %w", err)
	}

	patched := false
	for i, raw := range content {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}
		var btype string
		json.Unmarshal(block["type"], &btype)
		if btype != "text" {
			continue
		}
		var text string
		if err := json.Unmarshal(block["text"], &text); err != nil {
			continue
		}
		if !strings.Contains(text, original) {
			continue
		}
		newTextRaw, err := json.Marshal(strings.Replace(text, original, corrected, 1))
		if err != nil {
			return nil, err
		}
		block["text"] = newTextRaw
		newBlockRaw, err := json.Marshal(block)
		if err != nil {
			return nil, err
		}
		content[i] = newBlockRaw
		patched = true
		break
	}
	if !patched {
		return nil, errNotFound
	}

	newContentRaw, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	msg["content"] = newContentRaw
	newMsgRaw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	top["message"] = newMsgRaw
	newLine, err := json.Marshal(top)
	if err != nil {
		return nil, err
	}
	if !json.Valid(newLine) {
		return nil, errors.New("patched line failed its own validity check")
	}
	return newLine, nil
}

// writeAtomic writes data to a temp file in path's own directory (same
// filesystem, so the final rename is atomic), chmods it to mode, then
// renames it over path.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".spar-live-fixup-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// fixupRecord is one line of the fixup ledger — spar's own record of which
// trials have already been patched, kept separate from log.jsonl so
// store.Append stays a pure append with no "rewrite an existing record"
// capability added to it.
type fixupRecord struct {
	SessionID string    `json:"session_id"`
	TrialTS   time.Time `json:"trial_ts"`
	PatchedAt time.Time `json:"patched_at"`
}

func fixupLedgerPath() (string, error) {
	dir, err := store.SparDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "live-fixup-log.jsonl"), nil
}

func fixupLedgerKey(sessionID string, ts time.Time) string {
	return sessionID + "|" + ts.Format(time.RFC3339Nano)
}

func readFixupLedger() (map[string]bool, error) {
	path, err := fixupLedgerPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	defer f.Close()

	done := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r fixupRecord
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		done[fixupLedgerKey(r.SessionID, r.TrialTS)] = true
	}
	return done, sc.Err()
}

func appendFixupLedger(sessionID string, trialTS time.Time) error {
	path, err := fixupLedgerPath()
	if err != nil {
		return err
	}
	rec := fixupRecord{SessionID: sessionID, TrialTS: trialTS, PatchedAt: time.Now()}
	data, err := json.Marshal(rec)
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
