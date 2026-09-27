package mods

import (
	"context"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/dijama/lildisc/internal/gtkcord"
	"github.com/dijama/lildisc/internal/lilcss"
)

var enablePresence = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Status Indicators",
	Section:     "Mods",
	Description: "Show colored status dots next to usernames.",
})

var _ = lilcss.WriteCSS(`
	.mod-presence-dot {
		font-size: 9px;
		min-width: 9px;
		min-height: 9px;
		margin: 0 1px 1px 0;
		border-radius: {$radius_pill};
		padding: 1px;
		background-color: @theme_bg_color;
	}
	.mod-presence-online {
		color: @lil_status_online;
	}
	.mod-presence-idle {
		color: @lil_status_idle;
	}
	.mod-presence-dnd {
		color: @lil_status_dnd;
	}
	.mod-presence-offline {
		color: @lil_status_offline;
	}
`)

// NewPresenceDot creates a small colored status dot for the given user.
// It auto-updates when the user's presence changes.
// Returns nil if the mod is disabled.
func NewPresenceDot(ctx context.Context, userID discord.UserID, guildID discord.GuildID) *gtk.Label {
	if !enablePresence.Value() {
		return nil
	}

	state := gtkcord.FromContext(ctx)
	if state == nil {
		return nil
	}

	dot := gtk.NewLabel("●")
	dot.AddCSSClass("mod-presence-dot")

	updatePresenceDot(state, dot, userID, guildID)

	state.AddHandlerForWidget(dot, func(ev *gateway.PresenceUpdateEvent) {
		if ev.User.ID == userID {
			updatePresenceDot(state, dot, userID, guildID)
		}
	})

	// Discord never dispatches PresenceUpdateEvent for the current user's
	// own status changes. Self-status is instead carried by SESSIONS_REPLACE
	// (remote status change via another session) and USER_SETTINGS_UPDATE
	// (local status change via the in-app dropdown). ningen writes both
	// into PresenceStore silently, so without explicit subscriptions the
	// self-dot never refreshes.
	if me, _ := state.Me(); me != nil && me.ID == userID {
		state.AddHandlerForWidget(dot, func(*gateway.SessionsReplaceEvent) {
			updatePresenceDot(state, dot, userID, guildID)
		})
		state.AddHandlerForWidget(dot, func(*gateway.UserSettingsUpdateEvent) {
			updatePresenceDot(state, dot, userID, guildID)
		})
	}

	return dot
}

func updatePresenceDot(state *gtkcord.State, dot *gtk.Label, userID discord.UserID, guildID discord.GuildID) {
	presence, _ := state.PresenceStore.Presence(guildID, userID)

	var status discord.Status
	if presence != nil {
		status = presence.Status
	} else {
		status = discord.OfflineStatus
	}

	// Remove old status classes.
	dot.RemoveCSSClass("mod-presence-online")
	dot.RemoveCSSClass("mod-presence-idle")
	dot.RemoveCSSClass("mod-presence-dnd")
	dot.RemoveCSSClass("mod-presence-offline")

	switch status {
	case discord.OnlineStatus:
		dot.AddCSSClass("mod-presence-online")
	case discord.IdleStatus:
		dot.AddCSSClass("mod-presence-idle")
	case discord.DoNotDisturbStatus:
		dot.AddCSSClass("mod-presence-dnd")
	default:
		dot.AddCSSClass("mod-presence-offline")
	}

	dot.SetTooltipText(statusText(status))
}

// PresenceTooltip builds a rich tooltip string showing a user's status,
// activity, and device info. Returns empty string if no info available.
func PresenceTooltip(ctx context.Context, userID discord.UserID, guildID discord.GuildID) string {
	state := gtkcord.FromContext(ctx)
	if state == nil {
		return ""
	}

	presence, _ := state.PresenceStore.Presence(guildID, userID)
	if presence == nil {
		return ""
	}

	var parts []string

	// Status
	parts = append(parts, statusText(presence.Status))

	// Device info
	var devices []string
	if presence.ClientStatus.Desktop != "" {
		devices = append(devices, "🖥 Desktop")
	}
	if presence.ClientStatus.Mobile != "" {
		devices = append(devices, "📱 Mobile")
	}
	if presence.ClientStatus.Web != "" {
		devices = append(devices, "🌐 Web")
	}
	parts = append(parts, devices...)

	// Activities
	for _, act := range presence.Activities {
		switch act.Type {
		case discord.CustomActivity:
			line := ""
			if act.Emoji != nil {
				line += act.Emoji.Name + " "
			}
			if act.State != "" {
				line += act.State
			}
			if line != "" {
				parts = append(parts, line)
			}
		case discord.GameActivity:
			line := "Playing " + act.Name
			if act.Details != "" {
				line += "\n  " + act.Details
			}
			if act.State != "" {
				line += "\n  " + act.State
			}
			parts = append(parts, line)
		case discord.StreamingActivity:
			parts = append(parts, "Streaming "+act.Name)
		case discord.ListeningActivity:
			line := "Listening to " + act.Name
			if act.Details != "" {
				line += "\n  " + act.Details
			}
			if act.State != "" {
				line += " by " + act.State
			}
			parts = append(parts, line)
		case discord.WatchingActivity:
			parts = append(parts, "Watching "+act.Name)
		case discord.CompetingActivity:
			parts = append(parts, "Competing in "+act.Name)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	result := parts[0]
	for _, p := range parts[1:] {
		result += "\n" + p
	}
	return result
}

// SetupPresenceTooltip attaches a live-updating tooltip to a widget showing
// a user's status, activity, and device. Updates on presence changes.
func SetupPresenceTooltip(ctx context.Context, widget gtk.Widgetter, userID discord.UserID, guildID discord.GuildID) {
	if !enablePresence.Value() {
		return
	}

	state := gtkcord.FromContext(ctx)
	if state == nil {
		return
	}

	base := gtk.BaseWidget(widget)

	update := func() {
		tip := PresenceTooltip(ctx, userID, guildID)
		base.SetTooltipText(tip)
	}

	update()

	state.AddHandlerForWidget(widget, func(ev *gateway.PresenceUpdateEvent) {
		if ev.User.ID == userID {
			update()
		}
	})

	// Self-status carriers; see NewPresenceDot for the rationale.
	if me, _ := state.Me(); me != nil && me.ID == userID {
		state.AddHandlerForWidget(widget, func(*gateway.SessionsReplaceEvent) {
			update()
		})
		state.AddHandlerForWidget(widget, func(*gateway.UserSettingsUpdateEvent) {
			update()
		})
	}
}

func statusText(status discord.Status) string {
	switch status {
	case discord.OnlineStatus:
		return "Online"
	case discord.DoNotDisturbStatus:
		return "Do Not Disturb"
	case discord.IdleStatus:
		return "Idle"
	case discord.InvisibleStatus:
		return "Invisible"
	case discord.OfflineStatus:
		return "Offline"
	default:
		return "Unknown"
	}
}
