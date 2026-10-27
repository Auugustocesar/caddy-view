package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func statOf(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestFileIDTellsFilesApartAndSurvivesARename(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	idA, idB := fileID(a, statOf(t, a)), fileID(b, statOf(t, b))
	if idA == "" || idB == "" {
		t.Fatalf("fileID = %q and %q, want an identity on this platform", idA, idB)
	}
	if idA == idB {
		t.Errorf("two different files have the same ID %q", idA)
	}
	if again := fileID(a, statOf(t, a)); again != idA {
		t.Errorf("the same file has IDs %q and %q", idA, again)
	}

	moved := filepath.Join(dir, "a.log.1")
	if err := os.Rename(a, moved); err != nil {
		t.Fatal(err)
	}
	if got := fileID(moved, statOf(t, moved)); got != idA {
		t.Errorf("after a rename the ID is %q, want %q", got, idA)
	}
}

func TestFileIDOfAMissingFileIsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.log")
	if got := fileID(missing, nil); got != "" {
		t.Errorf("fileID of a missing file = %q, want empty", got)
	}
}
