package main

import (
	"bytes"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
)

// TestGlobalCSSParses renders the entire application stylesheet — every
// cssutil.WriteCSS and cssutil.Applier block in every package main pulls in —
// and fails if GTK reports a parse error in any of it.
//
// Worth having because the stylesheets are Go string literals assembled at
// init time and only parsed once the app has a display. A typo in one of them
// does not fail the build, does not fail any other test, and at runtime only
// disables the offending rule after printing to the log, so it is very easy to
// ship a broken style and never notice.
//
// Needs a display. In a headless environment run it under a nested
// compositor:
//
//	WLR_BACKENDS=headless WLR_LIBINPUT_NO_DEVICES=1 \
//		cage -- go test -run TestGlobalCSSParses .
func TestGlobalCSSParses(t *testing.T) {
	if os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") == "" {
		t.Skip("no display available; see the doc comment for how to run this")
	}

	// GTK is not safe to call from an arbitrary goroutine.
	runtime.LockOSThread()
	gtk.Init()

	// gotkit reports parse errors through the standard logger as well as
	// slog, so capturing the former is enough to detect them.
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	cssutil.ApplyGlobalCSS()

	if out := logged.String(); strings.Contains(out, "CSS error") {
		t.Fatalf("global stylesheet failed to parse:\n%s", out)
	}
}

// gtkNamedColors are the colours GTK and libadwaita define for us. Anything
// else referenced as @name has to be defined by this project.
var gtkNamedColors = map[string]bool{
	// Legacy GTK names, still aliased by libadwaita.
	"theme_bg_color": true, "theme_fg_color": true,
	"theme_base_color": true, "theme_text_color": true,
	"theme_selected_bg_color": true, "theme_selected_fg_color": true,
	"theme_unfocused_bg_color": true, "theme_unfocused_fg_color": true,
	"insensitive_fg_color": true, "insensitive_bg_color": true,
	"borders": true, "unfocused_borders": true,
	// libadwaita named palette.
	"window_bg_color": true, "window_fg_color": true,
	"view_bg_color": true, "view_fg_color": true,
	"headerbar_bg_color": true, "headerbar_fg_color": true,
	"sidebar_bg_color": true, "sidebar_fg_color": true,
	"popover_bg_color": true, "popover_fg_color": true,
	"dialog_bg_color": true, "dialog_fg_color": true,
	"card_bg_color": true, "card_fg_color": true,
	"accent_color": true, "accent_bg_color": true, "accent_fg_color": true,
	"destructive_color": true, "destructive_bg_color": true,
	"success_color": true, "warning_color": true, "error_color": true,
	"dim_label": true,
}

// cssAtRules are at-rule keywords, not colour references.
var cssAtRules = map[string]bool{
	"define-color": true, "keyframes": true, "media": true,
	"import": true, "supports": true, "charset": true, "namespace": true,
}

var (
	defineColorRe = regexp.MustCompile(`@define-color\s+([A-Za-z_][\w-]*)`)
	// A colour reference appears where a CSS value can: at the start of a
	// line, or after whitespace, an opening paren, a comma or a colon. The
	// leading context matters — without it this also matches things like the
	// "@me" in Discord's /users/@me REST paths.
	colorRefRe = regexp.MustCompile(`(?:^|[\s(,:])@([A-Za-z_][\w-]*)`)
)

// TestCSSColorsAreDefined checks that every @name referenced in the project's
// stylesheets is either defined by us via @define-color or provided by
// GTK/libadwaita.
//
// This exists because TestGlobalCSSParses does not cover it: GTK reports CSS
// syntax errors through the parsing-error signal, but an *undefined* colour
// reference is resolved silently, so a typo in a colour name produces neither
// a parse error nor any visible complaint — the property simply does not
// apply. Verified by injecting a bogus reference and watching the parse test
// still pass.
//
// Runs without a display, unlike the parse test.
func TestCSSColorsAreDefined(t *testing.T) {
	defined := map[string]bool{}
	type ref struct {
		name string
		file string
	}
	var refs []ref

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "nix" || name == "po" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		for _, m := range defineColorRe.FindAllSubmatch(src, -1) {
			defined[string(m[1])] = true
		}
		for _, line := range strings.Split(string(src), "\n") {
			// Skip Go comments, which may legitimately mention @something.
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, m := range colorRefRe.FindAllStringSubmatch(line, -1) {
				if cssAtRules[m[1]] {
					continue
				}
				refs = append(refs, ref{name: m[1], file: path})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking sources: %v", err)
	}

	if len(defined) == 0 || len(refs) == 0 {
		t.Fatalf("found %d definitions and %d references; the scan is broken",
			len(defined), len(refs))
	}

	for _, r := range refs {
		if !defined[r.name] && !gtkNamedColors[r.name] {
			t.Errorf("%s: @%s is referenced but never defined", r.file, r.name)
		}
	}
}
