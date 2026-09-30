// Package mediaqueue serializes the creation of GStreamer-backed media
// pipelines (GtkMediaFile, via GtkVideo, GtkMediaControls, or a raw
// GtkMediaFile playing a GIFV/GIF-as-video embed).
//
// GTK4's built-in GStreamer media backend is built on playbin3, which uses
// decodebin3. decodebin3 has a known upstream bug where more than one
// instance going through its initial stream setup in the same process at
// once can corrupt shared state and abort the whole process — not a Go
// panic, not a GError, nothing a defer/recover or an error check can catch.
// The crash this fixed showed up as:
//
//	gstdecodebin3.c:...:mq_slot_handle_stream_start: assertion failed: (collection)
//
// Anything in LilDisc that constructs a GtkMediaFile — inline audio
// players, and autoplaying GIFV/video embeds, both of which can have
// several instances become visible or start loading at the same moment —
// runs its pipeline construction through Run so that two are never
// mid-setup together.
package mediaqueue

import (
	"sync"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

var (
	mu      sync.Mutex
	pending []func(done func())
	busy    bool
)

// Run queues task to build its media pipeline once every earlier-queued
// task has finished — reported by task calling done(), which it must do
// exactly once, however it turns out (the stream became playable, it
// failed, or a caller-side timeout gave up waiting). Run itself returns
// immediately; task runs later on the main loop, never synchronously
// within the call to Run.
func Run(task func(done func())) {
	mu.Lock()
	pending = append(pending, task)
	start := !busy
	if start {
		busy = true
	}
	mu.Unlock()

	if start {
		glib.IdleAdd(runNext)
	}
}

func runNext() {
	mu.Lock()
	if len(pending) == 0 {
		busy = false
		mu.Unlock()
		return
	}
	next := pending[0]
	pending = pending[1:]
	mu.Unlock()

	next(func() { glib.IdleAdd(runNext) })
}
