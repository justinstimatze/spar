package gitdiff

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var indexLinePattern = regexp.MustCompile(`(?m)^index [0-9a-f]+\.\.([0-9a-f]+)(?: \d+)?$`)

// RegenerateFileDiff produces a real, syntactically valid unified diff
// between before and after by shelling out to `git diff --no-index`
// against two temp files, then rewriting the header lines to reference
// canonicalPath instead of the temp paths.
//
// This is deliberate: the injector model returns full mutated file
// content, never hand-edited diff syntax, so the diff shown to the user
// is always well-formed — a malformed hand-edited diff would be an
// obvious tell before anyone even reads its content.
func RegenerateFileDiff(before, after, canonicalPath string) (string, error) {
	dir, err := os.MkdirTemp("", "spar-regen-")
	if err != nil {
		return "", err
	}
	// os.MkdirTemp resolves $TMPDIR verbatim with no absoluteness check —
	// a relative TMPDIR would otherwise put the scratch files it's about
	// to write inside whatever directory the caller's cwd happens to be,
	// which for `spar review` is normally the repo under review. Fail
	// closed instead: the caller already treats any error here as "fall
	// back to a clean trial," so refusing is strictly safer than risking
	// a write inside the working tree.
	if !filepath.IsAbs(dir) {
		os.RemoveAll(dir)
		return "", fmt.Errorf("resolved temp directory is not absolute (check $TMPDIR): %q", dir)
	}
	defer os.RemoveAll(dir)

	aPath := filepath.Join(dir, "a")
	bPath := filepath.Join(dir, "b")
	if err := os.WriteFile(aPath, []byte(before), 0600); err != nil {
		return "", err
	}
	if err := os.WriteFile(bPath, []byte(after), 0600); err != nil {
		return "", err
	}

	out, runErr := exec.Command("git", "diff", "--no-index", "--no-color", aPath, bPath).Output()
	if runErr == nil {
		// git diff --no-index exits 0 only when the two files are
		// identical — a no-op mutation, not a usable trial.
		return "", errors.New("regenerated diff: before and after are identical")
	}
	ee, ok := runErr.(*exec.ExitError)
	if !ok || ee.ExitCode() != 1 {
		return "", fmt.Errorf("git diff --no-index: %w", exitErr(runErr))
	}

	// git diff --no-index strips the leading "/" and prepends "a/"/"b/"
	// to whatever absolute path it's given — confirmed empirically
	// against git's actual behavior. Since we chose aPath/bPath
	// ourselves, we know the exact substrings to replace.
	oldHeader := "a/" + strings.TrimPrefix(aPath, string(filepath.Separator))
	newHeader := "b/" + strings.TrimPrefix(bPath, string(filepath.Separator))
	text := string(out)
	text = strings.ReplaceAll(text, oldHeader, "a/"+canonicalPath)
	text = strings.ReplaceAll(text, newHeader, "b/"+canonicalPath)

	if before == "" {
		// aPath was written empty — either a genuinely new file, or a
		// before-content lookup that failed and collapsed to "" (see
		// gitdiff.go's FileContent). Either way there's no real prior
		// content, so render this the way git itself renders a new
		// file — "new file mode", /dev/null on the left, and the zero
		// blob on the index line — rather than what `git diff --no-index`
		// actually produces for two real (one empty) temp files, which
		// is indistinguishable from an ordinary modification and would
		// be an obvious tell to anyone who's looked at a real
		// added-file diff before.
		if nl := strings.IndexByte(text, '\n'); nl != -1 && strings.HasPrefix(text, "diff --git ") {
			text = text[:nl] + "\nnew file mode 100644" + text[nl:]
		}
		text = indexLinePattern.ReplaceAllString(text, "index 0000000..$1")
		text = strings.ReplaceAll(text, "--- a/"+canonicalPath, "--- /dev/null")
	}

	return text, nil
}

// LineNumbersTouched returns the after-side line numbers that carry a
// "+" (added/changed) line in a single-file unified diff — the exact
// lines a mutation changed, not just the hunk spans they sit in.
func LineNumbersTouched(diffText string) []int {
	var touched []int
	newLine := 0
	inHunk := false
	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			_, _, newStart, _, ok := parseHunkHeader(line)
			if !ok {
				inHunk = false
				continue
			}
			newLine = newStart
			inHunk = true
			continue
		}
		if !inHunk {
			continue
		}
		if line == "" {
			continue
		}
		switch line[0] {
		case '+':
			touched = append(touched, newLine)
			newLine++
		case ' ':
			newLine++
		case '-':
			// old-side only, doesn't advance the new-side counter
		case '\\':
			// "\ No newline at end of file" — not a content line
		}
	}
	return touched
}
