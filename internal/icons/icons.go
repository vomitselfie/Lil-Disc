// Package icons generates and loads icon Gresource.
package icons

import (
	_ "embed"
	"log"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

//go:generate glib-compile-resources lildisc.gresource.xml
//go:generate ./windows/generate-icon.sh

//go:embed lildisc.gresource
var Resources []byte

// LogoPNG is the raw bytes of lildisc_logo.png. Exposed so runtime consumers
// like the SNI tray icon can decode it to pixel data, independent of the
// gresource bundle. Keep in sync with the root lildisc_logo.png used for
// README/docs.
//
//go:embed lildisc_logo.png
var LogoPNG []byte

func init() {
	resources, err := gio.NewResourceFromData(glib.NewBytesWithGo(Resources))
	if err != nil {
		log.Panicln("Failed to create resources: ", err)
	}
	gio.ResourcesRegister(resources)
}
