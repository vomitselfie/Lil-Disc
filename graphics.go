package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotkit/app/prefs"
)

var preferIntegratedGPU = prefs.NewBool(false, prefs.PropMeta{
	Name:    "Prefer Integrated GPU",
	Section: "Graphics",
	Description: "On a machine with a discrete or external GPU, keep rendering " +
		"and video decoding on the integrated GPU that drives the display. " +
		"Stops an external card spinning up just to draw a chat window. " +
		"Takes effect after restarting LilDisc.",
})

// applyGraphicsPrefs turns the graphics preferences into environment variables,
// before GTK has started.
//
// These cannot be read through the normal preferences API at this point:
// prefs are loaded as part of application startup, whereas GSK_RENDERER is
// consumed when the renderer is created and GST_PLUGIN_FEATURE_RANK as a
// pipeline is built. So the saved value is read straight out of prefs.json,
// which is the same file the preferences window writes. The cost is that the
// setting applies at the next launch rather than immediately, which is why its
// description says so.
//
// Anything already in the environment is left alone, so both a shell override
// and ~/.config/lildisc/env still win over the toggle.
func applyGraphicsPrefs() {
	if !savedBoolPref(preferIntegratedGPU.ID(), false) {
		return
	}

	// The GL renderer follows the device driving the display, whereas GTK's
	// Vulkan renderer picks the "best" device it can find and considers a
	// discrete GPU best even when it is an external card attached to no
	// monitor.
	setEnvDefault("GSK_RENDERER", "gl")
	// Decode on the integrated GPU rather than NVDEC. Not "no": disabling
	// hardware decoding altogether would just move the cost to the CPU.
	setEnvDefault("LILDISC_MPV_HWDEC", "vaapi")
	// Demote GStreamer's NVIDIA decoders so autoplugging falls through to a
	// VA-API or software decoder for video and GIFV embeds.
	setEnvDefault("GST_PLUGIN_FEATURE_RANK",
		"nvh264dec:0,nvh265dec:0,nvav1dec:0,nvvp8dec:0,nvvp9dec:0,nvmpeg2videodec:0")
}

func setEnvDefault(key, value string) {
	if _, taken := os.LookupEnv(key); taken {
		return
	}
	if err := os.Setenv(key, value); err != nil {
		log.Printf("cannot set %s: %v", key, err)
	}
}

// savedBoolPref reads one boolean out of the saved preferences file. It
// returns fallback if the file, the key, or the value is missing or unusable —
// a graphics tweak is never worth failing to start over.
func savedBoolPref(id prefs.ID, fallback bool) bool {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fallback
	}

	b, err := os.ReadFile(filepath.Join(configDir, "lildisc", "prefs.json"))
	if err != nil {
		return fallback
	}

	var saved map[string]json.RawMessage
	if err := json.Unmarshal(b, &saved); err != nil {
		return fallback
	}

	raw, ok := saved[string(id)]
	if !ok {
		return fallback
	}

	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	return value
}
