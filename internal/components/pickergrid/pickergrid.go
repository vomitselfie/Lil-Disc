// Package pickergrid is the virtualized grid behind the emoji, sticker and
// GIF pickers.
//
// The pickers used to build a box, a picture and a click gesture for every
// result up front. With a few thousand custom emoji that stalled the main
// thread, which is why the emoji picker capped an empty search at 200, and
// the gesture-driven boxes could not be reached by keyboard at all.
//
// The grid is a GtkListView whose rows are either a section header or a
// line of cells. GTK only creates rows for what is on screen and recycles
// them while scrolling, so the cost follows the viewport, not the result
// count. Every cell is a real button: Tab and the arrow keys reach it, Enter
// and Space activate it, and screen readers announce it by its tooltip.
package pickergrid

import (
	"context"
	"strconv"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

// Item is one selectable cell.
type Item struct {
	// ImageURL shows an image. Text is used when it is empty, for Unicode
	// emoji.
	ImageURL string
	Text     string
	// Label names the item for screen readers and, with Detail, the tooltip.
	Label  string
	Detail string
	// Activate runs when the item is picked.
	Activate func()
}

// Section is a titled group of items. An empty title shows no header.
type Section struct {
	Title string
	Items []Item
}

// Options configure a grid.
type Options struct {
	Columns  int
	CellSize int
	// AnimateOnHover plays animated images while the pointer is over them.
	AnimateOnHover bool
	// Class is added to the grid, for per-picker styling.
	Class string
}

// row is a header or a run of up to Columns items.
type row struct {
	header string
	items  []Item
}

// Grid is a virtualized, sectioned grid of items.
type Grid struct {
	*gtk.ScrolledWindow
	ctx   context.Context
	opts  Options
	list  *gtk.ListView
	model *gtk.StringList
	rows  []row
	// widgets maps a list item to the row widget it owns.
	widgets map[uintptr]*rowWidget
	// onActivate runs after any item is picked, so a popover can close.
	onActivate func()
	empty      *gtk.Label
	stack      *gtk.Stack
}

var gridCSS = lilcss.Applier("picker-grid", `
	.picker-grid listview,
	.picker-grid listview > row {
		background: none;
		padding: 0;
	}
	.picker-grid-header {
		font-weight: bold;
		font-size: {$font_micro};
		color: @lil_text_faint;
		padding: {$space_md} {$space_md} {$space_xs} {$space_md};
	}
	button.picker-grid-cell {
		padding: {$space_xs};
		border-radius: {$radius_md};
		background: none;
		min-width: 0;
		min-height: 0;
	}
	button.picker-grid-cell:hover,
	button.picker-grid-cell:focus-visible {
		background: @lil_hover;
	}
	.picker-grid-cell .onlineimage {
		background: transparent;
	}
	.picker-grid-emoji {
		font-size: 28px;
	}
	.picker-grid-empty {
		padding: {$space_xl};
		color: @lil_text_faint;
	}
`)

// New creates an empty grid.
func New(ctx context.Context, opts Options) *Grid {
	if opts.Columns < 1 {
		opts.Columns = 1
	}

	g := &Grid{
		ctx:     ctx,
		opts:    opts,
		model:   gtk.NewStringList(nil),
		widgets: make(map[uintptr]*rowWidget),
	}

	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *glib.Object) {
		item := obj.Cast().(*gtk.ListItem)
		// Focus goes to the cells, not the row, so arrow keys and Tab move
		// between items rather than stopping on each line.
		item.SetFocusable(false)
		item.SetActivatable(false)
		w := g.newRowWidget()
		item.SetChild(w.box)
		g.widgets[item.Native()] = w
	})
	factory.ConnectTeardown(func(obj *glib.Object) {
		item := obj.Cast().(*gtk.ListItem)
		item.SetChild(nil)
		delete(g.widgets, item.Native())
	})
	factory.ConnectBind(func(obj *glib.Object) {
		item := obj.Cast().(*gtk.ListItem)
		w := g.widgets[item.Native()]
		if w == nil {
			return
		}
		idx, err := strconv.Atoi(item.Item().Cast().(*gtk.StringObject).String())
		if err != nil || idx < 0 || idx >= len(g.rows) {
			return
		}
		w.bind(idx, g.rows[idx])
	})
	factory.ConnectUnbind(func(obj *glib.Object) {
		if w := g.widgets[obj.Cast().(*gtk.ListItem).Native()]; w != nil {
			w.index = -1
		}
	})

	g.list = gtk.NewListView(gtk.NewNoSelection(g.model), &factory.ListItemFactory)

	g.empty = gtk.NewLabel("")
	g.empty.AddCSSClass("picker-grid-empty")
	g.empty.SetWrap(true)
	g.empty.SetVAlign(gtk.AlignStart)

	g.stack = gtk.NewStack()
	g.stack.AddChild(g.list)
	g.stack.AddChild(g.empty)

	g.ScrolledWindow = gtk.NewScrolledWindow()
	g.ScrolledWindow.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	g.ScrolledWindow.SetVExpand(true)
	g.ScrolledWindow.SetChild(g.stack)
	if opts.Class != "" {
		g.ScrolledWindow.AddCSSClass(opts.Class)
	}
	gridCSS(g.ScrolledWindow)

	return g
}

// OnActivate sets a callback run after any item is picked.
func (g *Grid) OnActivate(f func()) { g.onActivate = f }

