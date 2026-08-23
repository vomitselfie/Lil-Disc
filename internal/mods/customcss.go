package mods

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
)

var enableCustomCSS = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Load Custom CSS",
	Section:     "Mods",
	Description: "Load custom CSS from ~/.config/lildisc/custom.css on startup.",
})

func initCustomCSS(ctx context.Context) {
	if !enableCustomCSS.Value() {
		return
	}

	cssPath, err := customCSSPath()
	if err != nil {
		slog.Warn("cannot determine config dir for custom CSS", "err", err)
		return
	}

	if _, err := os.Stat(cssPath); os.IsNotExist(err) {
		slog.Debug("no custom CSS file found", "path", cssPath)
		return
	}

	provider := gtk.NewCSSProvider()
	provider.LoadFromPath(cssPath)

	display := gdk.DisplayGetDefault()
	if display != nil {
		gtk.StyleContextAddProviderForDisplay(
			display,
			provider,
			gtk.STYLE_PROVIDER_PRIORITY_USER,
		)
		slog.Info("loaded custom CSS", "path", cssPath)
	}
}

func customCSSPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "lildisc", "custom.css"), nil
}
