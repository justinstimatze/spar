// Package render shows a diff to the terminal (via delta if present,
// else plain text), prompts for a reaction, and reveals ground truth.
package render

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/justinstimatze/spar/internal/inject"
)

// Show renders diffText via delta when it's on PATH — matching the
// reviewer's normal git-diff-viewing setup — or plain text otherwise.
// If delta's own pager kicks in (TTY + delta's default paging: auto),
// the Prompt below only appears after the pager is quit — expected,
// matches the normal `git diff | delta` experience, not a bug.
func Show(w io.Writer, diffText string) error {
	if path, err := exec.LookPath("delta"); err == nil {
		cmd := exec.Command(path)
		cmd.Stdin = strings.NewReader(diffText)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	fmt.Fprintln(w, "(delta not found on PATH; showing plain diff)")
	_, err := fmt.Fprint(w, diffText)
	return err
}

// Prompt asks whether the diff looked right. Enter approves; a line
// starting with "f" flags a concern and asks for free text describing
// it (logged as-is, not fuzzy-matched against the hidden ground truth).
func Prompt(r *bufio.Reader, w io.Writer) (flagged bool, text string) {
	fmt.Fprint(w, "\n[Enter] looks good   [f] flag a concern: ")
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if strings.HasPrefix(strings.ToLower(line), "f") {
		fmt.Fprint(w, "what's the concern? ")
		t, _ := r.ReadString('\n')
		return true, strings.TrimSpace(t)
	}
	return false, ""
}

// Reveal prints ground truth and the computed outcome, and reminds the
// reviewer that nothing was written to their repository.
func Reveal(w io.Writer, res inject.Result, flagged bool, flagText string, outcome string) {
	fmt.Fprintln(w)
	if res.Injected {
		fmt.Fprintf(w, "This trial was INJECTED — file %s, category %q, severity %s.\n", res.File, res.Category, res.Severity)
		fmt.Fprintf(w, "What changed: %s\n", res.Description)
	} else {
		fmt.Fprintln(w, "This trial was CLEAN — the real diff, unmodified.")
		if res.FallbackReason != "" {
			fmt.Fprintf(w, "(injection was attempted but fell back: %s)\n", res.FallbackReason)
		}
	}
	if flagged {
		fmt.Fprintf(w, "You flagged a concern: %q\n", flagText)
	} else {
		fmt.Fprintln(w, "You did not flag a concern.")
	}
	fmt.Fprintf(w, "Outcome: %s\n", outcome)
	fmt.Fprintln(w, "spar: nothing was written to your repository.")
}
