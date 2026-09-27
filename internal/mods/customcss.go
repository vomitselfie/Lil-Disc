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
	Description: "Load custom CSS from ~/.config/lildisc/custom.css. Toggling reloads the file.",
})

// customCSSProvider is the installed custom.css, or nil.
var customCSSProvider *gtk.CSSProvider

// initCustomCSS keeps custom.css installed while the preference is on, and
// reloads it each time the preference is turned on.
func initCustomCSS(ctx context.Context) {
	enableCustomCSS.Subscribe(func() {
		if customCSSProvider != nil {
			if display := gdk.DisplayGetDefault(); display != nil {
				gtk.StyleContextRemoveProviderForDisplay(display, customCSSProvider)
			}
			customCSSProvider = nil
		}
		if enableCustomCSS.Value() {
			loadCustomCSS()
		}
	})
}

func loadCustomCSS() {
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
			// Above LilDisc's own theme, which sits at USER+100 and
			// USER+110, so custom.css can still override anything.
			gtk.STYLE_PROVIDER_PRIORITY_USER+200,
		)
		customCSSProvider = provider
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
