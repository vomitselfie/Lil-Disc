package mods

import (
	"context"
	"strings"
	"sync"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app/prefs"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableInlineAudio = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Inline Audio Player",
	Section:     "Mods",
	Description: "Play voice messages and other audio attachments inline with a play/seek control instead of showing a download link. Applies to messages shown afterwards.",
})

var _ = lilcss.WriteCSS(`
	.mod-audio-player {
		margin-top: 4px;
		padding: 4px 6px 6px 6px;
		border-radius: {$radius_lg};
		background-color: @lil_surface_raised;
	}
	.mod-audio-player .mod-audio-name {
		font-size: 0.9em;
		padding: 2px 4px 0 4px;
		opacity: 0.85;
	}
	.mod-audio-voice {
		border-left: 3px solid @lil_accent;
	}
`)

// IsAudioAttachment reports whether an attachment is an audio file
// that can be played inline. Uses ContentType when present, falling
// back to filename extension for older messages that omit it.
func IsAudioAttachment(a *discord.Attachment) bool {
	if strings.HasPrefix(a.ContentType, "audio/") {
		return true
	}
	if a.ContentType != "" {
		return false
	}
	lower := strings.ToLower(a.Filename)
	for _, ext := range []string{".ogg", ".opus", ".mp3", ".wav", ".flac", ".m4a", ".aac"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// NewAudioPlayer builds an inline audio player for the attachment.
// Returns nil when the mod is disabled so callers can fall back to
// the default link rendering.
//
// The player streams via GStreamer's souphttpsrc through
// gio.NewFileForURI — no local download is needed. Discord's media
// proxy (media.discordapp.net) is preferred when present because it
// supports Range requests more reliably than the raw CDN.
//
// Building the GtkMediaFile itself is what starts its GStreamer pipeline,
// so that part is deferred through queueAudioPipeline rather than done
// synchronously here; see its doc comment for why. A spinner placeholder
// fills in for the controls until this player's turn comes up, which is
// normally within a frame or two.
func NewAudioPlayer(ctx context.Context, a *discord.Attachment) gtk.Widgetter {
	if !enableInlineAudio.Value() {
		return nil
	}

	uri := string(a.Proxy)
	if uri == "" {
		uri = string(a.URL)
	}
	if uri == "" {
		return nil
	}

	// Discord voice messages always arrive as voice-message.ogg —
	// swap the verbatim filename for a nicer label.
	isVoice := strings.HasPrefix(a.Filename, "voice-message")
	displayName := a.Filename
	if isVoice {
		displayName = "Voice message"
	}

	header := gtk.NewLabel(displayName)
	header.AddCSSClass("mod-audio-name")
	header.SetXAlign(0)
	header.SetEllipsize(pango.EllipsizeEnd)
	header.SetTooltipText(a.Filename)

	placeholder := gtk.NewSpinner()
	placeholder.AddCSSClass("mod-audio-placeholder")
	placeholder.SetSizeRequest(20, 20)
	placeholder.SetHAlign(gtk.AlignStart)
	placeholder.SetMarginTop(4)
	placeholder.SetMarginBottom(4)
	placeholder.Start()

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("mod-audio-player")
	if isVoice {
		box.AddCSSClass("mod-audio-voice")
	}
	box.SetHAlign(gtk.AlignStart)
	box.Append(header)
	box.Append(placeholder)

	queueAudioPipeline(func(done func()) {
		// The player may have been destroyed (message deleted, scrolled
		// out and pruned) while it was waiting its turn in the queue.
		if box.Parent() == nil && !box.Mapped() && !box.Realized() {
			done()
			return
		}

		file := gio.NewFileForURI(uri)
		media := gtk.NewMediaFileForFile(file)

		controls := gtk.NewMediaControls(media)
		controls.SetHExpand(true)

		box.Remove(placeholder)
		box.Append(controls)

		// Releasing the MediaFile on destroy stops its GStreamer
		// pipeline and releases the associated PipeWire node. Without
		// this each voice message leaks a node until app exit — same
		// pattern chatkit uses for video embeds.
		mediaRef := coreglib.NewWeakRef(media)
		box.ConnectDestroy(func() {
			if m := mediaRef.Get(); m != nil {
				m.Pause()
				m.Clear()
			}
		})

		// Let the next queued player start its own pipeline once this
		// one has finished initializing, one way or the other, rather
		// than on a fixed delay — a slow fetch shouldn't hold up
		// players whose files are already cached, but nothing here
		// should count as "started" until this pipeline is actually
		// past the stage that crashes.
		var once sync.Once
		finish := func() { once.Do(done) }
		media.NotifyProperty("prepared", func() {
			if media.IsPrepared() {
				finish()
			}
		})
		media.NotifyProperty("error", func() {
			if media.Error() != nil {
				finish()
			}
		})
		// Safety net: if a file is slow or the above never fires for
		// some other reason, don't stall every audio player after it.
		glib.TimeoutSecondsAdd(5, func() { finish() })
	})

	return box
}

// audioPipelineQueue serializes GStreamer pipeline creation for inline
// audio players.
//
// GTK4's built-in GStreamer media backend (what GtkMediaFile uses) is
// built on playbin3/decodebin3. decodebin3 has a known crash when more
// than one instance is going through its initial stream setup in the
// same process at once — upstream, the assertion is
// "mq_slot_handle_stream_start: assertion failed: (collection)" in
// gstdecodebin3.c. It aborts the whole process outright: not a Go panic,
// not a GError, nothing a defer/recover or an error check can catch.
//
// Opening a DM or channel with more than one audio attachment used to
// build every GtkMediaFile synchronously in the same render pass, so
// their pipelines all started initializing at once — exactly that
// trigger. Building them one at a time, only starting the next once the
// previous has finished initializing (or errored, or timed out), keeps
// two pipelines from ever being mid-setup together.
var audioPipelineQueue struct {
	mu      sync.Mutex
	pending []func(done func())
	busy    bool
}

func queueAudioPipeline(start func(done func())) {
	audioPipelineQueue.mu.Lock()
	defer audioPipelineQueue.mu.Unlock()

	audioPipelineQueue.pending = append(audioPipelineQueue.pending, start)
	if !audioPipelineQueue.busy {
		audioPipelineQueue.busy = true
		glib.IdleAdd(runNextAudioPipeline)
	}
}

func runNextAudioPipeline() {
	audioPipelineQueue.mu.Lock()
	if len(audioPipelineQueue.pending) == 0 {
		audioPipelineQueue.busy = false
		audioPipelineQueue.mu.Unlock()
		return
	}
	next := audioPipelineQueue.pending[0]
	audioPipelineQueue.pending = audioPipelineQueue.pending[1:]
	audioPipelineQueue.mu.Unlock()

	next(func() { glib.IdleAdd(runNextAudioPipeline) })
}
