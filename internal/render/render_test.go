package render

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/justinstimatze/spar/internal/inject"
)

func TestPromptApprove(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	var w bytes.Buffer
	flagged, text := Prompt(r, &w)
	if flagged {
		t.Fatalf("Prompt() flagged = true, want false for a bare Enter")
	}
	if text != "" {
		t.Fatalf("Prompt() text = %q, want empty", text)
	}
}

func TestPromptFlag(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("f\nlooks like an off-by-one\n"))
	var w bytes.Buffer
	flagged, text := Prompt(r, &w)
	if !flagged {
		t.Fatalf("Prompt() flagged = false, want true for an \"f\" response")
	}
	if text != "looks like an off-by-one" {
		t.Fatalf("Prompt() text = %q, want %q", text, "looks like an off-by-one")
	}
}

func TestPromptFlagCaseInsensitive(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("F\nconcern\n"))
	var w bytes.Buffer
	flagged, _ := Prompt(r, &w)
	if !flagged {
		t.Fatalf("Prompt() flagged = false, want true for an uppercase \"F\" response")
	}
}

func TestRevealInjected(t *testing.T) {
	var w bytes.Buffer
	res := inject.Result{
		Injected:    true,
		File:        "queue.go",
		Category:    "off-by-one",
		Severity:    "medium",
		Description: "boundary check off by one",
	}
	Reveal(&w, res, true, "found it", "catch")
	out := w.String()
	for _, want := range []string{
		"INJECTED", "queue.go", "off-by-one", "medium",
		"boundary check off by one", "found it", "Outcome: catch",
		"nothing was written to your repository",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Reveal() output missing %q, got:\n%s", want, out)
		}
	}
}

func TestRevealCleanWithFallback(t *testing.T) {
	var w bytes.Buffer
	res := inject.Result{Injected: false, FallbackReason: "no eligible file"}
	Reveal(&w, res, false, "", "true_negative")
	out := w.String()
	for _, want := range []string{
		"CLEAN", "fell back: no eligible file", "did not flag",
		"Outcome: true_negative",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Reveal() output missing %q, got:\n%s", want, out)
		}
	}
}
