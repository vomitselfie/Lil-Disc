package mods

import (
	"context"
	"sync"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/history"
)

// The archive writes message text to disk, which is a real privacy cost on
// a shared or unencrypted machine, so it is off until asked for.
var enableHistory = prefs.NewBool(false, prefs.PropMeta{
	Name:    "Keep Local Message History",
	Section: "Mods",
	Description: "Save messages LilDisc sees to ~/.local/state/lildisc/history " +
		"(up to 5,000 per channel) so Ctrl+Shift+F can search them later. " +
		"Message text is stored unencrypted on this computer. Turning this " +
		"off stops recording; use \"Clear Message History\" in Ctrl+K > to " +
		"delete what is saved.",
})

// historyFlushSeconds is how often recorded messages are written to disk.
const historyFlushSeconds = 30

var messageHistory = history.Default()

// RecordMessages adds loaded messages to the local history, if it is on.
// Views call it for every batch they load.
func RecordMessages(msgs ...discord.Message) {
	if enableHistory.Value() && len(msgs) > 0 {
		messageHistory.Add(msgs...)
	}
}

// HistoryEnabled reports whether the local history is on.
func HistoryEnabled() bool { return enableHistory.Value() }

// EachHistoryEntry iterates the local history, for search.
func EachHistoryEntry(f func(history.Entry) bool) { messageHistory.Each(f) }

func initHistory(ctx context.Context, state *gtkcord.State, win ActionWidget) {
	state.AddHandler(func(ev *gateway.MessageCreateEvent) { RecordMessages(ev.Message) })
	state.AddHandler(func(ev *gateway.MessageUpdateEvent) {
		if !enableHistory.Value() {
			return
		}
		// Update events can be partial; record the cabinet's merged copy.
		if m, err := state.Cabinet.Message(ev.ChannelID, ev.ID); err == nil {
			messageHistory.Add(*m)
		}
	})
	state.AddHandler(func(ev *gateway.MessageDeleteEvent) {
		messageHistory.Remove(ev.ChannelID, ev.ID)
	})
	state.AddHandler(func(ev *gateway.MessageDeleteBulkEvent) {
		messageHistory.Remove(ev.ChannelID, ev.IDs...)
	})

	// Handlers above belong to this session's state; the rest is per
	// process, and HookState runs again on every re-login.
	historySetup.Do(func() {
		glib.TimeoutSecondsAdd(historyFlushSeconds, func() bool {
			go messageHistory.Flush()
			return true
		})
		app.FromContext(ctx).ConnectShutdown(messageHistory.Flush)

		gtkutil.AddActions(win, map[string]func(){
			"clear-history": func() { confirmClearHistory(ctx, win) },
		})
	})
}

var historySetup sync.Once

func confirmClearHistory(ctx context.Context, win gtk.Widgetter) {
	dialog := adw.NewAlertDialog(
		locale.Get("Clear Message History?"),
		locale.Get("This deletes every message LilDisc has saved for searching. "+
			"Messages on Discord are not affected."),
	)
	dialog.AddResponse("cancel", locale.Get("_Cancel"))
	dialog.AddResponse("clear", locale.Get("_Clear"))
	dialog.SetResponseAppearance("clear", adw.ResponseDestructive)
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		if response != "clear" {
			return
		}
		if err := messageHistory.Clear(); err != nil {
			app.Error(ctx, err)
		}
	})
	dialog.Present(win)
}
