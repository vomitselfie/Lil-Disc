package mods

import (
	"log/slog"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
)

var enableAutoIdle = prefs.NewBool(true, prefs.PropMeta{
	Name:    "Auto Idle",
	Section: "Mods",
	Description: "Go idle after a period without input, and back online when " +
		"you return. Discord only sends push notifications to your phone " +
		"while you are idle or offline elsewhere, so without this an open " +
		"LilDisc window suppresses mobile notifications indefinitely.",
})

var autoIdleMinutes = prefs.NewInt(10, prefs.IntMeta{
	Name:        "Auto Idle Timeout",
	Section:     "Mods",
	Description: "Minutes without input before going idle.",
	Min:         1,
	Max:         180,
})

// autoIdleTick is how often the idle timer is checked. Coarse on purpose: the
// timeout is measured in minutes, so there is nothing to gain from waking up
// more often than this.
const autoIdleTick = 30 // seconds

// autoIdle is the state of the idle timer. Everything here is touched only
// from the GTK main thread.
var autoIdle struct {
	lastActivity time.Time

	// chosen is the status the user actually picked, which auto-idle returns
	// to when they come back. Tracked separately from the presence store so a
	// manually chosen Do Not Disturb or Invisible is never overwritten: idle
	// is only ever entered from Online.
	chosen discord.Status

	// entered records that this mod set the current Idle status, so activity
	// only undoes an idle we caused rather than one the user asked for.
	entered bool

	// setUp guards against a second Hook on the same window stacking another
	// timer and another set of event controllers on top of the first.
	setUp bool
}

// NotifyStatusChosen records a status the user selected themselves. Called by
// the window's status actions.
func NotifyStatusChosen(status discord.Status) {
	autoIdle.chosen = status
	autoIdle.entered = false
	autoIdle.lastActivity = time.Now()
}

// SetupAutoIdle watches for input on the window and moves the account between
// Online and Idle.
//
// Discord decides where to deliver a notification when the message arrives,
// based on where you appear to be, and hands off to mobile once you are idle
// or offline everywhere else. The official client reaches that state through
// system-wide idle detection; LilDisc previously never reported idle at all,
// so leaving the window open told Discord you were at your desk indefinitely
// and mobile pushes were withheld the whole time.
//
// Input is only observed within this window, so this idles somewhat more
// eagerly than the official client, which watches the whole desktop. That
// errs in the useful direction: working in another app for ten minutes means
// you are not reading Discord here, and would rather your phone told you.
func SetupAutoIdle(state *gtkcord.State, win gtk.Widgetter) {
	if autoIdle.setUp {
		return
	}
	autoIdle.setUp = true

	base := gtk.BaseWidget(win)

	autoIdle.lastActivity = time.Now()
	autoIdle.chosen = discord.OnlineStatus
	autoIdle.entered = false

	// Seed from whatever the account is actually set to, so starting up while
	// Do Not Disturb does not get quietly reset to Online on first activity.
	//
	// Offline is taken verbatim rather than ignored: that is how an Invisible
	// account reports itself, and idling it would announce a status to
	// everyone else and undo the invisibility. Since only Online is ever
	// idled, recording it as-is is what keeps that from happening.
	if me, _ := state.Me(); me != nil {
		if presence, _ := state.PresenceStore.Presence(0, me.ID); presence != nil {
			if presence.Status != "" {
				autoIdle.chosen = presence.Status
			}
		}
	}

	setStatus := func(status discord.Status) {
		online := state.Online()
		go func() {
			if err := online.SetStatus(status, nil); err != nil {
				slog.Warn("auto idle: cannot set status", "status", status, "err", err)
			}
		}()
	}

	markActive := func() {
		autoIdle.lastActivity = time.Now()
		if !autoIdle.entered {
			return
		}
		autoIdle.entered = false
		slog.Debug("auto idle: activity, returning to", "status", autoIdle.chosen)
		setStatus(autoIdle.chosen)
	}

	// Capture phase so these see events regardless of which widget handles
	// them, and none of the handlers claim the event.
	motion := gtk.NewEventControllerMotion()
	motion.SetPropagationPhase(gtk.PhaseCapture)
	motion.ConnectMotion(func(_, _ float64) { markActive() })
	base.AddController(motion)

	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(_, _ uint, _ gdk.ModifierType) bool {
		markActive()
		return false
	})
	base.AddController(keys)

	scroll := gtk.NewEventControllerScroll(gtk.EventControllerScrollBothAxes)
	scroll.SetPropagationPhase(gtk.PhaseCapture)
	scroll.ConnectScroll(func(_, _ float64) bool {
		markActive()
		return false
	})
	base.AddController(scroll)

	timer := glib.TimeoutSecondsAdd(autoIdleTick, func() bool {
		if !enableAutoIdle.Value() {
			// Toggled off while already idle: undo it rather than stranding
			// the account there.
			if autoIdle.entered {
				autoIdle.entered = false
				setStatus(autoIdle.chosen)
			}
			return true
		}

		if autoIdle.entered || autoIdle.chosen != discord.OnlineStatus {
			return true
		}

		timeout := time.Duration(autoIdleMinutes.Value()) * time.Minute
		if time.Since(autoIdle.lastActivity) < timeout {
			return true
		}

		autoIdle.entered = true
		slog.Debug("auto idle: no input, going idle", "after", timeout)
		setStatus(discord.IdleStatus)
		return true
	})

	base.ConnectDestroy(func() { glib.SourceRemove(timer) })
}
