package mods

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func withRandomFilenames(t *testing.T, on bool) {
	t.Helper()
	was := enableRandomFilenames.Value()
	enableRandomFilenames.Publish(on)
	t.Cleanup(func() { enableRandomFilenames.Publish(was) })
}

func TestRandomizeFilenameDisabledIsPassThrough(t *testing.T) {
	withRandomFilenames(t, false)

	const name = "tax return 2025 jane doe.pdf"
	if got := RandomizeFilename(name); got != name {
		t.Errorf("RandomizeFilename(%q) = %q, want it untouched", name, got)
	}
}

// The original name is the thing being hidden, so none of it may survive.
func TestRandomizeFilenameDropsTheOriginal(t *testing.T) {
	withRandomFilenames(t, true)

	const secret = "jane-doe-passport-scan"
	got := RandomizeFilename(secret + ".jpg")
	if strings.Contains(strings.ToLower(got), "jane") ||
		strings.Contains(strings.ToLower(got), "passport") {
		t.Errorf("RandomizeFilename leaked the original name: %q", got)
	}
}

func TestRandomizeFilenamePreservesExtension(t *testing.T) {
	withRandomFilenames(t, true)

	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp4", ".pdf", ".txt"} {
		got := RandomizeFilename("original" + ext)
		if filepath.Ext(got) != ext {
			t.Errorf("RandomizeFilename(original%s) = %q, lost the extension", ext, got)
		}
	}
}

func TestRandomizeFilenameHandlesNoExtension(t *testing.T) {
	withRandomFilenames(t, true)

	got := RandomizeFilename("README")
	if got == "" {
		t.Fatal("RandomizeFilename returned empty")
	}
	if strings.Contains(got, "README") {
		t.Errorf("RandomizeFilename(README) = %q, leaked the original", got)
	}
}

// Discord reads the SPOILER_ prefix off the attachment name, so losing it
// would silently un-spoiler the upload.
func TestRandomizeFilenameKeepsSpoilerPrefix(t *testing.T) {
	withRandomFilenames(t, true)

	got := RandomizeFilename("SPOILER_holiday.png")
	if !strings.HasPrefix(got, "SPOILER_") {
		t.Errorf("RandomizeFilename = %q, lost the SPOILER_ prefix", got)
	}
	if filepath.Ext(got) != ".png" {
		t.Errorf("RandomizeFilename = %q, lost the extension", got)
	}
}

// The whole point of the rewrite: names must look like a device wrote them,
// not like sixteen characters out of a random number generator.
func TestPlausibleStemShapes(t *testing.T) {
	tests := []struct {
		ext     string
		pattern string
	}{
		{".png", `^Screenshot_\d{8}_\d{6}$`},
		{".PNG", `^Screenshot_\d{8}_\d{6}$`},
		{".jpg", `^IMG_\d{4}$`},
		{".jpeg", `^IMG_\d{4}$`},
		{".heic", `^IMG_\d{4}$`},
		{".mp4", `^VID_\d{8}_\d{6}$`},
		{".mov", `^VID_\d{8}_\d{6}$`},
		{".gif", `^image_\d{4}$`},
		{".webp", `^image_\d{4}$`},
		{".mp3", `^audio_\d{4}$`},
		{".pdf", `^file_\d{4}$`},
		{"", `^file_\d{4}$`},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			re := regexp.MustCompile(tt.pattern)
			// Run it a few times: every branch draws random numbers, and a
			// malformed clock component only shows up for some draws.
			for range 50 {
				got := plausibleStem(tt.ext)
				if !re.MatchString(got) {
					t.Fatalf("plausibleStem(%q) = %q, want %s", tt.ext, got, tt.pattern)
				}
			}
		})
	}
}

// A screenshot name carries a wall clock, so the hour, minute and second have
// to be values a clock can actually show.
func TestScreenshotTimeIsAValidClock(t *testing.T) {
	re := regexp.MustCompile(`^Screenshot_\d{8}_(\d{2})(\d{2})(\d{2})$`)

	for range 500 {
		got := plausibleStem(".png")
		m := re.FindStringSubmatch(got)
		if m == nil {
			t.Fatalf("plausibleStem(.png) = %q, unexpected shape", got)
		}
		h, min, sec := m[1], m[2], m[3]
		if h > "23" {
			t.Fatalf("%q has hour %s", got, h)
		}
		if min > "59" || sec > "59" {
			t.Fatalf("%q has minute %s second %s", got, min, sec)
		}
	}
}

// Two uploads in a row must not collide, or a batch of images all arrives
// under one name.
func TestPlausibleStemVaries(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		seen[plausibleStem(".jpg")] = true
	}
	if len(seen) < 10 {
		t.Errorf("only %d distinct names out of 100 draws", len(seen))
	}
}

func TestRandIntStaysInRange(t *testing.T) {
	for range 1000 {
		if v := randInt(10); v < 0 || v >= 10 {
			t.Fatalf("randInt(10) = %d, out of range", v)
		}
	}
}
