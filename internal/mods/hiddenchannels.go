package mods

import (
	"sort"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
	"github.com/dijama/lildisc/internal/gtkcord"
)

var enableHiddenChannels = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Show Hidden Channels",
	Section:     "Mods",
	Description: "Show channels you don't have access to with a lock icon, greyed out. May violate Discord's Terms of Service.",
})

var _ = cssutil.WriteCSS(`
	.channel-item-hidden {
		opacity: 0.35;
	}
`)

// IsChannelHidden returns true if the user doesn't have ViewChannel permission.
// Returns false if the mod is disabled or the channel is accessible.
func IsChannelHidden(state *gtkcord.State, chID discord.ChannelID) bool {
	if !enableHiddenChannels.Value() {
		return false
	}
	return !state.Offline().HasPermissions(chID, discord.PermissionViewChannel)
}

// FetchAllChannels returns all channels for a guild including hidden ones,
// filtered to the given parent and sorted by position. This supplements
// the upstream fetchSortedChannels which excludes hidden channels.
// Returns nil if the mod is disabled.
func FetchAllChannels(state *gtkcord.State, guildID discord.GuildID, parentID discord.ChannelID, allowedTypes []discord.ChannelType) []discord.Channel {
	if !enableHiddenChannels.Value() {
		return nil
	}

	allChannels, err := state.Cabinet.Channels(guildID)
	if err != nil {
		return nil
	}

	allowedMap := make(map[discord.ChannelType]bool, len(allowedTypes))
	for _, t := range allowedTypes {
		allowedMap[t] = true
	}

	// Collect hidden channels that match the parent and allowed types.
	var hidden []discord.Channel
	for _, ch := range allChannels {
		if !allowedMap[ch.Type] {
			continue
		}
		// Skip categories — they're always visible.
		if ch.Type == discord.GuildCategory {
			continue
		}
		// Only include channels in this parent.
		if ch.ParentID != parentID && !(parentID == 0 && !ch.ParentID.IsValid()) {
			continue
		}
		// Only include channels the user CAN'T see.
		if state.Offline().HasPermissions(ch.ID, discord.PermissionViewChannel) {
			continue
		}
		hidden = append(hidden, ch)
	}

	sort.Slice(hidden, func(i, j int) bool {
		if hidden[i].Position == hidden[j].Position {
			return hidden[i].ID < hidden[j].ID
		}
		return hidden[i].Position < hidden[j].Position
	})

	return hidden
}
