package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func userLine(text string) string {
	return `{"type":"user","message":{"role":"user","content":` + jsonString(text) + `}}`
}

func assistantLine(text string) string {
	return `{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":` + jsonString(text) + `}]}}`
}

func toolResultUserLine() string {
	return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
}

func jsonString(s string) string {
	// Minimal JSON string escaping sufficient for test fixtures (no
	// quotes/backslashes/newlines in the sample text used below).
	return `"` + s + `"`
}

func TestLastTurnReturnsOnlyTheMostRecentReply(t *testing.T) {
	path := writeTranscript(t,
		userLine("first prompt"),
		assistantLine("old reply, should not appear"),
		userLine("second prompt"),
		assistantLine("newest reply part one"),
		assistantLine("newest reply part two"),
	)

	turn, err := LastTurn(path)
	if err != nil {
		t.Fatalf("LastTurn: %v", err)
	}
	if strings.Contains(turn.Text, "old reply") {
		t.Errorf("LastTurn leaked an earlier turn: %q", turn.Text)
	}
	if !strings.Contains(turn.Text, "newest reply part one") || !strings.Contains(turn.Text, "newest reply part two") {
		t.Errorf("LastTurn missing expected blocks: %q", turn.Text)
	}
}

func TestLastTurnSkipsToolResultUserRecords(t *testing.T) {
	path := writeTranscript(t,
		userLine("real prompt"),
		assistantLine("reply before a tool call"),
		toolResultUserLine(),
		assistantLine("reply after the tool result"),
	)

	turn, err := LastTurn(path)
	if err != nil {
		t.Fatalf("LastTurn: %v", err)
	}
	if !strings.Contains(turn.Text, "reply before a tool call") || !strings.Contains(turn.Text, "reply after the tool result") {
		t.Errorf("a tool-result user record should not split the turn: %q", turn.Text)
	}
}

func TestLastTurnEmptyTranscript(t *testing.T) {
	path := writeTranscript(t, userLine("prompt with no reply yet"))
	turn, err := LastTurn(path)
	if err != nil {
		t.Fatalf("LastTurn: %v", err)
	}
	if turn.Text != "" {
		t.Errorf("LastTurn with no assistant reply yet should be empty, got %q", turn.Text)
	}
}

func TestLastTurnMissingFile(t *testing.T) {
	if _, err := LastTurn(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Error("LastTurn on a missing file should return an error")
	}
}
