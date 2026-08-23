package mods

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/diamondburned/gotkit/app/prefs"
)

var enableCacheClean = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Auto-Clean Image Cache",
	Section:     "Mods",
	Description: "Remove cached images older than 7 days on startup. Keeps the cache warm for recent content while preventing stale entries from accumulating.",
})

const cacheMaxAge = 7 * 24 * time.Hour

// CleanImageCache removes on-disk image cache entries older than cacheMaxAge.
// Recent entries are kept so the cache stays warm across restarts.
func CleanImageCache() {
	if !enableCacheClean.Value() {
		return
	}

	go func() {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return
		}

		imgDir := filepath.Join(cacheDir, "lildisc", "img2")
		entries, err := os.ReadDir(imgDir)
		if err != nil {
			return
		}

		cutoff := time.Now().Add(-cacheMaxAge)
		var removed, kept int

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			if info.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(imgDir, entry.Name()))
				removed++
			} else {
				kept++
			}
		}

		if removed > 0 {
			slog.Info("cleaned stale image cache entries",
				"removed", removed,
				"kept", kept,
				"max_age", cacheMaxAge.String())
		}
	}()
}
