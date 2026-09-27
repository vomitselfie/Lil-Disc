package quickswitcher

import (
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
)

func TestParseQuery(t *testing.T) {
	tests := []struct {
		in    string
		mode  searchMode
		query string
	}{
		{"", modeAll, ""},
		{"  general ", modeAll, "general"},
		{"@alice", modeDMs, "alice"},
		{"# general", modeChannels, "general"},
		{"!server", modeGuilds, "server"},
		{"> prefs", modeCommands, "prefs"},
		{">", modeCommands, ""},
	}
	for _, tt := range tests {
		mode, query := parseQuery(tt.in)
		if mode != tt.mode || query != tt.query {
			t.Errorf("parseQuery(%q) = %v, %q; want %v, %q", tt.in, mode, query, tt.mode, tt.query)
		}
	}
}

func TestModeFilters(t *testing.T) {
	dm := channelItem{Channel: &discord.Channel{ID: 1}}
	guildCh := channelItem{Channel: &discord.Channel{ID: 2}, guild: &discord.Guild{}}
	guild := guildItem{Guild: &discord.Guild{}}
	cmd := commands[0]

	cases := map[searchMode][4]bool{ // dm, guild channel, guild, command
		modeAll:      {true, true, true, false},
		modeDMs:      {true, false, false, false},
		modeChannels: {false, true, false, false},
		modeGuilds:   {false, false, true, false},
		modeCommands: {false, false, false, true},
	}
	for mode, want := range cases {
		got := [4]bool{mode.accepts(dm), mode.accepts(guildCh), mode.accepts(guild), mode.accepts(cmd)}
		if got != want {
			t.Errorf("mode %v accepts %v, want %v", mode, got, want)
		}
	}
}

// Of two channels matching the query equally, the one opened more recently
// must rank first.
func TestRecencyBreaksTies(t *testing.T) {
	older := channelItem{Channel: &discord.Channel{ID: 10}, search: "general"}
	newer := channelItem{Channel: &discord.Channel{ID: 20}, search: "general"}

	idx := index{items: indexItems{older, newer}}
	gtkcord.SetRecentChannels([]discord.ChannelID{20, 10})
	t.Cleanup(func() { gtkcord.SetRecentChannels(nil) })

	got := idx.search("general")
	if len(got) != 2 || got[0].(channelItem).ID != 20 {
		t.Fatalf("most recent channel did not rank first: %+v", got)
	}

	// With nothing typed, the switcher lists recent channels in order.
	browse := idx.search("")
	if len(browse) != 0 {
		// channels map is empty in this index, so browse finds nothing;
		// populate it and check the order.
		t.Fatalf("unexpected browse results without a channel map: %v", browse)
	}
	idx.channels = map[discord.ChannelID]channelItem{10: older, 20: newer}
	browse = idx.search("")
	if len(browse) != 2 || browse[0].(channelItem).ID != 20 || browse[1].(channelItem).ID != 10 {
		t.Fatalf("browse order = %+v, want 20 then 10", browse)
	}
}