// SetSections replaces the grid's contents.
func (g *Grid) SetSections(sections []Section) {
	g.rows = g.rows[:0]
	for _, sec := range sections {
		if len(sec.Items) == 0 {
			continue
		}
		if sec.Title != "" {
			g.rows = append(g.rows, row{header: sec.Title})
		}
		for i := 0; i < len(sec.Items); i += g.opts.Columns {
			end := min(i+g.opts.Columns, len(sec.Items))
			g.rows = append(g.rows, row{items: sec.Items[i:end]})
		}
	}

	keys := make([]string, len(g.rows))
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}
	g.model.Splice(0, g.model.NItems(), keys)
	g.stack.SetVisibleChild(g.list)

	if len(g.rows) > 0 {
		g.list.ScrollTo(0, gtk.ListScrollNone, nil)
	}
}

// SetMessage replaces the grid with a line of text, for loading, empty and
// error states.
func (g *Grid) SetMessage(text string) {
	g.rows = g.rows[:0]
	g.model.Splice(0, g.model.NItems(), nil)
	g.empty.SetText(text)
	g.stack.SetVisibleChild(g.empty)
}

// ActivateFirst picks the first item, for Enter in a search entry. It
// reports whether there was one.
func (g *Grid) ActivateFirst() bool {
	for _, r := range g.rows {
		if len(r.items) > 0 {
			g.activate(r.items[0])
			return true
		}
	}
	return false
}

// FocusFirst moves keyboard focus to the first cell, for Down from a search
// entry. From there the arrow keys move between cells.
func (g *Grid) FocusFirst() bool {
	first := -1
	for i, r := range g.rows {
		if len(r.items) > 0 {
			first = i
			break
		}
	}
	if first < 0 {
		return false
	}
	g.list.ScrollTo(uint(first), gtk.ListScrollNone, nil)
	for _, w := range g.widgets {
		if w.index == first {
			return w.cells[0].button.GrabFocus()
		}
	}
	return false
}

func (g *Grid) activate(it Item) {
	if it.Activate != nil {
		it.Activate()
	}
	if g.onActivate != nil {
		g.onActivate()
	}
}

// rowWidget is a recycled row: a header label and Columns cells, of which
// bind shows the ones it needs.
type rowWidget struct {
	box    *gtk.Box
	header *gtk.Label
	cells  []*cell
	index  int // row index currently bound, -1 when unbound
}

type cell struct {
	button  *gtk.Button
	picture *onlineimage.Picture
	text    *gtk.Label
	item    Item
}

func (g *Grid) newRowWidget() *rowWidget {
	w := &rowWidget{index: -1}

	w.header = gtk.NewLabel("")
	w.header.AddCSSClass("picker-grid-header")
	w.header.SetXAlign(0)

	cells := gtk.NewBox(gtk.OrientationHorizontal, 0)
	cells.SetHomogeneous(true)

	for i := 0; i < g.opts.Columns; i++ {
		c := &cell{}

		c.picture = onlineimage.NewPicture(g.ctx, imgutil.HTTPProvider)
		c.picture.SetSizeRequest(g.opts.CellSize, g.opts.CellSize)
		c.picture.SetContentFit(gtk.ContentFitContain)
		if g.opts.AnimateOnHover {
			c.picture.EnableAnimation().OnHover()
		}

		c.text = gtk.NewLabel("")
		c.text.AddCSSClass("picker-grid-emoji")
		c.text.SetSizeRequest(g.opts.CellSize, g.opts.CellSize)

		content := gtk.NewBox(gtk.OrientationVertical, 0)
		content.Append(c.picture)
		content.Append(c.text)

		c.button = gtk.NewButton()
		c.button.AddCSSClass("picker-grid-cell")
		c.button.SetChild(content)
		c.button.ConnectClicked(func() { g.activate(c.item) })

		cells.Append(c.button)
		w.cells = append(w.cells, c)
	}

	w.box = gtk.NewBox(gtk.OrientationVertical, 0)
	w.box.Append(w.header)
	w.box.Append(cells)
	return w
}

func (w *rowWidget) bind(index int, r row) {
	w.index = index
	w.header.SetVisible(r.header != "")
	w.header.SetText(r.header)

	for i, c := range w.cells {
		if i >= len(r.items) {
			// Keep the slot so a short last line stays on the grid, but
			// take it out of focus and the accessibility tree.
			c.item = Item{}
			c.picture.SetVisible(false)
			c.text.SetVisible(true)
			c.text.SetText("")
			c.button.SetOpacity(0)
			c.button.SetSensitive(false)
			c.button.SetCanTarget(false)
			c.button.SetVisible(len(r.items) > 0)
			continue
		}

		it := r.items[i]
		c.item = it
		c.button.SetVisible(true)
		c.button.SetOpacity(1)
		c.button.SetSensitive(true)
		c.button.SetCanTarget(true)

		tooltip := it.Label
		if it.Detail != "" {
			tooltip += "\n" + it.Detail
		}
		c.button.SetTooltipText(tooltip)
		c.button.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
			[]glib.Value{*glib.NewValue(it.Label)})

		if it.ImageURL != "" {
			c.text.SetVisible(false)
			c.picture.SetVisible(true)
			c.picture.SetURL(it.ImageURL)
		} else {
			c.picture.SetVisible(false)
			c.text.SetVisible(true)
			c.text.SetText(it.Text)
		}
	}
}
