package mods

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// apiCacheDir returns the directory for cached API responses.
func apiCacheDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	d := filepath.Join(dir, "lildisc", "api_cache")
	os.MkdirAll(d, 0o755)
	return d
}

// safeCacheFilename rejects anything that could escape apiCacheDir. Callers
// today only pass typed-int formatted names, but this guards against future
// callers passing untrusted strings.
func safeCacheFilename(filename string) bool {
	if filename == "" || filename == "." || filename == ".." {
		return false
	}
	if strings.ContainsAny(filename, `/\`) {
		return false
	}
	if strings.Contains(filename, "..") {
		return false
	}
	return true
}

// loadCachedJSON loads a JSON file from the API cache directory.
// Returns false if the file doesn't exist or is older than maxAge.
// Pass maxAge <= 0 to accept any age (never expire).
func loadCachedJSON(filename string, maxAge time.Duration, dest interface{}) bool {
	if !safeCacheFilename(filename) {
		return false
	}
	dir := apiCacheDir()
	if dir == "" {
		return false
	}

	path := filepath.Join(dir, filename)
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	// Check expiry.
	if maxAge > 0 && time.Since(info.ModTime()) > maxAge {
		return false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	if err := json.Unmarshal(data, dest); err != nil {
		slog.Debug("apicache: corrupt cache file, removing", "file", filename, "err", err)
		os.Remove(path)
		return false
	}

	return true
}

// saveCachedJSON saves a JSON file to the API cache directory.
func saveCachedJSON(filename string, data interface{}) {
	if !safeCacheFilename(filename) {
		return
	}
	dir := apiCacheDir()
	if dir == "" {
		return
	}

	b, err := json.Marshal(data)
	if err != nil {
		return
	}

	path := filepath.Join(dir, filename)
	os.WriteFile(path, b, 0o644)
}

// ClearAPICache removes all cached API responses, forcing fresh fetches.
func ClearAPICache() {
	dir := apiCacheDir()
	if dir == "" {
		return
	}
	os.RemoveAll(dir)
	slog.Info("cleared API cache", "dir", dir)
}
