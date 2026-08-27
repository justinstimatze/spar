// Package gitdiff captures a real git diff, parses it into per-file
// pieces, and can splice a locally-regenerated single-file diff back in
// place of one real file's section — without ever writing to the
// working tree or git index itself.
package gitdiff

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// LineRange is an inclusive after-side line range, 1-indexed.
type LineRange struct {
	Start, End int
}

// FileChange describes one file's section of a diff.
type FileChange struct {
	Path    string // canonical (new-side) path
	OldPath string // equals Path unless this is a rename
	Status  byte   // 'A' added, 'M' modified, 'D' deleted, 'R' renamed
	Binary  bool

	// HunkRanges are the after-side line spans the real diff touched,
	// taken straight from each hunk's @@ header. Used to scope where an
	// injected mutation is allowed to land — see internal/inject.
	HunkRanges []LineRange
}

// Diff is a captured, parsed git diff.
type Diff struct {
	RawText string
	Staged  bool
	Files   []FileChange
}

// Empty reports whether there's nothing to review.
func (d Diff) Empty() bool {
	return strings.TrimSpace(d.RawText) == ""
}

// RepoRoot resolves the git repository root containing startDir.
func RepoRoot(startDir string) (string, error) {
	out, err := exec.Command("git", "-C", startDir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository (or any parent up to mount point): %w", exitErr(err))
	}
	return strings.TrimSpace(string(out)), nil
}

// Capture returns the staged diff if non-empty, else the working-tree
// diff. -M enables rename detection so renamed files are identifiable
// (and, deliberately, excluded from injection candidacy elsewhere).
func Capture(repoRoot string) (Diff, error) {
	staged, err := runGitDiff(repoRoot, true)
	if err != nil {
		return Diff{}, err
	}
	if strings.TrimSpace(staged) != "" {
		files, err := parseDiff(staged)
		if err != nil {
			return Diff{}, err
		}
		return Diff{RawText: staged, Staged: true, Files: files}, nil
	}
	working, err := runGitDiff(repoRoot, false)
	if err != nil {
		return Diff{}, err
	}
	files, err := parseDiff(working)
	if err != nil {
		return Diff{}, err
	}
	return Diff{RawText: working, Staged: false, Files: files}, nil
}

func runGitDiff(repoRoot string, staged bool) (string, error) {
	args := []string{"-C", repoRoot, "diff", "--no-color", "-M"}
	if staged {
		args = append(args, "--staged")
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", exitErr(err))
	}
	return string(out), nil
}

// FileContent resolves the before/after content for one changed file.
// before comes from HEAD (empty for a new file, or any error resolving
// it — an empty repo, a path not yet in HEAD, all collapse to ""). after
// comes from the git index when the diff being reviewed is staged (so a
// diff shown mid-further-edit stays consistent with what --staged
// reported), or from the working tree on disk otherwise. after is empty
// for a deleted file.
func FileContent(repoRoot string, fc FileChange, staged bool) (before, after string, err error) {
	beforePath := fc.OldPath
	if beforePath == "" {
		beforePath = fc.Path
	}
	if fc.Status != 'A' {
		if out, err := exec.Command("git", "-C", repoRoot, "show", "HEAD:"+beforePath).Output(); err == nil {
			before = string(out)
		}
	}
	if fc.Status == 'D' {
		return before, "", nil
	}
	if staged {
		out, err := exec.Command("git", "-C", repoRoot, "show", ":"+fc.Path).Output()
		if err != nil {
			return before, "", fmt.Errorf("git show :%s: %w", fc.Path, exitErr(err))
		}
		return before, string(out), nil
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, fc.Path))
	if err != nil {
		return before, "", err
	}
	return before, string(data), nil
}

// SpliceFile replaces the diff block for path with replacementBlock,
// leaving every other file's section untouched. replacementBlock should
// be a complete, standalone "diff --git a/<path> b/<path>\n..." section.
func (d Diff) SpliceFile(path, replacementBlock string) (string, error) {
	blocks := splitDiffBlocks(d.RawText)
	found := false
	for i, b := range blocks {
		if strings.Contains(b, "+++ b/"+path+"\n") || strings.HasSuffix(strings.TrimRight(b, "\n"), "+++ b/"+path) {
			if !strings.HasSuffix(replacementBlock, "\n") {
				replacementBlock += "\n"
			}
			blocks[i] = replacementBlock
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("splice: no diff block found for %q", path)
	}
	return strings.Join(blocks, ""), nil
}

func splitDiffBlocks(rawText string) []string {
	var blocks []string
	var cur strings.Builder
	started := false
	for _, line := range strings.SplitAfter(rawText, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if started {
				blocks = append(blocks, cur.String())
				cur.Reset()
			}
			started = true
		}
		if started {
			cur.WriteString(line)
		}
	}
	if started {
		blocks = append(blocks, cur.String())
	}
	return blocks
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseHunkHeader parses a "@@ -a,b +c,d @@" line. A count omitted from
// the header (bare "@@ -5 +5 @@") means a count of 1, per unified-diff
// convention.
func parseHunkHeader(line string) (oldStart, oldCount, newStart, newCount int, ok bool) {
	m := hunkHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, 0, 0, false
	}
	oldStart, _ = strconv.Atoi(m[1])
	oldCount = 1
	if m[2] != "" {
		oldCount, _ = strconv.Atoi(m[2])
	}
	newStart, _ = strconv.Atoi(m[3])
	newCount = 1
	if m[4] != "" {
		newCount, _ = strconv.Atoi(m[4])
	}
	return oldStart, oldCount, newStart, newCount, true
}

func parseDiff(rawText string) ([]FileChange, error) {
	var files []FileChange
	var cur *FileChange
	flush := func() {
		if cur == nil {
			return
		}
		if cur.OldPath == "" {
			cur.OldPath = cur.Path
		}
		if cur.Path == "" {
			cur.Path = cur.OldPath
		}
		files = append(files, *cur)
		cur = nil
	}
	for _, line := range strings.Split(rawText, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = &FileChange{Status: 'M'}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = 'A'
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = 'D'
		case strings.HasPrefix(line, "rename from "):
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
			cur.Status = 'R'
		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
			rest := strings.TrimSuffix(strings.TrimPrefix(line, "Binary files "), " differ")
			if parts := strings.SplitN(rest, " and ", 2); len(parts) == 2 {
				if p := strings.TrimPrefix(parts[0], "a/"); p != "/dev/null" {
					cur.OldPath = p
				}
				if p := strings.TrimPrefix(parts[1], "b/"); p != "/dev/null" {
					cur.Path = p
				}
			}
		case strings.HasPrefix(line, "--- "):
			p := strings.TrimPrefix(line, "--- ")
			if p != "/dev/null" && cur.OldPath == "" {
				cur.OldPath = strings.TrimPrefix(p, "a/")
			}
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p != "/dev/null" {
				cur.Path = strings.TrimPrefix(p, "b/")
			}
		case strings.HasPrefix(line, "@@ "):
			if _, _, newStart, newCount, ok := parseHunkHeader(line); ok && newCount > 0 {
				cur.HunkRanges = append(cur.HunkRanges, LineRange{Start: newStart, End: newStart + newCount - 1})
			}
		}
	}
	flush()
	return files, nil
}

// exitErr flattens an *exec.ExitError's stderr into the error text so
// callers/logs see the actual git message instead of just "exit status 1".
func exitErr(err error) error {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}
