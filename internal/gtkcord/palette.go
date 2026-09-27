package gtkcord

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"

	"github.com/dijama/lildisc/internal/lilcss"
)

// LilDisc ships its own light and dark palettes rather than deriving colours
// from whatever GTK theme is installed. The derived approach meant the app
// took on the look of the host theme (Matcha's red and grey, on a stock
// Manjaro Sway install) and never had one of its own.
//
// The palette is installed in three layers, all above USER priority so that
// a full theme copied into ~/.config/gtk-4.0/gtk.css cannot repaint it:
//
//	USER+100  palette: named colours and libadwaita's CSS variables
//	USER+100  base:    stock widgets (buttons, entries, popovers, switches)
//	USER+110  components: every LilDisc stylesheet again, so the base layer
//	          does not flatten them
//
// custom.css stays on top at USER+200, so users can still override anything.

var useLilDiscTheme = prefs.NewBool(true, prefs.PropMeta{
	Name:    "Use LilDisc's theme",
	Section: "Appearance",
	Description: "Use LilDisc's own light and dark colours instead of the " +
		"system GTK theme. Turn off to follow the system theme.",
})

// Palette is one colour scheme. Every field is a CSS colour expression.
type Palette struct {
	Rail    string // guild rail, the furthest back surface
	Sunken  string // channel and DM sidebar
	Surface string // the chat itself, and the window
	Raised  string // composer, embeds, cards, reactions
	Overlay string // popovers, menus, tooltips

	Text      string
	TextDim   string
	TextFaint string

	Border       string
	BorderStrong string

	Hover    string
	Active   string
	Selected string

	Accent     string // filled accent backgrounds
	AccentText string // accent used as a text or icon colour
	AccentFg   string // text on top of Accent

	Mention     string
	Destructive string
	Success     string
	Warning     string

	Shadow string // popover and floating-surface shadow colour
}

// The accent is the blurple in the app icon. Dark mode lifts it a little so
// it holds the same weight against a near-black surface.
var (
	DarkPalette = Palette{
		Rail:    "#111218",
		Sunken:  "#16171e",
		Surface: "#1b1c24",
		Raised:  "#23242e",
		Overlay: "#2a2b37",

		Text:      "#e7e8f0",
		TextDim:   "#a5a8b9",
		TextFaint: "#6e7286",

		Border:       "alpha(white, 0.06)",
		BorderStrong: "alpha(white, 0.11)",

		Hover:    "alpha(white, 0.045)",
		Active:   "alpha(white, 0.08)",
		Selected: "alpha(#6e79f7, 0.20)",

		Accent:     "#6e79f7",
		AccentText: "#a1a8fb",
		AccentFg:   "#ffffff",

		Mention:     "alpha(#6e79f7, 0.14)",
		Destructive: "#f0474d",
		Success:     "#3ba55d",
		Warning:     "#f0b232",

		Shadow: "alpha(black, 0.45)",
	}

	LightPalette = Palette{
		Rail:    "#e2e4ee",
		Sunken:  "#eceef5",
		Surface: "#f8f9fc",
		Raised:  "#ffffff",
		Overlay: "#ffffff",

		Text:      "#1b1c26",
		TextDim:   "#4e5266",
		TextFaint: "#8a8ea3",

		Border:       "alpha(black, 0.08)",
		BorderStrong: "alpha(black, 0.14)",

		Hover:    "alpha(black, 0.04)",
		Active:   "alpha(black, 0.07)",
		Selected: "alpha(#5865f2, 0.15)",

		Accent:     "#5865f2",
		AccentText: "#4752c4",
		AccentFg:   "#ffffff",

		Mention:     "alpha(#5865f2, 0.11)",
		Destructive: "#d83c3e",
		Success:     "#248046",
		Warning:     "#c27c0e",

		Shadow: "alpha(black, 0.16)",
	}
)

