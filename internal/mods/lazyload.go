package mods

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
)

var enableLazyLoading = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Lazy Load Embeds",
	Section:     "Mods",
	Description: "Defer loading embed images until they scroll into view. Reduces bandwidth and speeds up channel switches.",
})

// LazyLoad defers calling loadFn until the widget is mapped (visible in the
// window). If the widget is already mapped or lazy loading is disabled, loadFn
// runs immediately.
//
// Use this to wrap embed SetFromURL calls so off-screen images don't start
// HTTP fetches until the user scrolls to them.
func LazyLoad(w gtk.Widgetter, loadFn func()) {
	if !enableLazyLoading.Value() {
		loadFn()
		return
	}

	base := gtk.BaseWidget(w)
	if base.Mapped() {
		loadFn()
		return
	}

	var loaded bool
	base.ConnectMap(func() {
		if !loaded {
			loaded = true
			loadFn()
		}
	})
}
