package mods

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vomitselfie/Lil-Disc/internal/lilpath"
)

// apiCacheDir returns the directory for cached API responses. It is a
// cache, so it lives under ~/.cache; lilpath.Setup moves the old
// ~/.config/lildisc/api_cache there.
func apiCacheDir() string {
	d := lilpath.CacheDir("api")
	if d == "" || lilpath.MkdirAll(d) != nil {
		return ""
	}
	return d
}

// apiCacheVersion is the envelope format. Bump it when a cached payload's
// shape changes, and older files are ignored rather than misread.
const apiCacheVersion = 1

// apiCacheEnvelope wraps every cached payload. The creation time is
// recorded inside the file rather than read from its mtime, which a copy or
// restore can change.
type apiCacheEnvelope struct {
	Version int             `json:"version"`
	Created time.Time       `json:"created"`
	Data    json.RawMessage `json:"data"`
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
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var env apiCacheEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.Version != apiCacheVersion {
		// Corrupt, or written by an older version without the envelope.
		slog.Debug("apicache: unreadable cache file, removing", "file", filename, "err", err)
		os.Remove(path)
		return false
	}

	if maxAge > 0 && time.Since(env.Created) > maxAge {
		return false
	}

	if err := json.Unmarshal(env.Data, dest); err != nil {
		slog.Debug("apicache: corrupt cache payload, removing", "file", filename, "err", err)
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

	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	b, err := json.Marshal(apiCacheEnvelope{
		Version: apiCacheVersion,
		Created: time.Now(),
		Data:    payload,
	})
	if err != nil {
		return
	}

	if err := lilpath.WriteFile(filepath.Join(dir, filename), b); err != nil {
		slog.Debug("apicache: cannot write cache file", "file", filename, "err", err)
	}
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
