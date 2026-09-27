// Package lilpath owns where LilDisc keeps its own files, following the XDG
// split between configuration, cache and state, and writes them privately
// and atomically.
//
// Before this, LilDisc's own files lived under the config directory whatever
// they were (an API cache, emoji recents), were created world-readable
// (0755 directories, 0644 files) although they hold account data, and were
// written in place, so a crash mid-write left a truncated file.
//
// Files gotkit manages (preferences, drafts, remembered widths) keep their
// locations; gotkit already writes them 0600.
package lilpath

import (
	"os"
	"path/filepath"
)

const appDir = "lildisc"

// Private permissions for everything LilDisc writes.
const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// ConfigDir returns ~/.config/lildisc joined with parts. It does not create
// anything.
func ConfigDir(parts ...string) string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{base, appDir}, parts...)...)
}

// CacheDir returns ~/.cache/lildisc joined with parts: disposable data that
// can be refetched.
func CacheDir(parts ...string) string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{base, appDir}, parts...)...)
}

// StateDir returns ~/.local/state/lildisc joined with parts: data worth
// keeping between runs that is neither configuration nor disposable.
func StateDir(parts ...string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(append([]string{base, appDir}, parts...)...)
}

// MkdirAll creates dir privately.
func MkdirAll(dir string) error {
	return os.MkdirAll(dir, dirMode)
}

// WriteFile writes data to path privately and atomically: into a temporary
// file in the same directory, synced, then renamed over path. The parent
// directory is created if needed.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := MkdirAll(dir); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once renamed

	if err := f.Chmod(fileMode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Migrate moves old to new if old exists and new does not, so a file or
// directory changing location is carried over once. It reports whether it
// moved anything.
func Migrate(old, new string) bool {
	if old == "" || new == "" {
		return false
	}
	if _, err := os.Lstat(old); err != nil {
		return false
	}
	if _, err := os.Lstat(new); err == nil {
		return false
	}
	if err := MkdirAll(filepath.Dir(new)); err != nil {
		return false
	}
	return os.Rename(old, new) == nil
}

// Tighten makes LilDisc's config and cache directories private. They hold
// preferences, drafts and images from private conversations, and were
// created 0755. Only the top-level directories are changed; anything
// written inside them from now on is private anyway.
func Tighten() {
	for _, dir := range []string{ConfigDir(), CacheDir(), StateDir()} {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() && info.Mode().Perm()&0o077 != 0 {
			os.Chmod(dir, dirMode)
		}
	}
}

// Setup moves LilDisc's files from where earlier versions kept them and
// tightens directory permissions. Call it once at startup, before anything
// reads them.
func Setup() {
	// The API cache is disposable, so it belongs in the cache directory.
	Migrate(ConfigDir("api_cache"), CacheDir("api"))
	// Emoji recents are state: worth keeping, but not configuration.
	Migrate(ConfigDir("emoji_recents.json"), StateDir("emoji_recents.json"))
	Tighten()
}
