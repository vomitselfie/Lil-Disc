package gtkcord

import (
	"slices"
	"sync"

	"github.com/diamondburned/arikawa/v3/discord"
)

// maxRecentChannels is how many recently opened channels are remembered.
const maxRecentChannels = 50

var recentChannels struct {
	mu  sync.Mutex
	ids []discord.ChannelID // most recent first
}

// NoteChannelOpened records that the user opened a channel, for ranking the
// quick switcher by recency.
func NoteChannelOpened(id discord.ChannelID) {
	if !id.IsValid() {
		return
	}
	recentChannels.mu.Lock()
	defer recentChannels.mu.Unlock()

	ids := slices.DeleteFunc(recentChannels.ids, func(x discord.ChannelID) bool { return x == id })
	ids = slices.Insert(ids, 0, id)
	if len(ids) > maxRecentChannels {
		ids = ids[:maxRecentChannels]
	}
	recentChannels.ids = ids
}

// RecentChannels returns recently opened channels, most recent first.
func RecentChannels() []discord.ChannelID {
	recentChannels.mu.Lock()
	defer recentChannels.mu.Unlock()
	return slices.Clone(recentChannels.ids)
}

// SetRecentChannels replaces the recent list, for restoring a saved session.
func SetRecentChannels(ids []discord.ChannelID) {
	recentChannels.mu.Lock()
	defer recentChannels.mu.Unlock()
	if len(ids) > maxRecentChannels {
		ids = ids[:maxRecentChannels]
	}
	recentChannels.ids = slices.Clone(ids)
}
