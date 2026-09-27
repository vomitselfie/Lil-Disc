package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/adaptive"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/components/logui"
	"github.com/diamondburned/gotkit/components/prefui"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
	"github.com/vomitselfie/Lil-Disc/internal/mods"
	"github.com/vomitselfie/Lil-Disc/internal/window"
	"github.com/vomitselfie/Lil-Disc/internal/window/about"

	_ "github.com/diamondburned/gotkit/gtkutil/aggressivegc"
	_ "github.com/vomitselfie/Lil-Disc/internal/icons"
)

//go:embed po/*
var po embed.FS

func init() {
	po, _ := fs.Sub(po, "po")
	locale.LoadLocale(po)
}

// Version is connected to about.SetVersion.
var Version string

func init() { about.SetVersion(Version) }

var _ = lilcss.WriteCSS(`
	window.background,
	window.background.solid-csd {
		background-color: @theme_bg_color;
	}

	avatar > image {
		background: none;
	}
	avatar > label {
		background: @borders;
	}

	/* Message body text. This is the one thing in the app people read at
	   length, so it gets the loosest leading. */
	.md-textblock {
		line-height: {$line_body};
	}

	/* Blockquotes: tighter line spacing than regular text.
	   The body line-height is too airy inside > quotes. */
	.md-blockquote .md-textblock {
		line-height: {$line_tight};
	}

	/* Everything else that holds prose. Only the markdown TextViews above
	   used to carry a line-height, so labels — embed descriptions, system
	   messages, search results, tooltips — fell back to pango's default
	   leading and rendered visibly tighter than the message text right
	   beside them. Wrapping labels are the ones where it shows. */
	label {
		line-height: {$line_tight};
	}
	.message-content-box label,
	.message-normalembed label,
	.message-system-content,
	.mod-search-content {
		line-height: {$line_body};
	}

	/* Code blocks: reduce excess bottom padding inside the scroll frame.
	   Upstream has padding: 4px 6px on the text; the scroll propagation
	   adds extra dead space below short blocks. */
	.md-codeblock-frame textview {
		padding: 4px 6px 2px 6px;
	}
	.md-codeblock-frame scrolledwindow {
		margin-bottom: 0;
		padding-bottom: 0;
	}

	/* Reply previews: give the content a bit more breathing room.
	   The 0.9em font + tight blockquote padding makes replies feel cramped. */
	.message-reply-box {
		padding-top: 2px;
		padding-bottom: 4px;
	}
	.message-reply-box .mauthor-chip {
		margin-bottom: 1px;
	}
	.message-reply-content {
		margin-top: 1px;
	}
`)

func init() {
	app.Hook(func(*app.Application) {
		adw.Init()
		adaptive.Init()
	})
}

// loadEnvFile applies KEY=VALUE lines from ~/.config/lildisc/env to this
// process, ignoring blank lines and those starting with '#'. Variables already
// present in the environment win, so a one-off override from a shell still
// takes effect.
//
// This exists because some things have to be decided before GTK initialises
// and therefore cannot be preferences: GSK_RENDERER picks the rendering
// backend, and on a hybrid or eGPU machine that choice decides which GPU gets
// woken up. GTK's Vulkan renderer selects a discrete GPU when it finds one,
// even if that GPU is not the one driving the display, which on an external
// card means spinning it up merely to draw a chat window. GStreamer's decoder
// ranking (GST_PLUGIN_FEATURE_RANK) has the same character: it is read as the
// pipeline is built, long before any settings UI exists.
//
// Keeping this as a file rather than baked-in defaults matters because the
// right answer is per-machine — the correct device ID on one laptop is wrong
// on every other.
func loadEnvFile() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}

	path := filepath.Join(configDir, "lildisc", "env")
	f, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("cannot read %s: %v", path, err)
		}
		return
	}

	for _, line := range strings.Split(string(f), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			continue
		}
		if _, taken := os.LookupEnv(key); taken {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			log.Printf("cannot set %s from %s: %v", key, path, err)
		}
	}
}

func main() {
	// Both run before app.New, which initialises GTK and fixes the renderer
	// choice. The env file goes first so an explicit override there beats the
	// preference toggle.
	loadEnvFile()
	applyGraphicsPrefs()

	// After loadEnvFile, because the client version and build numbers it
	// installs can be overridden from that file, and before any Discord
	// state is constructed, because arikawa reads them as it builds the
	// REST client and the gateway.
	gtkcord.InitClientIdentity()

	m := manager{}
	m.app = app.New(context.Background(), "io.github.vomitselfie.lildisc", "LilDisc")
	m.app.AddJSONActions(map[string]interface{}{
		"app.preferences": func() { prefui.ShowDialog(m.win.Context()) },
		"app.about":       func() { about.New(m.win.Context()).Present(m.win) },
		"app.logs":        func() { logui.ShowDefaultViewer(m.win.Context()) },
		"app.quit":        func() { m.app.Quit() },
	})
	m.app.AddActionCallbacks(map[string]gtkutil.ActionCallback{
		"app.open-channel": m.forwardSignalToWindow("open-channel", gtkcord.SnowflakeVariant),
		"app.open-guild":   m.forwardSignalToWindow("open-guild", gtkcord.SnowflakeVariant),
		"app.open-message": m.forwardSignalToWindow("open-message", gtkcord.MessageLocationVariantType),
	})
	m.app.AddActionShortcuts(map[string]string{
		"<Ctrl>Q": "app.quit",
	})
	m.app.ConnectActivate(func() { m.activate(m.app.Context()) })
	m.app.RunMain()
}

type manager struct {
	app *app.Application
	win *window.Window
}

func (m *manager) forwardSignalToWindow(name string, t *glib.VariantType) gtkutil.ActionCallback {
	return gtkutil.ActionCallback{
		ArgType: t,
		Func:    func(args *glib.Variant) { m.win.ActivateAction(name, args) },
	}
}

func (m *manager) activate(ctx context.Context) {
	if m.win != nil {
		m.win.Present()
		return
	}

	// Before the window exists, so it never paints in the host theme first.
	gtkcord.InitTheme()

	m.win = window.NewWindow(ctx)
	m.win.Present()

	// mod: initialize all mods after window is ready
	mods.Init(ctx, m.win)

	prefs.AsyncLoadSaved(ctx, func(err error) {
		if err != nil {
			app.Error(ctx, err)
		}
	})
}
