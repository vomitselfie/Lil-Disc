package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := os.MkdirAll(filepath.Join(dir, "lildisc"), 0o755); err != nil {
		t.Fatal(err)
	}

	const content = `
# a comment
GSK_RENDERER=gl

  LILDISC_MPV_HWDEC = vaapi
QUOTED="with spaces"
ALREADY_SET=from-file
not a key-value line
EMPTY_KEY_IGNORED
=novalue
`
	envPath := filepath.Join(dir, "lildisc", "env")
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// A value already in the environment must survive: a one-off override from
	// a shell should beat the config file.
	t.Setenv("ALREADY_SET", "from-environment")

	// Make sure these do not leak into other tests.
	for _, k := range []string{"GSK_RENDERER", "LILDISC_MPV_HWDEC", "QUOTED"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	loadEnvFile()

	for _, tt := range []struct{ key, want string }{
		{"GSK_RENDERER", "gl"},
		{"LILDISC_MPV_HWDEC", "vaapi"},
		{"QUOTED", "with spaces"},
		{"ALREADY_SET", "from-environment"},
	} {
		if got := os.Getenv(tt.key); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestLoadEnvFileMissingIsNotAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	loadEnvFile() // must not panic when the file does not exist
}
