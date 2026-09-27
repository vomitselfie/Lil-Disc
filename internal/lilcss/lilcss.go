// Package lilcss wraps gotkit's cssutil so that LilDisc keeps its own copy of
// every component stylesheet.
//
// cssutil installs component CSS at APPLICATION priority, which sits below
// USER priority, where GTK loads ~/.config/gtk-4.0/gtk.css. When that file is
// a full theme (as on Manjaro Sway, which copies Matcha there) it outranks
// both libadwaita and every rule in this app. LilDisc's own theme therefore
// has to install above USER, and once its widget base layer is up there, the
// component rules have to be installed above it again or the base layer
// would flatten them. Keeping the raw text here is what makes that second
// installation possible; cssutil does not expose its buffer.
package lilcss

import (
	"log"
	"strings"
	"text/template"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
)

var (
	components strings.Builder
	variables  = template.FuncMap{}
)

// WriteCSS is cssutil.WriteCSS, recording the stylesheet as well.
func WriteCSS(css string) struct{} {
	components.WriteString(css)
	return cssutil.WriteCSS(css)
}

// Applier is cssutil.Applier, recording the stylesheet as well.
func Applier(class, css string) func(gtk.Widgetter) {
	components.WriteString(css)
	return cssutil.Applier(class, css)
}

// AddCSSVariables is cssutil.AddCSSVariables, recording the variables as
// well so Render can expand the same {$name} templates.
func AddCSSVariables(vars map[string]string) {
	for k, v := range vars {
		v := v
		variables[k] = func() string { return v }
	}
	cssutil.AddCSSVariables(vars)
}

// Render expands {$name} variables in css the same way cssutil does.
func Render(name, css string) string {
	t, err := template.New(name).Delims("{$", "}").Funcs(variables).Parse(css)
	if err != nil {
		log.Panicf("cannot parse CSS template %s: %v", name, err)
	}
	var out strings.Builder
	if err := t.Execute(&out, nil); err != nil {
		log.Panicf("cannot render CSS template %s: %v", name, err)
	}
	return out.String()
}

// Components returns every component stylesheet written so far, rendered.
func Components() string {
	return Render("components", components.String())
}
