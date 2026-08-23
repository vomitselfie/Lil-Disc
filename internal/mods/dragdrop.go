package mods

import (
	"context"
	"io"
	"log/slog"
	"mime"
	"path/filepath"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
)

var enableDragDrop = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Drag-and-Drop Upload",
	Section:     "Mods",
	Description: "Enable drag-and-drop file upload onto the message area.",
})

var _ = cssutil.WriteCSS(`
	.mod-drag-target:drop(active) {
		outline: 2px dashed @accent_color;
		outline-offset: -2px;
	}
`)

// DroppedFile represents a file received from a drag-and-drop operation.
type DroppedFile struct {
	Name string
	Type string // MIME type
	Size int64
	Open func() (io.ReadCloser, error)
}

// SetupDragDrop attaches a GTK DropTarget to the given widget.
// The onDrop callback is called for each file dropped.
// Returns false if the mod is disabled.
func SetupDragDrop(ctx context.Context, widget gtk.Widgetter, onDrop func(DroppedFile)) bool {
	if !enableDragDrop.Value() {
		return false
	}

	base := gtk.BaseWidget(widget)

	// First, remove any existing DropTarget controllers from the widget
	// (e.g., the TextView's built-in handler that pastes file paths as text).
	var toRemove []gtk.EventControllerer
	for i := uint(0); i < base.ObserveControllers().NItems(); i++ {
		obj := base.ObserveControllers().Item(i)
		if _, ok := obj.Cast().(*gtk.DropTarget); ok {
			toRemove = append(toRemove, obj.Cast().(gtk.EventControllerer))
		}
	}
	for _, ctrl := range toRemove {
		base.RemoveController(ctrl)
	}

	// Add our own DropTargetAsync in the capture phase so we intercept
	// before any remaining default handlers.
	drop := gtk.NewDropTargetAsync(nil, gdk.ActionCopy)
	drop.SetPropagationPhase(gtk.PhaseCapture)

	// GTK4 automatically sets :drop(active) pseudo-class on the widget
	// when a drag is hovering, so we just need a CSS rule on a marker class.
	// (ConnectDragLeave panics on nil GdkDrop in gotk4 bindings.)
	base.AddCSSClass("mod-drag-target")

	drop.ConnectDrop(func(dropper gdk.Dropper, x, y float64) bool {
		gdkDrop := dropper.(*gdk.Drop)

		// Ask for the file list when the source can provide one. Dragging
		// several files at once produces a single GdkFileList; requesting
		// GFile gets only the first of them, which is why every file after the
		// first used to be silently dropped. A single-file drag may offer only
		// GFile, so that stays as the fallback.
		//
		// The formats have to be unioned with the deserialisable GTypes first.
		// A file manager advertises text/uri-list, not the GdkFileList GType —
		// GDK only expands that while deserialising — so testing the raw
		// formats would answer "no file list" for exactly the drags this is
		// meant to catch.
		readType := gio.GTypeFile
		if formats := gdkDrop.Formats(); formats != nil {
			if formats.UnionDeserializeGTypes().ContainGType(gdk.GTypeFileList) {
				readType = gdk.GTypeFileList
			}
		}

		gdkDrop.ReadValueAsync(ctx, readType, int(glib.PriorityDefault), func(res gio.AsyncResulter) {
			val, err := gdkDrop.ReadValueFinish(res)
			if err != nil {
				slog.Warn("failed to read dropped value", "err", err)
				gdkDrop.Finish(0)
				return
			}

			files := droppedFiles(val)
			if len(files) == 0 {
				gdkDrop.Finish(0)
				return
			}

			slog.Debug("drag-drop: received files", "count", len(files))
			for _, file := range files {
				processDroppedFile(ctx, file, onDrop)
			}
			gdkDrop.Finish(gdk.ActionCopy)
		})

		return true
	})

	base.AddController(drop)
	return true
}

// droppedFiles pulls the files out of a dropped value, which is a GdkFileList
// for a multi-file drag and a bare GFile otherwise.
func droppedFiles(val *coreglib.Value) []*gio.File {
	if list, ok := val.GoValue().(*gdk.FileList); ok {
		return list.Files()
	}

	// Single-file drags arrive as a plain GFile object.
	if obj := val.Object(); obj != nil {
		if file, ok := obj.Cast().(*gio.File); ok && file != nil {
			return []*gio.File{file}
		}
	}

	slog.Warn("drag-drop: unhandled dropped value type", "type", val.TypeName())
	return nil
}

func processDroppedFile(ctx context.Context, file *gio.File, onDrop func(DroppedFile)) {
	basename := file.Basename()

	info, err := file.QueryInfo(
		ctx,
		"standard::size,standard::content-type",
		gio.FileQueryInfoNone,
	)

	var size int64
	var mimeType string
	if err == nil {
		size = info.Size()
		mimeType = info.ContentType()
	} else {
		slog.Warn("could not query dropped file info", "err", err)
		mimeType = mime.TypeByExtension(filepath.Ext(basename))
	}

	onDrop(DroppedFile{
		Name: basename,
		Type: mimeType,
		Size: size,
		Open: func() (io.ReadCloser, error) {
			// Use Background context: the original drop context may be
			// cancelled by the time the user hits Send.
			bgCtx := context.Background()
			stream, err := file.Read(bgCtx)
			if err != nil {
				return nil, err
			}
			return gioutil.Reader(bgCtx, stream), nil
		},
	})
}
