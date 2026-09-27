package gtkcord

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Every lil_* colour the base layer reads must be defined by both palettes,
// and by the fallback used when the theme is off; an undefined colour makes
// GTK drop the whole declaration.
func TestPalettesDefineEveryColourTheBaseUses(t *testing.T) {
	used := map[string]bool{}
	for _, m := range regexp.MustCompile(`@(lil_[a-z_]+)`).FindAllStringSubmatch(baseCSS, -1) {
		used[m[1]] = true
	}
	if len(used) == 0 {
		t.Fatal("found no lil_* colours in baseCSS")
	}

	for name, p := range map[string]Palette{"dark": DarkPalette, "light": LightPalette} {
		css := p.CSS()
		for c := range used {
			if !strings.Contains(css, "@define-color "+c+" ") {
				t.Errorf("%s palette does not define @%s", name, c)
			}
		}
	}
}

func TestPalettesAreComplete(t *testing.T) {
	for name, p := range map[string]Palette{"dark": DarkPalette, "light": LightPalette} {
		v := reflect.ValueOf(p)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).String() == "" {
				t.Errorf("%s palette leaves %s empty", name, v.Type().Field(i).Name)
			}
		}
	}
}
