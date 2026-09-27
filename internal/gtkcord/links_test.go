package gtkcord

import "testing"

func TestParseDiscordLink(t *testing.T) {
	tests := []struct {
		url    string
		ok     bool
		ch, id uint64
	}{
		{"https://discord.com/channels/1/2/3", true, 2, 3},
		{"https://discord.com/channels/@me/2/3", true, 2, 3},
		{"https://canary.discord.com/channels/1/2/3", true, 2, 3},
		{"https://discordapp.com/channels/1/2", true, 2, 0},
		{"https://discord.com/channels/1/2/3/", true, 2, 3},
		{"https://DISCORD.com/channels/1/2/3", true, 2, 3},

		{"https://discord.com/channels/1", false, 0, 0},
		{"https://discord.com/channels/1/2/3/4", false, 0, 0},
		{"https://discord.com/channels/x/2/3", false, 0, 0},
		{"https://discord.com/channels/1/x", false, 0, 0},
		{"https://discord.com/channels/1/2/x", false, 0, 0},
		{"https://discord.com/invite/abc", false, 0, 0},
		{"https://evil.example/channels/1/2/3", false, 0, 0},
		{"https://discord.com.evil.example/channels/1/2/3", false, 0, 0},
		{"ftp://discord.com/channels/1/2/3", false, 0, 0},
		{"not a url", false, 0, 0},
	}

	for _, tt := range tests {
		loc, ok := ParseDiscordLink(tt.url)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v, want %v", tt.url, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if uint64(loc.ChannelID) != tt.ch || uint64(loc.MessageID) != tt.id {
			t.Errorf("%s: got %d/%d, want %d/%d", tt.url, loc.ChannelID, loc.MessageID, tt.ch, tt.id)
		}
	}
}
