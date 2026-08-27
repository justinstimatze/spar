// Package transcript reads a Claude Code session's JSONL transcript far
// enough to recover the most recent assistant turn. Trimmed from cope's
// (github.com/justinstimatze/cope) internal/transcript LastTurn for spar's
// one real use — live mode's soft corroboration check at reveal time —
// dropping cope's loop/lane distinction, Key-based rescore tracking, and
// forward-scanning AllTurns/backfill machinery, none of which spar needs.
package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type message struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type record struct {
	Type     string  `json:"type"`
	APIError bool    `json:"isApiErrorMessage"`
	Message  message `json:"message"`
}

// syntheticModel is what Claude Code writes in an assistant record it
// composed itself rather than received from the model: API error banners,
// interrupt notices, and anything else the harness needs in the reply
// position.
const syntheticModel = "<synthetic>"

// assistantText pulls the assistant's prose out of one JSONL line, or
// returns "" for anything else (tool calls, tool results, harness-composed
// records).
func assistantText(line []byte) string {
	if len(bytes.TrimSpace(line)) == 0 {
		return ""
	}
	var r record
	if err := json.Unmarshal(line, &r); err != nil {
		return "" // a malformed line should not break corroboration
	}
	if r.Type != "assistant" || r.Message.Role != "assistant" {
		return ""
	}
	if r.APIError || r.Message.Model == syntheticModel {
		return ""
	}
	var blocks []block
	if err := json.Unmarshal(r.Message.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// isUserPrompt reports whether a line is a real prompt from the user, as
// opposed to a tool-result-carrying user record (which shares the same
// "user" record type but isn't a turn boundary).
func isUserPrompt(line []byte) bool {
	if len(bytes.TrimSpace(line)) == 0 {
		return false
	}
	var r record
	if err := json.Unmarshal(line, &r); err != nil {
		return false
	}
	if r.Type != "user" || r.Message.Role != "user" {
		return false
	}
	var s string
	if err := json.Unmarshal(r.Message.Content, &s); err == nil {
		return strings.TrimSpace(s) != ""
	}
	var blocks []block
	if err := json.Unmarshal(r.Message.Content, &blocks); err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return true
		}
	}
	return false
}

// tailChunk is how much the backward reader takes per step. One megabyte
// holds the last turn in all but pathological transcripts.
var tailChunk = 1 << 20

// maxCarry bounds the partial line held over when a single JSONL record
// spans more than one chunk.
const maxCarry = 64 << 20

// maxTurnBlocks bounds how far back turn assembly walks when it finds no
// boundary, so an unreadable transcript can't pull the whole session in as
// one turn.
const maxTurnBlocks = 512

const turnSep = "\n\n"

// eachLineBackward calls fn with each line of f, last first, and stops when
// fn returns false or the start of the file is reached. Reading backward
// keeps this fast on a large transcript — spar's live-hook has a ~10s hook
// timeout budget, and this only ever needs the last turn.
func eachLineBackward(f *os.File, size int64, fn func(line []byte) bool) error {
	var carry []byte
	for off := size; off > 0; {
		n := int64(tailChunk)
		if n > off {
			n = off
		}
		off -= n

		buf := make([]byte, n, n+int64(len(carry)))
		if _, err := f.ReadAt(buf, off); err != nil {
			return err
		}
		buf = append(buf, carry...)

		lines := bytes.Split(buf, []byte{'\n'})
		first := 1
		if off == 0 {
			first = 0
		}
		for i := len(lines) - 1; i >= first; i-- {
			if !fn(lines[i]) {
				return nil
			}
		}
		if off == 0 {
			return nil
		}
		carry = lines[0]
		if len(carry) > maxCarry {
			return fmt.Errorf("a single line exceeds %d bytes", maxCarry)
		}
	}
	return nil
}

// Turn is one assistant reply: every prose block written since the user's
// last real prompt, in the order it was written.
type Turn struct {
	Text string
}

// LastTurn returns the most recent turn that has a reply in it, or an empty
// Turn if the transcript has no assistant reply yet. Used only for live
// mode's soft corroboration — spar never scores or validates against this,
// it's an audit signal alongside the model's own self-reported disclosure.
func LastTurn(path string) (Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return Turn{}, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return Turn{}, err
	}

	var parts []string // newest first until reversed below
	err = eachLineBackward(f, fi.Size(), func(line []byte) bool {
		if isUserPrompt(line) {
			return len(parts) == 0 // a prompt still waiting for its reply
		}
		if t := assistantText(line); t != "" {
			parts = append(parts, t)
			return len(parts) < maxTurnBlocks
		}
		return true
	})
	if err != nil {
		return Turn{}, fmt.Errorf("%s: %w", path, err)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return Turn{Text: strings.Join(parts, turnSep)}, nil
}
