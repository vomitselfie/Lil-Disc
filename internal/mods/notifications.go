package mods

import (
	"context"
	"fmt"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/notify"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/ningen/v3"
	"github.com/dijama/lildisc/internal/gtkcord"
)

var enableNotifyAll = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Notify on All Messages",
	Section:     "Mods",
	Description: "Send desktop notifications for all messages in unmuted channels, not just mentions.",
})

var respectMuteState = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Respect Mute Settings",
	Section:     "Mods",
	Description: "Suppress notifications for muted channels and servers.",
})

func initNotifications(ctx context.Context, state *gtkcord.State, win gtk.Widgetter) {
	state.BindWidget(win, func(ev gateway.Event) {
		switch ev := ev.(type) {
		case *gateway.MessageCreateEvent:
			handleMessageNotification(ctx, state, ev)
		}
	})
}

func handleMessageNotification(ctx context.Context, state *gtkcord.State, ev *gateway.MessageCreateEvent) {
	if !enableNotifyAll.Value() {
		return
	}

	// Don't notify for our own messages.
	me, _ := state.Me()
	if me != nil && ev.Author.ID == me.ID {
		return
	}

	// Already handled by the built-in mention notification handler.
	mentions := state.MessageMentions(&ev.Message)
	if mentions.Has(ningen.MessageMentions) {
		return
	}

	// Respect DND status.
	if state.Status() == discord.DoNotDisturbStatus {
		return
	}

	// Respect channel/guild mute settings.
	if respectMuteState.Value() {
		unreadOpts := ningen.UnreadOpts{}
		if state.ChannelIsMuted(ev.ChannelID, unreadOpts) {
			return
		}
	}

	avatarURL := gtkcord.InjectAvatarSize(ev.Author.AvatarURL())

	notify.Send(ctx, notify.Notification{
		ID: notify.HashID(ev.ChannelID),
		Title: fmt.Sprintf(
			"%s (%s)",
			state.AuthorDisplayName(ev),
			gtkcord.ChannelNameFromID(ctx, ev.ChannelID),
		),
		Body: state.MessagePreview(&ev.Message),
		Icon: notify.IconURL(ctx, avatarURL, notify.IconName("avatar-default-symbolic")),
		Action: notify.Action{
			ActionID: "app.open-channel",
			Argument: gtkcord.NewChannelIDVariant(ev.ChannelID),
		},
	})
}
