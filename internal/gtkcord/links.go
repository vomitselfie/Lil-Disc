package gtkcord

import (
	"net/url"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
)

// discordLinkHosts are the hosts that serve Discord's web client, including
// the test channels and the old domain, which still resolves.
var discordLinkHosts = map[string]bool{
	"discord.com":           true,
	"www.discord.com":       true,
	"ptb.discord.com":       true,
	"canary.discord.com":    true,
	"discordapp.com":        true,
	"www.discordapp.com":    true,
	"ptb.discordapp.com":    true,
	"canary.discordapp.com": true,
}

// ParseDiscordLink recognises a link to a channel or message in Discord's web
// client:
//
//	https://discord.com/channels/<guild or @me>/<channel>[/<message>]
//
// MessageID is zero for a channel link. ok is false for anything else.
func ParseDiscordLink(raw string) (loc MessageLocation, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return loc, false
	}
	if !discordLinkHosts[strings.ToLower(u.Host)] {
		return loc, false
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || len(parts) > 4 || parts[0] != "channels" {
		return loc, false
	}
	if parts[1] != "@me" {
		if _, err := discord.ParseSnowflake(parts[1]); err != nil {
			return loc, false
		}
	}

	ch, err := discord.ParseSnowflake(parts[2])
	if err != nil || !ch.IsValid() {
		return loc, false
	}
	loc.ChannelID = discord.ChannelID(ch)

	if len(parts) == 4 {
		msg, err := discord.ParseSnowflake(parts[3])
		if err != nil || !msg.IsValid() {
			return MessageLocation{}, false
		}
		loc.MessageID = discord.MessageID(msg)
	}
	return loc, true
}
