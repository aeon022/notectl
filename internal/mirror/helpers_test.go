package mirror

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLeafFolderAndAppleBody(t *testing.T) {
	for in, want := range map[string]string{"": "", "Notes": "Notes", "Projects/Git": "Git", "a/b/c": "c"} {
		if got := leafFolder(in); got != want {
			t.Errorf("leafFolder(%q) = %q, want %q", in, got, want)
		}
	}
	if appleBody("T", "") != "T" || appleBody("T", "body") != "T\n\nbody" {
		t.Error("Apple body must start with the title line")
	}
}

func TestStampModTime(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "n.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	stampModTime(dir, "n.md", want)
	st, _ := os.Stat(f)
	if !st.ModTime().Equal(want) {
		t.Errorf("mtime = %v, want %v", st.ModTime(), want)
	}
	before := st.ModTime()
	stampModTime(dir, "n.md", time.Time{}) // zero time must be a no-op
	if st2, _ := os.Stat(f); !st2.ModTime().Equal(before) {
		t.Error("zero time must not touch the file")
	}
	stampModTime(dir, "missing.md", want) // best-effort, must not panic
}
