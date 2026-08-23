package mods

import (
	"context"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil"
)

var enableKeybinds = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Additional Keybinds",
	Section:     "Mods",
	Description: "Add extra keyboard shortcuts (Ctrl+/ for help).",
})

func initKeybinds(ctx context.Context, win ActionWidget) {
	if !enableKeybinds.Value() {
		return
	}

	gtkutil.AddActions(win, map[string]func(){
		"keybind-help": func() { showKeybindHelp(win) },
	})

	gtkutil.AddActionShortcuts(win, map[string]string{
		"<Ctrl>slash": "win.keybind-help",
	})
}

func showKeybindHelp(win gtk.Widgetter) {
	keybinds := []struct{ key, action string }{
		{"Ctrl+Q", "Quit"},
		{"Ctrl+K", "Quick Switcher"},
		{"Ctrl+/", "Keybind Help"},
		{"Escape", "Cancel Reply / Stop Editing"},
		{"Up Arrow", "Edit Last Message (empty input)"},
		{"Enter", "Send Message"},
		{"Shift+Enter", "New Line"},
		{"Tab", "Accept Autocomplete"},
	}

	grid := gtk.NewGrid()
	grid.SetColumnSpacing(24)
	grid.SetRowSpacing(8)
	grid.SetMarginTop(12)
	grid.SetMarginBottom(12)
	grid.SetMarginStart(24)
	grid.SetMarginEnd(24)

	for i, kb := range keybinds {
		keyLabel := gtk.NewLabel("")
		keyLabel.SetXAlign(0)
		keyLabel.SetMarkup("<b>" + kb.key + "</b>")

		actionLabel := gtk.NewLabel(kb.action)
		actionLabel.SetXAlign(0)

		grid.Attach(keyLabel, 0, i, 1, 1)
		grid.Attach(actionLabel, 1, i, 1, 1)
	}

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(grid)
	scroll.SetMinContentHeight(200)

	toolbarView := adw.NewToolbarView()
	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(true)
	toolbarView.AddTopBar(header)
	toolbarView.SetContent(scroll)

	dialog := adw.NewDialog()
	dialog.SetTitle("Keyboard Shortcuts")
	dialog.SetContentWidth(360)
	dialog.SetContentHeight(320)
	dialog.SetChild(toolbarView)

	base := gtk.BaseWidget(win)
	root := base.Root()
	if root != nil {
		dialog.Present(root)
	}
}
