package mods

import (
	"context"
	"log/slog"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/dijama/lildisc/internal/gtkcord"
)

// AttachChannelContextMenu adds a right-click context menu to a channel
// widget with Mark as Read, Mute/Unmute, and Copy Channel ID options.
func AttachChannelContextMenu(ctx context.Context, widget gtk.Widgetter, chID discord.ChannelID) {
	state := gtkcord.FromContext(ctx)
	if state == nil {
		return
	}

	actions := map[string]func(){
		"chctx.mark-read": func() {
			// Use the channel's LastMessageID — Cabinet.Messages() is empty
			// for channels the user hasn't opened yet.
			ch, _ := state.Cabinet.Channel(chID)
			if ch != nil && ch.LastMessageID.IsValid() {
				state.ReadState.MarkRead(chID, ch.LastMessageID)
				slog.Debug("marked channel as read", "channel", chID)
			}
		},
		"chctx.copy-id": func() {
			display := gdk.DisplayGetDefault()
			if display != nil {
				display.Clipboard().SetText(chID.String())
			}
		},
	}

	gtkutil.BindActionMap(widget, actions)
	gtkutil.BindPopoverMenuCustom(widget, gtk.PosBottom, []gtkutil.PopoverMenuItem{
		gtkutil.MenuItemIcon("Mark as _Read", "chctx.mark-read", "mail-read-symbolic"),
		gtkutil.MenuItemIcon("Copy Channel _ID", "chctx.copy-id", "edit-copy-symbolic"),
	})
}