// CSS renders the palette as named colours and libadwaita CSS variables.
func (p Palette) CSS() string {
	var b strings.Builder
	def := func(name, value string) { fmt.Fprintf(&b, "@define-color %s %s;\n", name, value) }

	// LilDisc's own names, which every component stylesheet uses.
	def("lil_rail", p.Rail)
	def("lil_surface_sunken", p.Sunken)
	def("lil_surface", p.Surface)
	def("lil_surface_raised", p.Raised)
	def("lil_surface_overlay", p.Overlay)
	def("lil_text", p.Text)
	def("lil_text_dim", p.TextDim)
	def("lil_text_faint", p.TextFaint)
	def("lil_border", p.Border)
	def("lil_border_strong", p.BorderStrong)
	def("lil_hover", p.Hover)
	def("lil_active", p.Active)
	def("lil_selected", p.Selected)
	def("lil_accent", p.Accent)
	def("lil_accent_text", p.AccentText)
	def("lil_accent_fg", p.AccentFg)
	def("lil_mention", p.Mention)
	def("lil_shadow", p.Shadow)
	def("mentioned", p.Destructive)

	// The legacy GTK names, which older component CSS and GTK itself read.
	def("theme_bg_color", p.Surface)
	def("theme_fg_color", p.Text)
	def("theme_base_color", p.Surface)
	def("theme_text_color", p.Text)
	def("theme_selected_bg_color", p.Accent)
	def("theme_selected_fg_color", p.AccentFg)
	def("theme_unfocused_bg_color", p.Surface)
	def("theme_unfocused_fg_color", p.Text)
	def("borders", p.BorderStrong)

	// libadwaita's names. It reads CSS variables since 1.6, and older rules
	// still use the named colours, so set both.
	adwColors := [][2]string{
		{"window-bg-color", p.Surface},
		{"window-fg-color", p.Text},
		{"view-bg-color", p.Surface},
		{"view-fg-color", p.Text},
		{"headerbar-bg-color", p.Surface},
		{"headerbar-fg-color", p.Text},
		{"headerbar-backdrop-color", p.Surface},
		{"headerbar-border-color", p.Border},
		{"headerbar-shade-color", p.Border},
		{"sidebar-bg-color", p.Sunken},
		{"sidebar-fg-color", p.Text},
		{"sidebar-backdrop-color", p.Sunken},
		{"sidebar-border-color", p.Border},
		{"sidebar-shade-color", p.Border},
		{"secondary-sidebar-bg-color", p.Sunken},
		{"secondary-sidebar-fg-color", p.Text},
		{"card-bg-color", p.Raised},
		{"card-fg-color", p.Text},
		{"card-shade-color", p.Border},
		{"dialog-bg-color", p.Surface},
		{"dialog-fg-color", p.Text},
		{"popover-bg-color", p.Overlay},
		{"popover-fg-color", p.Text},
		{"popover-shade-color", p.Border},
		{"thumbnail-bg-color", p.Raised},
		{"thumbnail-fg-color", p.Text},
		{"accent-color", p.AccentText},
		{"accent-bg-color", p.Accent},
		{"accent-fg-color", p.AccentFg},
		{"destructive-color", p.Destructive},
		{"destructive-bg-color", p.Destructive},
		{"destructive-fg-color", "white"},
		{"success-color", p.Success},
		{"success-bg-color", p.Success},
		{"success-fg-color", "white"},
		{"warning-color", p.Warning},
		{"warning-bg-color", p.Warning},
		{"warning-fg-color", "alpha(black, 0.8)"},
		{"error-color", p.Destructive},
		{"error-bg-color", p.Destructive},
		{"error-fg-color", "white"},
		{"shade-color", p.Shadow},
		{"scrollbar-outline-color", "transparent"},
	}
	for _, c := range adwColors {
		def(strings.ReplaceAll(c[0], "-", "_"), c[1])
	}
	b.WriteString(":root {\n")
	for _, c := range adwColors {
		fmt.Fprintf(&b, "\t--%s: %s;\n", c[0], c[1])
	}
	b.WriteString("}\n")

	return b.String()
}

// The theme owns its providers so it can swap palettes when the colour
// scheme flips and remove everything when the preference is turned off.
type theme struct {
	display    *gdk.Display
	palette    *gtk.CSSProvider
	base       *gtk.CSSProvider
	components *gtk.CSSProvider
	dark       bool
	installed  bool
}

// InitTheme installs LilDisc's theme on the default display and keeps it in
// step with the light/dark preference and the theme preference. It must run
// after the application has started, once a display exists.
func InitTheme() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		slog.Warn("no display; not installing the LilDisc theme")
		return
	}

	t := &theme{
		display:    display,
		palette:    newProvider("lildisc-palette", ""),
		base:       newProvider("lildisc-base", lilcss.Render("base", baseCSS)),
		components: newProvider("lildisc-components", lilcss.Components()),
	}

	styles := adw.StyleManagerGetDefault()
	update := func() {
		t.apply(useLilDiscTheme.Value(), styles.Dark())
	}
	styles.NotifyProperty("dark", update)
	// Subscribe also runs update once, immediately.
	useLilDiscTheme.Subscribe(update)
}

func (t *theme) apply(enabled, dark bool) {
	if !enabled {
		if t.installed {
			for _, p := range []*gtk.CSSProvider{t.palette, t.base, t.components} {
				gtk.StyleContextRemoveProviderForDisplay(t.display, p)
			}
			t.installed = false
		}
		return
	}

	if !t.installed || t.dark != dark {
		palette := LightPalette
		if dark {
			palette = DarkPalette
		}
		t.palette.LoadFromString(palette.CSS())
		t.dark = dark
	}

	if !t.installed {
		user := int(gtk.STYLE_PROVIDER_PRIORITY_USER)
		gtk.StyleContextAddProviderForDisplay(t.display, t.palette, uint(user+100))
		gtk.StyleContextAddProviderForDisplay(t.display, t.base, uint(user+100))
		gtk.StyleContextAddProviderForDisplay(t.display, t.components, uint(user+110))
		t.installed = true
	}
}

func newProvider(name, css string) *gtk.CSSProvider {
	p := gtk.NewCSSProvider()
	p.ConnectParsingError(func(section *gtk.CSSSection, err error) {
		loc := section.StartLocation()
		slog.Warn("theme CSS error",
			"provider", name,
			"line", loc.Lines()+1,
			"err", err)
	})
	if css != "" {
		p.LoadFromString(css)
	}
	return p
}
