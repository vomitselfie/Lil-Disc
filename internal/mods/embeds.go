package mods

import (
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/chatkit/components/embed"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
)

// mod: embeds — override chatkit's black background on embed images.
// The upstream CSS sets background-color: black on .thumbnail-embed-image
// which kills transparency for PNG emoji and other alpha images.
var _ = cssutil.WriteCSS(`
	.thumbnail-embed .thumbnail-embed-image {
		background-color: transparent;
	}
`)

// TypeFromURL wraps chatkit's embed.TypeFromURL but strips query params first.
// The upstream function uses path.Ext(url) which breaks on URLs like
// "https://cdn.discordapp.com/emojis/123.gif?size=48" (returns ".gif?size=48").
func TypeFromURL(u string) embed.EmbedType {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	return embed.TypeFromURL(u)
}

// Embed-related preferences exposed for use by the messages package.

var AutoAnimateGIFs = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Auto-Animate GIFs",
	Section:     "Mods",
	Description: "Automatically animate GIFs and GIFV embeds instead of requiring hover. When off, GIFs animate on hover.",
})

var AutoPlayVideos = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Auto-Play Video Thumbnails",
	Section:     "Mods",
	Description: "Show video embeds with animated thumbnails where available.",
})

var PreferGIF = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Prefer GIF over MP4",
	Section:     "Mods",
	Description: "For Tenor/Giphy, load the actual .gif instead of the .mp4 video version. Note: GIFs display as static images; the MP4 video path supports animation.",
})

// GIFVToGIF attempts to find a real .gif URL for a GIFV embed (Tenor/Giphy).
// Returns the GIF URL and true if a known GIF source was found.
// Only returns URLs that are already confirmed GIFs — does not fabricate them.
func GIFVToGIF(msgEmbed *discord.Embed) (string, bool) {
	if !PreferGIF.Value() {
		return "", false
	}

	// Check if thumbnail is already a GIF (common for Giphy).
	if msgEmbed.Thumbnail != nil {
		u := string(msgEmbed.Thumbnail.URL)
		if isGIFURL(u) {
			return u, true
		}
	}

	// Check image field.
	if msgEmbed.Image != nil {
		u := string(msgEmbed.Image.URL)
		if isGIFURL(u) {
			return u, true
		}
	}

	// For Giphy: video URL is .mp4 but the GIF lives at the same path
	// with "giphy.gif" instead of "giphy.mp4".
	if msgEmbed.Video != nil {
		u := string(msgEmbed.Video.URL)
		if strings.Contains(u, "giphy.com") && strings.HasSuffix(u, "/giphy.mp4") {
			return strings.TrimSuffix(u, "/giphy.mp4") + "/giphy.gif", true
		}
	}

	return "", false
}

// IsBrokenProxy checks if a Discord proxy URL looks truncated/broken.
// Known pattern: fixupx/fxtwitter proxies end at "/media" without a filename.
func IsBrokenProxy(proxyURL string) bool {
	if proxyURL == "" {
		return true
	}
	// Discord external proxy URLs that end without a file extension are likely
	// truncated. A valid proxy should have a full path including filename.
	// Example broken: https://images-ext-1.discordapp.net/external/HASH/https/pbs.twimg.com/media
	// Example valid:  https://images-ext-1.discordapp.net/external/HASH/https/pbs.twimg.com/media/xxx.jpg
	if strings.Contains(proxyURL, "images-ext") && strings.HasSuffix(proxyURL, "/media") {
		return true
	}
	return false
}

// GroupEmbeds returns a slice of consecutive embeds starting at index i that
// share the same URL and each have an Image field. Used for multi-image posts
// (e.g. Twitter/X) where Discord creates separate embeds per image.
func GroupEmbeds(embeds []discord.Embed, i int) []discord.Embed {
	if i >= len(embeds) {
		return nil
	}

	first := &embeds[i]
	if first.URL == "" || first.Image == nil {
		return embeds[i : i+1]
	}

	end := i + 1
	for end < len(embeds) {
		e := &embeds[end]
		if e.URL != first.URL || e.Image == nil {
			break
		}
		end++
	}

	return embeds[i:end]
}

func isGIFURL(u string) bool {
	lower := strings.ToLower(u)
	if i := strings.IndexByte(lower, '?'); i >= 0 {
		lower = lower[:i]
	}
	return strings.HasSuffix(lower, ".gif")
}
