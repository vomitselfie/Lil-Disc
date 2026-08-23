package mods

import (
	"crypto/sha1"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/dijama/lildisc/internal/signaling"
)

var enableAvatarCacheBust = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Avatar Cache Refresh",
	Section:     "Mods",
	Description: "Automatically clear cached avatar when a user's profile picture changes.",
})

// AvatarRefreshSignaler fires when the user manually invalidates their own
// avatar via the win.refresh-avatar action. The user-bar widget subscribes
// to it and refetches/redisplays in response. Signal() must be called from
// the GTK main thread because listeners touch widgets directly.
var AvatarRefreshSignaler signaling.Signaler

// BustImageCache removes the on-disk cached file for the given URL from
// LilDisc's image cache (~/.cache/lildisc/img2/).
// Note: gotkit's in-memory invalidURLs cache (1 hour TTL) cannot be cleared
// without an upstream patch. This clears the disk cache so the next app
// restart will re-fetch correctly.
func BustImageCache(url string) {
	if !enableAvatarCacheBust.Value() || url == "" {
		return
	}

	homeDir, err := os.UserCacheDir()
	if err != nil {
		return
	}

	cacheDir := filepath.Join(homeDir, "lildisc", "img2")
	cachePath := urlCachePath(cacheDir, url)

	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
		slog.Debug("failed to bust image cache file", "url", url, "err", err)
	} else if err == nil {
		slog.Debug("busted avatar cache file", "url", url)
	}
}

// urlCachePath mirrors gotkit's imgutil.urlPath: sha1(url) → base64url filename
func urlCachePath(baseDir, url string) string {
	b := sha1.Sum([]byte(url))
	f := base64.URLEncoding.EncodeToString(b[:])
	return filepath.Join(baseDir, f)
}
