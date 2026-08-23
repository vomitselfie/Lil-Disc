package mods

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"log/slog"
	"os"
	"sync"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/dijama/lildisc/internal/icons"
)

// sniIconPixmap is the SNI-spec shape for an icon: a slice of (width,
// height, ARGB pixel data) triples. Tray hosts pick the closest size.
type sniIconPixmap = []struct {
	W, H int32
	Data []byte
}

var (
	trayIconOnce   sync.Once
	trayIconPixmap sniIconPixmap
)

// trayIconData returns the lildisc logo decoded from icons.LogoPNG into
// network-byte-order ARGB (A, R, G, B) — the format SNI requires. The
// decode is cached after the first call.
func trayIconData() sniIconPixmap {
	trayIconOnce.Do(func() {
		img, err := png.Decode(bytes.NewReader(icons.LogoPNG))
		if err != nil {
			slog.Warn("tray: failed to decode embedded logo", "err", err)
			return
		}
		// Normalise to NRGBA (non-premultiplied 8-bit) regardless of what
		// the PNG decoder handed back, so we can read raw pixels.
		bounds := img.Bounds()
		nrgba := image.NewNRGBA(bounds)
		draw.Draw(nrgba, bounds, img, bounds.Min, draw.Src)
		w, h := bounds.Dx(), bounds.Dy()
		argb := make([]byte, w*h*4)
		for y := 0; y < h; y++ {
			src := nrgba.Pix[(y-bounds.Min.Y)*nrgba.Stride:]
			dst := argb[y*w*4:]
			for x := 0; x < w; x++ {
				r := src[x*4+0]
				g := src[x*4+1]
				b := src[x*4+2]
				a := src[x*4+3]
				dst[x*4+0] = a
				dst[x*4+1] = r
				dst[x*4+2] = g
				dst[x*4+3] = b
			}
		}
		trayIconPixmap = sniIconPixmap{
			{W: int32(w), H: int32(h), Data: argb},
		}
	})
	return trayIconPixmap
}

var enableTray = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Close to Tray",
	Section:     "Mods",
	Description: "Minimize to system tray when closing the window instead of quitting.",
})

const (
	sniDBusPath  = "/StatusNotifierItem"
	sniItemIface = "org.freedesktop.StatusNotifierItem"
	menuDBusPath = "/MenuBar"
	menuIface    = "com.canonical.dbusmenu"
)

// trayItem holds the D-Bus connection and window reference.
type trayItem struct {
	conn *dbus.Conn
	win  gtk.Widgetter
}

func initTray(ctx context.Context, win ActionWidget) {
	if !enableTray.Value() {
		return
	}

	// Hold the application so it stays alive when the window is hidden.
	app.FromContext(ctx).Hold()

	// Intercept window close: hide instead of destroy.
	base := gtk.BaseWidget(win)
	var connected bool
	base.ConnectMap(func() {
		if connected {
			return
		}
		connected = true
		gtkWin := base.Root().CastType(gtk.GTypeWindow).(*gtk.Window)
		gtkWin.ConnectCloseRequest(func() (stop bool) {
			if !enableTray.Value() {
				return false
			}
			gtkWin.SetVisible(false)
			return true
		})
	})

	// Start the SNI tray icon.
	go func() {
		if err := startSNI(ctx, win); err != nil {
			slog.Warn("failed to start system tray icon", "err", err)
			slog.Info("close-to-tray will still hide the window, but no tray icon will appear")
		}
	}()
}

