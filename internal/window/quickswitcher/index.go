package quickswitcher

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/ningen/v3"
	"github.com/sahilm/fuzzy"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
)

type index struct {
	state    *gtkcord.State
	items    indexItems
	channels map[discord.ChannelID]channelItem
	buffer   indexItems
}

const searchLimit = 25

var excludedChannelTypes = []discord.ChannelType{
	discord.GuildCategory,
	discord.GuildForum,
}

var allowedChannelTypes = slices.DeleteFunc(
	slices.Clone(gtkcord.AllowedChannelTypes),
	func(t discord.ChannelType) bool {
		return slices.Contains(excludedChannelTypes, t)
	},
)

func (idx *index) update(ctx context.Context) {
	idx.state = gtkcord.FromContext(ctx)
	state := idx.state.Offline()
	items := make([]indexItem, 0, 250)
	channels := make(map[discord.ChannelID]channelItem)

	dms, err := state.PrivateChannels()
	if err != nil {
		app.Error(ctx, err)
		return
	}

	for i := range dms {
		item := newChannelItem(state, nil, &dms[i])
		items = append(items, item)
		channels[item.ID] = item
	}

	guilds, err := state.Guilds()
	if err != nil {
		app.Error(ctx, err)
		return
	}

	for i, guild := range guilds {
		chs, err := state.Channels(guild.ID, allowedChannelTypes)
		if err != nil {
			slog.Error(
				"cannot populate channels for guild in quick switcher",
				"guild", guild.Name,
				"guild_id", guild.ID,
				"err", err)
			continue
		}

		items = append(items, newGuildItem(&guilds[i]))
		for j := range chs {
			item := newChannelItem(state, &guilds[i], &chs[j])
			items = append(items, item)
			channels[item.ID] = item
		}
	}

	idx.items = items
	idx.channels = channels
}

// searchMode narrows what a query matches. It is chosen by a leading
// character, as in the official client's switcher.
type searchMode int

const (
	modeAll      searchMode = iota
	modeDMs                 // "@" prefix: people
	modeChannels            // "#" prefix: server channels
	modeGuilds              // "!" prefix: servers
	modeCommands            // ">" prefix: commands
)

func parseQuery(str string) (searchMode, string) {
	str = strings.TrimSpace(str)
	if str == "" {
		return modeAll, ""
	}
	mode := modeAll
	switch str[0] {
	case '@':
		mode = modeDMs
	case '#':
		mode = modeChannels
	case '!':
		mode = modeGuilds
	case '>':
		mode = modeCommands
	default:
		return modeAll, str
	}
	return mode, strings.TrimSpace(str[1:])
}

func (m searchMode) accepts(it indexItem) bool {
	switch m {
	case modeDMs:
		ch, ok := it.(channelItem)
		return ok && ch.guild == nil
	case modeChannels:
		ch, ok := it.(channelItem)
		return ok && ch.guild != nil
	case modeGuilds:
		_, ok := it.(guildItem)
		return ok
	case modeCommands:
		_, ok := it.(commandItem)
		return ok
	}
	_, isCommand := it.(commandItem)
	return !isCommand
}

// Ranking weights. The fuzzy score says how well the text matches; the
// rest lift what is likely wanted right now, so a channel opened a minute
// ago, or one that mentions you, beats a similarly named one untouched for
// months. They are sized against sahilm/fuzzy scores, which run from a few
// points for a scattered match to a few dozen for a tight prefix one.
const (
	weightRecency = 30 // most recent channel; falls off linearly
	weightUnread  = 8
	weightMention = 20
)

func (idx *index) search(str string) []indexItem {
	if idx.items == nil {
		return nil
	}

	mode, query := parseQuery(str)
	idx.buffer = idx.buffer[:0]

	if query == "" {
		return idx.browse(mode)
	}

	pool := make(indexItems, 0, len(idx.items)+len(commands))
	for _, it := range idx.items {
		if mode.accepts(it) {
			pool = append(pool, it)
		}
	}
	for _, c := range commands {
		if mode.accepts(c) {
			pool = append(pool, c)
		}
	}

	recency := idx.recencyRanks()

	type scored struct {
		item  indexItem
		score int
	}
	matches := fuzzy.FindFrom(query, pool)
	results := make([]scored, len(matches))
	for i, m := range matches {
		results[i] = scored{pool[m.Index], m.Score + idx.boost(pool[m.Index], recency)}
	}
	slices.SortStableFunc(results, func(a, b scored) int { return cmp.Compare(b.score, a.score) })

	for i := 0; i < len(results) && i < searchLimit; i++ {
		idx.buffer = append(idx.buffer, results[i].item)
	}
	return idx.buffer
}

// browse lists what a mode shows before anything is typed: recent channels
// for a bare or @ query, and every command for >.
func (idx *index) browse(mode searchMode) []indexItem {
	switch mode {
	case modeCommands:
		for _, c := range commands {
			idx.buffer = append(idx.buffer, c)
		}
		return idx.buffer
	case modeGuilds:
		return nil
	}

	for _, id := range gtkcord.RecentChannels() {
		ch, ok := idx.channels[id]
		if !ok || !mode.accepts(ch) {
			continue
		}
		idx.buffer = append(idx.buffer, ch)
		if len(idx.buffer) >= searchLimit {
			break
		}
	}
	return idx.buffer
}

func (idx *index) recencyRanks() map[discord.ChannelID]int {
	recent := gtkcord.RecentChannels()
	ranks := make(map[discord.ChannelID]int, len(recent))
	for i, id := range recent {
		ranks[id] = i
	}
	return ranks
}

// boost is the non-textual part of an item's score.
func (idx *index) boost(it indexItem, recency map[discord.ChannelID]int) int {
	ch, ok := it.(channelItem)
	if !ok {
		return 0
	}

	var b int
	if rank, ok := recency[ch.ID]; ok {
		b += weightRecency * (len(recency) - rank) / len(recency)
	}
	if idx.state != nil {
		switch idx.state.ChannelIsUnread(ch.ID, ningen.UnreadOpts{}) {
		case ningen.ChannelMentioned:
			b += weightMention
		case ningen.ChannelUnread:
			b += weightUnread
		}
	}
	return b
}
