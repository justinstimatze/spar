package gitdiff

import (
	"reflect"
	"testing"
)

const threeFileDiff = `diff --git a/added.go b/added.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/added.go
@@ -0,0 +1,2 @@
+package foo
+func A() {}
diff --git a/modified.go b/modified.go
index abc1234..def5678 100644
--- a/modified.go
+++ b/modified.go
@@ -10,3 +10,3 @@ func B() {
 	x := 1
-	y := 2
+	y := 3
 	return x + y
diff --git a/deleted.go b/deleted.go
deleted file mode 100644
index 9876543..0000000
--- a/deleted.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package foo
-func D() {}
`

func TestParseDiffThreeFiles(t *testing.T) {
	files, err := parseDiff(threeFileDiff)
	if err != nil {
		t.Fatalf("parseDiff: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3: %+v", len(files), files)
	}

	added, modified, deleted := files[0], files[1], files[2]

	if added.Path != "added.go" || added.Status != 'A' {
		t.Errorf("added: got Path=%q Status=%c, want added.go/A", added.Path, added.Status)
	}
	if got := added.HunkRanges; !reflect.DeepEqual(got, []LineRange{{Start: 1, End: 2}}) {
		t.Errorf("added HunkRanges = %+v", got)
	}

	if modified.Path != "modified.go" || modified.Status != 'M' {
		t.Errorf("modified: got Path=%q Status=%c, want modified.go/M", modified.Path, modified.Status)
	}
	if got := modified.HunkRanges; !reflect.DeepEqual(got, []LineRange{{Start: 10, End: 12}}) {
		t.Errorf("modified HunkRanges = %+v", got)
	}

	if deleted.Path != "deleted.go" || deleted.Status != 'D' {
		t.Errorf("deleted: got Path=%q Status=%c, want deleted.go/D", deleted.Path, deleted.Status)
	}
	if len(deleted.HunkRanges) != 0 {
		t.Errorf("deleted HunkRanges should be empty (newCount=0), got %+v", deleted.HunkRanges)
	}
}

func TestParseDiffRename(t *testing.T) {
	diff := `diff --git a/old_name.go b/new_name.go
similarity index 95%
rename from old_name.go
rename to new_name.go
index abc1234..def5678 100644
--- a/old_name.go
+++ b/new_name.go
@@ -1,2 +1,2 @@
-package foo
+package bar
 func X() {}
`
	files, err := parseDiff(diff)
	if err != nil {
		t.Fatalf("parseDiff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	f := files[0]
	if f.Status != 'R' || f.OldPath != "old_name.go" || f.Path != "new_name.go" {
		t.Errorf("rename: got Status=%c OldPath=%q Path=%q", f.Status, f.OldPath, f.Path)
	}
}

func TestParseDiffBinary(t *testing.T) {
	diff := `diff --git a/image.png b/image.png
index 1234567..89abcde 100644
Binary files a/image.png and b/image.png differ
`
	files, err := parseDiff(diff)
	if err != nil {
		t.Fatalf("parseDiff: %v", err)
	}
	if len(files) != 1 || !files[0].Binary || files[0].Path != "image.png" {
		t.Errorf("binary: got %+v", files)
	}
}

func TestParseDiffEmpty(t *testing.T) {
	files, err := parseDiff("")
	if err != nil {
		t.Fatalf("parseDiff: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %+v", files)
	}
}

func TestDiffEmpty(t *testing.T) {
	if !(Diff{RawText: "  \n"}).Empty() {
		t.Error("whitespace-only diff should be Empty")
	}
	if (Diff{RawText: threeFileDiff}).Empty() {
		t.Error("real diff should not be Empty")
	}
}

func TestSpliceFile(t *testing.T) {
	d := Diff{RawText: threeFileDiff}
	replacement := `diff --git a/modified.go b/modified.go
index abc1234..fff9999 100644
--- a/modified.go
+++ b/modified.go
@@ -10,3 +10,3 @@ func B() {
 	x := 1
-	y := 2
+	y := 999
 	return x + y
`
	spliced, err := d.SpliceFile("modified.go", replacement)
	if err != nil {
		t.Fatalf("SpliceFile: %v", err)
	}

	files, err := parseDiff(spliced)
	if err != nil {
		t.Fatalf("parseDiff(spliced): %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("splice changed file count: got %d, want 3", len(files))
	}
	if files[0].Path != "added.go" || files[2].Path != "deleted.go" {
		t.Errorf("splice disturbed other files: %+v", files)
	}
	// The other two files' blocks must be byte-identical to the original.
	origBlocks := splitDiffBlocks(threeFileDiff)
	splicedBlocks := splitDiffBlocks(spliced)
	if origBlocks[0] != splicedBlocks[0] {
		t.Errorf("added.go block changed by splice")
	}
	if origBlocks[2] != splicedBlocks[2] {
		t.Errorf("deleted.go block changed by splice")
	}
	if splicedBlocks[1] != replacement {
		t.Errorf("modified.go block = %q, want replacement", splicedBlocks[1])
	}
}

func TestSpliceFileNotFound(t *testing.T) {
	d := Diff{RawText: threeFileDiff}
	if _, err := d.SpliceFile("does-not-exist.go", "diff --git a/x b/x\n"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseHunkHeaderNoCounts(t *testing.T) {
	// A count omitted (bare "@@ -5 +5 @@") means count=1.
	oldStart, oldCount, newStart, newCount, ok := parseHunkHeader("@@ -5 +5 @@")
	if !ok || oldStart != 5 || oldCount != 1 || newStart != 5 || newCount != 1 {
		t.Errorf("got %d,%d,%d,%d,%v", oldStart, oldCount, newStart, newCount, ok)
	}
}

func TestLineNumbersTouchedSingleChange(t *testing.T) {
	diff := `diff --git a/f.go b/f.go
index abc..def 100644
--- a/f.go
+++ b/f.go
@@ -10,3 +10,3 @@ func B() {
 	x := 1
-	y := 2
+	y := 3
 	return x + y
`
	got := LineNumbersTouched(diff)
	want := []int{11}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLineNumbersTouchedMultipleAddedLines(t *testing.T) {
	diff := `diff --git a/f.go b/f.go
new file mode 100644
index 0000000..abc123
--- /dev/null
+++ b/f.go
@@ -0,0 +1,3 @@
+line1
+line2
+line3
`
	got := LineNumbersTouched(diff)
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLineNumbersTouchedNoNewlineMarker(t *testing.T) {
	// "\ No newline at end of file" must be skipped, not counted as content.
	diff := "diff --git a/f.go b/f.go\n" +
		"index abc..def 100644\n" +
		"--- a/f.go\n" +
		"+++ b/f.go\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old\n" +
		"\\ No newline at end of file\n" +
		"+new\n" +
		"\\ No newline at end of file\n"
	got := LineNumbersTouched(diff)
	want := []int{1}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLineNumbersTouchedNoHunks(t *testing.T) {
	if got := LineNumbersTouched("just some text\nwith no diff markers\n"); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
