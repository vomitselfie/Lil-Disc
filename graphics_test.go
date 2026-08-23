package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writePrefs writes a prefs.json containing the "Prefer Integrated GPU" toggle
// under the same key the preferences window would use, derived from the
// preference itself rather than hardcoded.
func writePrefs(t *testing.T, value string) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := os.MkdirAll(filepath.Join(dir, "lildisc"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := `{"` + string(preferIntegratedGPU.ID()) + `": ` + value + `}`
	path := filepath.Join(dir, "lildisc", "prefs.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func clearGraphicsEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GSK_RENDERER", "LILDISC_MPV_HWDEC", "GST_PLUGIN_FEATURE_RANK"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// TestApplyGraphicsPrefs covers the whole path the toggle actually takes:
// preferences window writes prefs.json, startup reads it back before GTK
// exists. Worth testing end to end because a mismatch between the key written
// and the key read produces no error at all — the toggle simply does nothing,
// which is not something anyone would notice until they wondered why their GPU
// was still busy.
func TestApplyGraphicsPrefs(t *testing.T) {
	clearGraphicsEnv(t)
	writePrefs(t, "true")

	applyGraphicsPrefs()

	if got := os.Getenv("GSK_RENDERER"); got != "gl" {
		t.Errorf("GSK_RENDERER = %q, want %q", got, "gl")
	}
	if got := os.Getenv("LILDISC_MPV_HWDEC"); got != "vaapi" {
		t.Errorf("LILDISC_MPV_HWDEC = %q, want %q", got, "vaapi")
	}
	if os.Getenv("GST_PLUGIN_FEATURE_RANK") == "" {
		t.Error("GST_PLUGIN_FEATURE_RANK was not set")
	}
}

func TestApplyGraphicsPrefsDisabled(t *testing.T) {
	clearGraphicsEnv(t)
	writePrefs(t, "false")

	applyGraphicsPrefs()

	if got := os.Getenv("GSK_RENDERER"); got != "" {
		t.Errorf("GSK_RENDERER = %q, want it left unset", got)
	}
}

// An explicit override, from a shell or from ~/.config/lildisc/env, has to beat
// the toggle rather than be silently replaced by it.
func TestApplyGraphicsPrefsKeepsExistingEnv(t *testing.T) {
	clearGraphicsEnv(t)
	writePrefs(t, "true")
	t.Setenv("GSK_RENDERER", "vulkan")

	applyGraphicsPrefs()

	if got := os.Getenv("GSK_RENDERER"); got != "vulkan" {
		t.Errorf("GSK_RENDERER = %q, want the pre-existing %q", got, "vulkan")
	}
}

func TestApplyGraphicsPrefsNoFile(t *testing.T) {
	clearGraphicsEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	applyGraphicsPrefs() // must not panic when prefs.json does not exist

	if got := os.Getenv("GSK_RENDERER"); got != "" {
		t.Errorf("GSK_RENDERER = %q, want it left unset", got)
	}
}