func startSNI(ctx context.Context, win gtk.Widgetter) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to session bus: %w", err)
	}
	defer conn.Close()

	item := &trayItem{conn: conn, win: win}
	menu := &trayMenu{item: item}
	busName := conn.Names()[0]

	// Export SNI on both freedesktop and KDE interfaces.
	conn.Export(item, sniDBusPath, sniItemIface)
	conn.Export(item, sniDBusPath, "org.kde.StatusNotifierItem")
	conn.Export(item, sniDBusPath, "org.freedesktop.DBus.Properties")
	conn.Export(introspect.NewIntrospectable(&introspect.Node{
		Name: sniDBusPath,
		Interfaces: []introspect.Interface{{
			Name: sniItemIface,
			Methods: []introspect.Method{
				{Name: "Activate", Args: []introspect.Arg{
					{Name: "x", Type: "i", Direction: "in"},
					{Name: "y", Type: "i", Direction: "in"},
				}},
				{Name: "SecondaryActivate", Args: []introspect.Arg{
					{Name: "x", Type: "i", Direction: "in"},
					{Name: "y", Type: "i", Direction: "in"},
				}},
				{Name: "ContextMenu", Args: []introspect.Arg{
					{Name: "x", Type: "i", Direction: "in"},
					{Name: "y", Type: "i", Direction: "in"},
				}},
			},
			Properties: []introspect.Property{
				{Name: "Category", Type: "s", Access: "read"},
				{Name: "Id", Type: "s", Access: "read"},
				{Name: "Title", Type: "s", Access: "read"},
				{Name: "Status", Type: "s", Access: "read"},
				{Name: "IconName", Type: "s", Access: "read"},
				{Name: "ItemIsMenu", Type: "b", Access: "read"},
				// "Menu" intentionally omitted; see sniProperties().
			},
		}},
	}), sniDBusPath, "org.freedesktop.DBus.Introspectable")

	// Export the dbusmenu for right-click context menu.
	conn.Export(menu, menuDBusPath, menuIface)
	conn.Export(menu, menuDBusPath, "org.freedesktop.DBus.Properties")
	conn.Export(introspect.NewIntrospectable(&introspect.Node{
		Name: menuDBusPath,
		Interfaces: []introspect.Interface{{
			Name: menuIface,
			Methods: []introspect.Method{
				{Name: "GetLayout", Args: []introspect.Arg{
					{Name: "parentId", Type: "i", Direction: "in"},
					{Name: "recursionDepth", Type: "i", Direction: "in"},
					{Name: "propertyNames", Type: "as", Direction: "in"},
					{Name: "revision", Type: "u", Direction: "out"},
					{Name: "layout", Type: "(ia{sv}av)", Direction: "out"},
				}},
				{Name: "Event", Args: []introspect.Arg{
					{Name: "id", Type: "i", Direction: "in"},
					{Name: "eventId", Type: "s", Direction: "in"},
					{Name: "data", Type: "v", Direction: "in"},
					{Name: "timestamp", Type: "u", Direction: "in"},
				}},
				{Name: "AboutToShow", Args: []introspect.Arg{
					{Name: "id", Type: "i", Direction: "in"},
					{Name: "needUpdate", Type: "b", Direction: "out"},
				}},
			},
			Signals: []introspect.Signal{
				{Name: "LayoutUpdated", Args: []introspect.Arg{
					{Name: "revision", Type: "u"},
					{Name: "parent", Type: "i"},
				}},
			},
		}},
	}), menuDBusPath, "org.freedesktop.DBus.Introspectable")

	// Register with watcher (KDE first, then freedesktop).
	var registered bool
	for _, watcher := range []string{"org.kde.StatusNotifierWatcher", "org.freedesktop.StatusNotifierWatcher"} {
		obj := conn.Object(watcher, "/StatusNotifierWatcher")
		call := obj.Call(watcher+".RegisterStatusNotifierItem", 0, busName)
		if call.Err == nil {
			registered = true
			slog.Info("registered tray icon", "watcher", watcher)
			break
		}
	}
	if !registered {
		return fmt.Errorf("no StatusNotifierWatcher found")
	}

	slog.Info("system tray icon registered")

	// Emit LayoutUpdated so tray hosts know the menu has content.
	conn.Emit(menuDBusPath, menuIface+".LayoutUpdated", uint32(1), int32(0))

	<-ctx.Done()
	return nil
}

// --- SNI Item methods ---

func (t *trayItem) Activate(x, y int32) *dbus.Error {
	glib.IdleAdd(func() {
		base := gtk.BaseWidget(t.win)
		gtkWin := base.Root().CastType(gtk.GTypeWindow).(*gtk.Window)
		if gtkWin.IsVisible() {
			gtkWin.SetVisible(false)
		} else {
			gtkWin.SetVisible(true)
			gtkWin.Present()
		}
	})
	return nil
}

