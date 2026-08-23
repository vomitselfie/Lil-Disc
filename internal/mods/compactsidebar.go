package mods

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
	"github.com/dijama/lildisc/internal/signaling"
)

var enableCompactSidebar = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Compact DM Sidebar",
	Section:     "Mods",
	Description: "When the sidebar is narrow, show only avatars without names.",
})

var _ = cssutil.WriteCSS(`
	.mod-sidebar-compact .direct-channel {
		padding: 4px 2px;
	}
	.mod-sidebar-compact .direct-channel-avatar {
		margin-right: 0;
	}
	.mod-sidebar-compact .mod-friend-row {
		padding: 4px 2px;
	}
	.mod-sidebar-compact .mod-friend-row-avatar {
		margin-right: 0;
	}
	.mod-sidebar-compact .mod-friend-list-expander {
		margin: 6px 2px 2px 2px;
		padding-top: 4px;
	}
`)

// Compact-mode width thresholds, measured on the sidebar's list column
// (the guild rail is not included).
//
// The two values form a dead band: the sidebar enters compact mode below
// CompactEnterWidth and only leaves it again above CompactLeaveWidth. A
// single threshold would let the state flip on every frame while the user
// rests the drag handle on it, and because toggling compact changes widget
// visibility — which changes the requested width — that flip can feed back
// into itself.
const (
	CompactEnterWidth = 180
	CompactLeaveWidth = 200

	// CompactWidth is the list column's width in compact mode: an avatar
	// plus its row padding, with nothing else to show.
	CompactWidth = 56
)

// compactSidebarState is the single source of truth for icon-only mode.
// Widgets do not measure anything themselves; they subscribe to it and are
// told. Only the sidebar's width driver calls SetSidebarCompact.
var compactSidebarState struct {
	compact bool
	changed signaling.Signaler
}

// SidebarCompact reports whether the sidebar is currently in icon-only mode.
func SidebarCompact() bool {
	return compactSidebarState.compact
}

// SetSidebarCompact sets icon-only mode and notifies every bound widget.
// Must be called from the GTK main thread. Calling it with the current
// value is free.
func SetSidebarCompact(compact bool) {
	if !enableCompactSidebar.Value() {
		compact = false
	}
	if compactSidebarState.compact == compact {
		return
	}
	compactSidebarState.compact = compact
	compactSidebarState.changed.Signal()
}

// CompactSidebarEnabled reports whether the compact-sidebar mod is on.
func CompactSidebarEnabled() bool { return enableCompactSidebar.Value() }

// SubscribeCompactSidebarEnabled runs f whenever the compact-sidebar
// preference is toggled, so the sidebar can re-evaluate its current width
// against the new setting. The returned function unsubscribes.
func SubscribeCompactSidebarEnabled(f func()) func() {
	return enableCompactSidebar.Subscribe(f)
}

// BindCompactSidebar makes widget follow icon-only mode. apply is called
// immediately with the current state and again on every change, and is
// disconnected when widget is destroyed.
//
// This replaces the previous approach of walking the sidebar's widget tree
// and matching CSS class names: rows created while already compact never
// got their labels hidden, renaming a CSS class silently broke the feature,
// and the walk ran from a per-frame tick callback. Now each widget states
// its own compact behaviour at construction, so newly built rows are
// correct by definition.
func BindCompactSidebar(widget gtk.Widgetter, apply func(compact bool)) {
	apply(compactSidebarState.compact)

	disconnect := compactSidebarState.changed.Connect(func() {
		apply(compactSidebarState.compact)
	})

	gtk.BaseWidget(widget).ConnectDestroy(disconnect)
}

// BindCompactSidebarRow is the common case of BindCompactSidebar: a row
// whose child widgets are hidden in compact mode, and which centres itself
// on its avatar once they are gone.
func BindCompactSidebarRow(row gtk.Widgetter, hide ...gtk.Widgetter) {
	rowBase := gtk.BaseWidget(row)

	BindCompactSidebar(row, func(compact bool) {
		for _, w := range hide {
			if w != nil {
				gtk.BaseWidget(w).SetVisible(!compact)
			}
		}
		if compact {
			rowBase.SetHAlign(gtk.AlignCenter)
		} else {
			rowBase.SetHAlign(gtk.AlignFill)
		}
	})
}

// SetupCompactSidebar marks widget as the container that carries the
// compact CSS class, so the stylesheet above can restyle its descendants.
// Width measurement is not done here — see the sidebar's width driver.
func SetupCompactSidebar(widget gtk.Widgetter) {
	base := gtk.BaseWidget(widget)

	BindCompactSidebar(widget, func(compact bool) {
		if compact {
			base.AddCSSClass("mod-sidebar-compact")
		} else {
			base.RemoveCSSClass("mod-sidebar-compact")
		}
	})
}
