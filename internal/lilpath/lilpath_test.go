package lilpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileIsPrivateAndComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.json")

	if err := WriteFile(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("two")); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil || string(b) != "two" {
		t.Fatalf("read %q, %v", b, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}

	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestMigrate(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.json")
	moved := filepath.Join(dir, "new", "moved.json")

	if Migrate(old, moved) {
		t.Error("migrated a file that does not exist")
	}

	os.WriteFile(old, []byte("x"), 0o644)
	if !Migrate(old, moved) {
		t.Fatal("did not migrate")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old file still present")
	}

	// A second copy must not overwrite what is already at the new place.
	os.WriteFile(old, []byte("y"), 0o644)
	if Migrate(old, moved) {
		t.Error("migrated over an existing destination")
	}
	if b, _ := os.ReadFile(moved); string(b) != "x" {
		t.Errorf("destination overwritten: %q", b)
	}
}

func TestStateDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	if got := StateDir("f"); got != "/tmp/xdg-state/lildisc/f" {
		t.Errorf("StateDir = %q", got)
	}
}