// SecondaryActivate is called by tray hosts on right-click when no dbusmenu
// is available. Shows the window and presents a quit dialog.
func (t *trayItem) SecondaryActivate(x, y int32) *dbus.Error {
	return t.ContextMenu(x, y)
}

func (t *trayItem) ContextMenu(x, y int32) *dbus.Error {
	// Right-click handler. We deliberately omit the Menu property in
	// sniProperties so Waybar (and any compliant SNI host) skips its
	// dbusmenu path and falls through to this RPC, which gives us a real
	// adw.AlertDialog we control instead of a libdbusmenu-rendered menu
	// that's flaky on Wayland bottom bars.
	glib.IdleAdd(func() {
		base := gtk.BaseWidget(t.win)
		root := base.Root()
		if root == nil {
			return
		}
		gtkWin, ok := root.CastType(gtk.GTypeWindow).(*gtk.Window)
		if !ok || gtkWin == nil {
			return
		}
		wasVisible := gtkWin.IsVisible()

		// adw.AlertDialog.Present accepts a nil parent (see gotk4-adwaita
		// adw-dialog_1_5.go: `if parent != nil` gates the arg). With nil
		// parent, libadwaita presents as a standalone top-level, so we
		// don't have to map the main window first.
		alert := adw.NewAlertDialog("LilDisc", "")
		const (
			respShow = "show"
			respQuit = "quit"
		)
		showLabel := "_Show Window"
		if wasVisible {
			showLabel = "_Hide Window"
		}
		alert.AddResponse(respShow, showLabel)
		alert.AddResponse(respQuit, "_Quit")
		alert.AddResponse("cancel", "_Cancel")
		alert.SetResponseAppearance(respQuit, adw.ResponseDestructive)
		alert.SetDefaultResponse(respShow)
		alert.SetCloseResponse("cancel")

		alert.ConnectResponse(func(response string) {
			switch response {
			case respShow:
				if wasVisible {
					gtkWin.SetVisible(false)
				} else {
					gtkWin.SetVisible(true)
					gtkWin.Present()
				}
			case respQuit:
				os.Exit(0)
			}
		})

		alert.Present(nil)
	})
	return nil
}

// --- SNI Properties ---

func (t *trayItem) Get(iface, prop string) (dbus.Variant, *dbus.Error) {
	if iface != sniItemIface && iface != "org.kde.StatusNotifierItem" {
		return dbus.Variant{}, nil
	}
	props := sniProperties()
	if v, ok := props[prop]; ok {
		return v, nil
	}
	return dbus.Variant{}, nil
}

func (t *trayItem) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != sniItemIface && iface != "org.kde.StatusNotifierItem" {
		return nil, nil
	}
	return sniProperties(), nil
}

func (t *trayItem) Set(iface, prop string, value dbus.Variant) *dbus.Error {
	return nil
}

func sniProperties() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"Category": dbus.MakeVariant("Communications"),
		"Id":       dbus.MakeVariant("lildisc"),
		"Title":    dbus.MakeVariant("LilDisc"),
		"Status":   dbus.MakeVariant("Active"),
		// IconName is the themed fallback if the host ignores IconPixmap.
		// It's our app id so it matches the installed hicolor icon.
		"IconName":      dbus.MakeVariant("io.github.dijama.lildisc"),
		"IconThemePath": dbus.MakeVariant(""),
		"ItemIsMenu":    dbus.MakeVariant(false),
		"WindowId":      dbus.MakeVariant(int32(0)),
		// IconPixmap is the universal way to ship an icon with no theme
		// install required — the bytes travel over D-Bus.
		"IconPixmap": dbus.MakeVariant(trayIconData()),
		// NOTE: the "Menu" property is deliberately absent. Waybar
		// reads SNI properties via DBus.Properties.GetAll, then on
		// right-click checks `menu.empty()` (item.cpp:540) — only when
		// the string is empty does it skip the dbusmenu path and fall
		// through to calling our ContextMenu() RPC. We tried setting
		// Menu="/NO_DBUSMENU" previously but Waybar still passed that
		// to libdbusmenu_gtkmenu_new which returns a non-NULL "zombie"
		// menu, suppressing the RPC fallback. Omitting the property
		// entirely is the only way to reliably reach our RPC handler.
	}
}

