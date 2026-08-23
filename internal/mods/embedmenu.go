package mods

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/diamondburned/chatkit/components/embed"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/gtkutil"
)

// AttachEmbedContextMenu adds a right-click context menu to an embed widget
// with Save As, Copy URL, and Open in Browser options.
func AttachEmbedContextMenu(ctx context.Context, embedWidget *embed.Embed, sourceURL, filename string) {
	if sourceURL == "" {
		return
	}
	if filename == "" {
		filename = filenameFromURL(sourceURL)
	}

	actions := map[string]func(){
		"embedctx.save": func() {
			saveEmbedMedia(ctx, sourceURL, filename)
		},
		"embedctx.copy-url": func() {
			display := gdk.DisplayGetDefault()
			if display != nil {
				display.Clipboard().SetText(sourceURL)
			}
		},
		"embedctx.open-browser": func() {
			app.OpenURI(ctx, sourceURL)
		},
	}

	gtkutil.BindActionMap(embedWidget, actions)
	gtkutil.BindPopoverMenuCustom(embedWidget, gtk.PosBottom, []gtkutil.PopoverMenuItem{
		gtkutil.MenuItemIcon("_Save As…", "embedctx.save", "document-save-symbolic"),
		gtkutil.MenuItemIcon("_Copy URL", "embedctx.copy-url", "edit-copy-symbolic"),
		gtkutil.MenuItemIcon("Open in _Browser", "embedctx.open-browser", "web-browser-symbolic"),
	})
}

func saveEmbedMedia(ctx context.Context, sourceURL, filename string) {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Save Media")
	dialog.SetInitialName(filename)

	dialog.Save(ctx, app.GTKWindowFromContext(ctx), func(result gio.AsyncResulter) {
		file, err := dialog.SaveFinish(result)
		if err != nil {
			// Cancelling the dialog also lands here; not an error.
			return
		}
		outPath := file.Path()

		go func() {
			if err := downloadFile(sourceURL, outPath); err != nil {
				slog.Error("failed to save media", "url", sourceURL, "path", outPath, "err", err)
			} else {
				slog.Info("media saved", "path", outPath)
			}
		}()
	})
}

// maxEmbedDownloadSize caps the on-disk size of any embed save. Discord
// attachments cap at 500MB on Nitro, so this matches that ceiling. The cap
// exists to bound damage from a hostile or malfunctioning CDN claiming a
// Content-Length it doesn't honor.
const maxEmbedDownloadSize = 500 << 20 // 500 MiB

func downloadFile(url, outPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: unexpected status %d", resp.StatusCode)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	limited := &io.LimitedReader{R: resp.Body, N: maxEmbedDownloadSize + 1}
	_, copyErr := io.Copy(out, limited)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(outPath)
		return fmt.Errorf("write file: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(outPath)
		return fmt.Errorf("close file: %w", closeErr)
	}
	if limited.N <= 0 {
		os.Remove(outPath)
		return fmt.Errorf("download exceeds %d byte limit", maxEmbedDownloadSize)
	}

	return nil
}

func filenameFromURL(u string) string {
	return MediaFilename(u, "")
}

// MediaFilename extracts a usable filename from a URL. If the URL's path
// doesn't contain a recognisable media extension, fallbackExt (e.g. ".mp4")
// is appended. This also fixes URLs like Twitter's ".../1280x720/go" where
// path.Base returns "go" with no extension.
func MediaFilename(u string, fallbackExt string) string {
	// Strip query params.
	if idx := strings.IndexByte(u, '?'); idx >= 0 {
		u = u[:idx]
	}
	name := path.Base(u)
	if name == "" || name == "." || name == "/" {
		name = "media"
	}

	ext := path.Ext(name)
	if isMediaExtension(ext) {
		return name
	}

	// No recognisable extension — append fallback.
	if fallbackExt != "" {
		if !strings.HasPrefix(fallbackExt, ".") {
			fallbackExt = "." + fallbackExt
		}
		return name + fallbackExt
	}
	return name
}

func isMediaExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg",
		".mp4", ".webm", ".mov", ".avi", ".mkv", ".m4v",
		".mp3", ".ogg", ".wav", ".flac":
		return true
	}
	return false
}
