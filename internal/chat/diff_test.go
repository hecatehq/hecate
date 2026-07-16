package chat

import "testing"

func TestParseChangedFilesFromUnifiedDiff(t *testing.T) {
	diff := `diff --git a/README.md b/README.md
index 111..222 100644
--- a/README.md
+++ b/README.md
@@ -1,2 +1,3 @@
 hello
-old
+new
+extra
diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-gone
diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+born`

	files := ParseChangedFiles(diff, "")
	if len(files) != 3 {
		t.Fatalf("file count = %d, want 3: %#v", len(files), files)
	}
	byPath := map[string]ChangedFile{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	if got := byPath["README.md"]; got.Status != "modified" || got.Additions != 2 || got.Deletions != 1 || got.Diff == "" {
		t.Fatalf("README.md = %#v", got)
	}
	if got := byPath["new.txt"]; got.Status != "added" || got.Additions != 1 || got.Deletions != 0 {
		t.Fatalf("new.txt = %#v", got)
	}
	if got := byPath["old.txt"]; got.Status != "deleted" || got.Additions != 0 || got.Deletions != 1 {
		t.Fatalf("old.txt = %#v", got)
	}
}

func TestParseChangedFilesFallsBackToDiffStat(t *testing.T) {
	diffStat := "README.md | 2 +-\nui/src/ChatView.tsx | 12 +++++++---\n2 files changed, 8 insertions(+), 4 deletions(-)"

	files := ParseChangedFiles("", diffStat)
	if len(files) != 2 {
		t.Fatalf("file count = %d, want 2: %#v", len(files), files)
	}
	if files[0].Path != "README.md" || files[0].Additions != 1 || files[0].Deletions != 1 {
		t.Fatalf("first file = %#v", files[0])
	}
	if files[1].Path != "ui/src/ChatView.tsx" || files[1].Additions != 7 || files[1].Deletions != 3 {
		t.Fatalf("second file = %#v", files[1])
	}
}

func TestExtractFileDiff(t *testing.T) {
	diff := `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-a
+b
diff --git a/b.txt b/b.txt
--- a/b.txt
+++ b/b.txt
@@ -1 +1 @@
-c
+d`

	file, ok := ExtractFileDiff(diff, "b.txt")
	if !ok {
		t.Fatal("ExtractFileDiff ok = false, want true")
	}
	if file.Path != "b.txt" || file.Additions != 1 || file.Deletions != 1 {
		t.Fatalf("file = %#v", file)
	}
	if file.Diff == "" || file.Diff == diff {
		t.Fatalf("file diff = %q, want only b.txt block", file.Diff)
	}
	if _, ok := ExtractFileDiff(diff, "missing.txt"); ok {
		t.Fatal("ExtractFileDiff missing ok = true, want false")
	}
}

func TestParseChangedFilesPreservesUnusualGitPaths(t *testing.T) {
	t.Parallel()

	controlPath := "line\nname\t.txt"
	diff := "diff --git a/foo b/bar.txt b/foo b/bar.txt\n" +
		"--- a/foo b/bar.txt\n" +
		"+++ b/foo b/bar.txt\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n" +
		"diff --git \"a/line\\nname\\t.txt\" \"b/line\\nname\\t.txt\"\n" +
		"--- \"a/line\\nname\\t.txt\"\n" +
		"+++ \"b/line\\nname\\t.txt\"\n" +
		"@@ -1 +1 @@\n" +
		"-before\n" +
		"+after\n"

	files := ParseChangedFiles(diff, "")
	if len(files) != 2 {
		t.Fatalf("files = %#v, want two unusual paths", files)
	}
	byPath := make(map[string]ChangedFile, len(files))
	for _, file := range files {
		byPath[file.Path] = file
	}
	if got, ok := byPath["foo b/bar.txt"]; !ok || got.Additions != 1 || got.Deletions != 1 {
		t.Fatalf("space-delimiter path = %#v, present=%v", got, ok)
	}
	if got, ok := byPath[controlPath]; !ok || got.Additions != 1 || got.Deletions != 1 {
		t.Fatalf("C-quoted path = %#v, present=%v", got, ok)
	}
	if _, ok := ExtractFileDiff(diff, controlPath); !ok {
		t.Fatalf("ExtractFileDiff(%q) did not preserve exact decoded path", controlPath)
	}
}

func TestParseChangedFilesUsesDecodedRenameAndCopyDestinations(t *testing.T) {
	t.Parallel()

	diff := "diff --git a/old.txt \"b/new\\tname.txt\"\n" +
		"similarity index 100%\n" +
		"rename from old.txt\n" +
		"rename to \"new\\tname.txt\"\n" +
		"diff --git a/source.txt \"b/copy\\nname.txt\"\n" +
		"similarity index 100%\n" +
		"copy from source.txt\n" +
		"copy to \"copy\\nname.txt\"\n"

	files := ParseChangedFiles(diff, "")
	if len(files) != 2 {
		t.Fatalf("files = %#v, want rename and copy", files)
	}
	if files[0].Path != "copy\nname.txt" || files[0].Status != "copied" {
		t.Fatalf("copy = %#v", files[0])
	}
	if files[1].Path != "new\tname.txt" || files[1].Status != "renamed" {
		t.Fatalf("rename = %#v", files[1])
	}
}