// --- DBusMenu for right-click context ---

// Menu item IDs
const (
	menuIDRoot = 0
	menuIDShow = 1
	menuIDQuit = 2
)

type trayMenu struct {
	item   *trayItem
	revision uint32
}

// menuLayout is the D-Bus (ia{sv}av) struct for dbusmenu items.
type menuLayout struct {
	ID         int32                    `dbus:"struct"`
	Properties map[string]dbus.Variant  `dbus:"struct"`
	Children   []dbus.Variant           `dbus:"struct"`
}

// GetLayout implements com.canonical.dbusmenu.
func (m *trayMenu) GetLayout(parentID int32, recursionDepth int32, propertyNames []string) (uint32, menuLayout, *dbus.Error) {
	slog.Debug("GetLayout called", "parentID", parentID, "depth", recursionDepth)
	show := menuLayout{
		ID: menuIDShow,
		Properties: map[string]dbus.Variant{
			"label":   dbus.MakeVariant("Show/Hide"),
			"enabled": dbus.MakeVariant(true),
			"visible": dbus.MakeVariant(true),
		},
		Children: []dbus.Variant{},
	}

	quit := menuLayout{
		ID: menuIDQuit,
		Properties: map[string]dbus.Variant{
			"label":   dbus.MakeVariant("Quit"),
			"enabled": dbus.MakeVariant(true),
			"visible": dbus.MakeVariant(true),
		},
		Children: []dbus.Variant{},
	}

	root := menuLayout{
		ID: menuIDRoot,
		Properties: map[string]dbus.Variant{
			"children-display": dbus.MakeVariant("submenu"),
		},
		Children: []dbus.Variant{
			dbus.MakeVariant(show),
			dbus.MakeVariant(quit),
		},
	}

	return m.revision, root, nil
}

// GetGroupProperties implements com.canonical.dbusmenu.
func (m *trayMenu) GetGroupProperties(ids []int32, propertyNames []string) ([]struct {
	V0 int32
	V1 map[string]dbus.Variant
}, *dbus.Error) {
	return nil, nil
}

// GetProperty implements com.canonical.dbusmenu.
func (m *trayMenu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	return dbus.Variant{}, nil
}

// Event implements com.canonical.dbusmenu.
func (m *trayMenu) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	switch id {
	case menuIDShow:
		m.item.Activate(0, 0)
	case menuIDQuit:
		glib.IdleAdd(func() {
			os.Exit(0)
		})
	}
	return nil
}

// EventGroup implements com.canonical.dbusmenu.
func (m *trayMenu) EventGroup(events []struct {
	V0 int32
	V1 string
	V2 dbus.Variant
	V3 uint32
}) ([]int32, *dbus.Error) {
	for _, ev := range events {
		m.Event(ev.V0, ev.V1, ev.V2, ev.V3)
	}
	return nil, nil
}

// AboutToShow implements com.canonical.dbusmenu.
func (m *trayMenu) AboutToShow(id int32) (bool, *dbus.Error) {
	return true, nil
}

// AboutToShowGroup implements com.canonical.dbusmenu.
func (m *trayMenu) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return nil, nil, nil
}

// --- Menu Properties interface ---

func (m *trayMenu) Get(iface, prop string) (dbus.Variant, *dbus.Error) {
	if prop == "Version" {
		return dbus.MakeVariant(uint32(3)), nil
	}
	if prop == "TextDirection" {
		return dbus.MakeVariant("ltr"), nil
	}
	if prop == "Status" {
		return dbus.MakeVariant("normal"), nil
	}
	if prop == "IconThemePath" {
		return dbus.MakeVariant([]string{}), nil
	}
	return dbus.Variant{}, nil
}

func (m *trayMenu) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	return map[string]dbus.Variant{
		"Version":       dbus.MakeVariant(uint32(3)),
		"TextDirection": dbus.MakeVariant("ltr"),
		"Status":        dbus.MakeVariant("normal"),
		"IconThemePath": dbus.MakeVariant([]string{}),
	}, nil
}

func (m *trayMenu) Set(iface, prop string, value dbus.Variant) *dbus.Error {
	return nil
}
