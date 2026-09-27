package mods

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"
	"github.com/vomitselfie/Lil-Disc/internal/discordident"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableStickerPicker = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Sticker Picker",
	Section:     "Mods",
	Description: "Picker showing all available guild stickers, organized by server.",
})

var stickerPickerCSS = lilcss.Applier("mod-sticker-picker", `
	.mod-sticker-picker {
		min-width: 380px;
		min-height: 420px;
	}
	.mod-sticker-search {
		margin: {$space_md};
	}
	.mod-sticker-grid {
		padding: {$space_xs};
	}
	.mod-sticker-item {
		padding: {$space_xs};
		border-radius: {$radius_md};
	}
	.mod-sticker-item:hover {
		background: @lil_hover;
	}
	.mod-sticker-item .onlineimage {
		background: transparent;
	}
	.mod-sticker-guild-header {
		font-weight: bold;
		font-size: {$font_micro};
		color: @lil_text_faint;
		padding: {$space_md} {$space_md} {$space_xs} {$space_md};
	}
`)

const stickerPickerSize = 72

// StickerPickResult contains the result of a sticker selection.
type StickerPickResult struct {
	StickerID discord.StickerID
	Name      string
}

// guildSticker represents a sticker fetched from the Discord REST API.
type guildSticker struct {
	ID         discord.StickerID `json:"id"`
	Name       string            `json:"name"`
	Tags       string            `json:"tags"`
	FormatType int               `json:"format_type"` // 1=PNG, 2=APNG, 3=Lottie
}

// staticURL returns a media proxy URL for reliable static thumbnail loading.
func (s guildSticker) staticURL() string {
	return fmt.Sprintf("https://media.discordapp.net/stickers/%s.webp?size=128", s.ID)
}

// animatedURL returns the CDN URL which serves the actual APNG for animation.
func (s guildSticker) animatedURL() string {
	return fmt.Sprintf("https://cdn.discordapp.com/stickers/%s.png", s.ID)
}

func (s guildSticker) matchesQuery(query string) bool {
	if strings.Contains(strings.ToLower(s.Name), query) {
		return true
	}
	for _, tag := range strings.Split(s.Tags, ",") {
		if strings.Contains(strings.ToLower(strings.TrimSpace(tag)), query) {
			return true
		}
	}
	return false
}

// stickerCache caches guild stickers in memory and on disk.
var (
	stickerCacheMu sync.Mutex
	stickerCache   = make(map[discord.GuildID][]guildSticker)
)

// fetchGuildStickers loads stickers from disk cache first, then API as fallback.
func fetchGuildStickers(token string, guildID discord.GuildID) ([]guildSticker, error) {
	stickerCacheMu.Lock()
	if cached, ok := stickerCache[guildID]; ok {
		stickerCacheMu.Unlock()
		return cached, nil
	}
	stickerCacheMu.Unlock()

	// Try disk cache first (no expiry — only refreshed manually).
	cacheFile := fmt.Sprintf("stickers_%s.json", guildID)
	var stickers []guildSticker
	if loadCachedJSON(cacheFile, 0, &stickers) {
		stickerCacheMu.Lock()
		stickerCache[guildID] = stickers
		stickerCacheMu.Unlock()
		return stickers, nil
	}

	// Fetch from API.
	url := fmt.Sprintf("https://discord.com/api/v10/guilds/%s/stickers", guildID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	discordident.Get().Apply(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("discord API returned %d: %s", resp.StatusCode, string(body))
	}

	if err := decodeJSONResponse(resp, &stickers); err != nil {
		return nil, err
	}

	// Save to memory and disk.
	stickerCacheMu.Lock()
	stickerCache[guildID] = stickers
	stickerCacheMu.Unlock()
	saveCachedJSON(cacheFile, stickers)

	return stickers, nil
}

// InvalidateStickerCache clears cached stickers for a guild (memory and disk).
func InvalidateStickerCache(guildID discord.GuildID) {
	stickerCacheMu.Lock()
	delete(stickerCache, guildID)
	stickerCacheMu.Unlock()
	os.Remove(filepath.Join(apiCacheDir(), fmt.Sprintf("stickers_%s.json", guildID)))
}

// SendSticker sends a message containing only a sticker to the given channel.
// arikawa v3's SendMessageData doesn't support sticker_ids, so we make a raw
// POST to the Discord API.
func SendSticker(token string, channelID discord.ChannelID, stickerID discord.StickerID, ref *discord.MessageReference) error {
	type stickerMessage struct {
		StickerIDs []discord.StickerID       `json:"sticker_ids"`
		Reference  *discord.MessageReference `json:"message_reference,omitempty"`
	}

	body, err := json.Marshal(stickerMessage{
		StickerIDs: []discord.StickerID{stickerID},
		Reference:  ref,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", channelID)
	req, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	discordident.Get().Apply(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("discord API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// defaultStickerPack is a standard Discord sticker pack from /sticker-packs.
type defaultStickerPack struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Stickers []guildSticker `json:"stickers"`
}

type stickerPacksResponse struct {
	Packs []defaultStickerPack `json:"sticker_packs"`
}

var (
	defaultPacksMu    sync.Mutex
	defaultPacksCache []defaultStickerPack
)

func fetchDefaultStickerPacks(token string) []defaultStickerPack {
	defaultPacksMu.Lock()
	if defaultPacksCache != nil {
		defer defaultPacksMu.Unlock()
		return defaultPacksCache
	}
	defaultPacksMu.Unlock()

	// Try disk cache first.
	var packs []defaultStickerPack
	if loadCachedJSON("default_sticker_packs.json", 0, &packs) {
		defaultPacksMu.Lock()
		defaultPacksCache = packs
		defaultPacksMu.Unlock()
		return packs
	}

	// Fetch from API.
	req, err := http.NewRequest("GET", "https://discord.com/api/v10/sticker-packs", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", token)
	discordident.Get().Apply(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	var data stickerPacksResponse
	if err := decodeJSONResponse(resp, &data); err != nil {
		return nil
	}

	defaultPacksMu.Lock()
	defaultPacksCache = data.Packs
	defaultPacksMu.Unlock()
	saveCachedJSON("default_sticker_packs.json", data.Packs)

	return data.Packs
}

// NewStickerPickerPopover creates a sticker picker popover for the composer.
// guildID is the current guild context — non-Nitro users only see that guild's stickers.
func NewStickerPickerPopover(ctx context.Context, guildID discord.GuildID, onPick func(StickerPickResult)) *gtk.Popover {
	if !enableStickerPicker.Value() {
		return nil
	}

	state := gtkcord.FromContext(ctx)
	if state == nil {
		return nil
	}

	search := gtk.NewSearchEntry()
	search.AddCSSClass("mod-sticker-search")
	search.SetPlaceholderText("Search stickers...")

	stickerBox := gtk.NewBox(gtk.OrientationVertical, 0)
	stickerBox.AddCSSClass("mod-sticker-grid")

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(stickerBox)
	scroll.SetVExpand(true)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(search)
	content.Append(scroll)
	stickerPickerCSS(content)

	popover := gtk.NewPopover()
	popover.AddCSSClass("mod-sticker-picker")
	popover.SetChild(content)
	popover.SetSizeRequest(380, 420)

	addStickerFlow := func(stickers []guildSticker) *gtk.FlowBox {
		flow := gtk.NewFlowBox()
		flow.SetSelectionMode(gtk.SelectionNone)
		flow.SetMaxChildrenPerLine(4)
		flow.SetMinChildrenPerLine(3)
		flow.SetHomogeneous(true)

		for _, s := range stickers {
			s := s
			// Skip Lottie stickers — no renderer available.
			if s.FormatType == 3 {
				continue
			}

			img := onlineimage.NewPicture(ctx, imgutil.HTTPProvider)
			img.SetSizeRequest(stickerPickerSize, stickerPickerSize)
			img.SetContentFit(gtk.ContentFitContain)
			img.SetURL(s.staticURL())

			tooltip := html.EscapeString(s.Name)
			if s.Tags != "" {
				tooltip += "\n" + fmt.Sprintf(
					`<span size="smaller" fgalpha="75%%">%s</span>`,
					html.EscapeString(s.Tags),
				)
			}

			box := gtk.NewBox(gtk.OrientationVertical, 0)
			box.AddCSSClass("mod-sticker-item")
			box.Append(img)
			box.SetTooltipMarkup(tooltip)

			click := gtk.NewGestureClick()
			click.ConnectReleased(func(n int, x, y float64) {
				onPick(StickerPickResult{
					StickerID: s.ID,
					Name:      s.Name,
				})
				popover.Popdown()
			})
			box.AddController(click)
			flow.Append(box)
		}
		return flow
	}

	populate := func(query string) {
		clearBox(stickerBox)
		query = strings.ToLower(query)

		hasNitro := state.EmojiState.HasNitro()
		token := state.Token()

		// Determine which guilds to show stickers for.
		var guildIDs []discord.GuildID
		if hasNitro {
			// Nitro: show all guilds.
			guilds, err := state.Cabinet.Guilds()
			if err == nil {
				for _, g := range guilds {
					guildIDs = append(guildIDs, g.ID)
				}
			}
		} else if guildID.IsValid() {
			// Non-Nitro: only current guild's stickers.
			guildIDs = []discord.GuildID{guildID}
		}

		loading := gtk.NewLabel("Loading stickers...")
		loading.AddCSSClass("mod-sticker-guild-header")
		stickerBox.Append(loading)

		go func() {
			type stickerSection struct {
				name     string
				stickers []guildSticker
			}

			var sections []stickerSection

			// Guild stickers.
			guilds, _ := state.Cabinet.Guilds()
			guildNames := make(map[discord.GuildID]string, len(guilds))
			for _, g := range guilds {
				guildNames[g.ID] = g.Name
			}

			// Fetch in parallel with bounded concurrency. The disk cache makes
			// warm fetches instant; the slow path is the first cold open with
			// many guilds, where 100 sequential RTTs would block the picker
			// for seconds.
			perGuild := make([]stickerSection, len(guildIDs))
			sem := make(chan struct{}, 5)
			var wg sync.WaitGroup
			for i, gID := range guildIDs {
				wg.Add(1)
				sem <- struct{}{}
				go func(i int, gID discord.GuildID) {
					defer wg.Done()
					defer func() { <-sem }()
					stickers, err := fetchGuildStickers(token, gID)
					if err != nil {
						slog.Debug("failed to fetch stickers",
							"guild", gID, "err", err)
						return
					}
					if query != "" {
						stickers = filterStickers(stickers, query)
					}
					if len(stickers) == 0 {
						return
					}
					name := guildNames[gID]
					if name == "" {
						name = gID.String()
					}
					perGuild[i] = stickerSection{name: name, stickers: stickers}
				}(i, gID)
			}
			wg.Wait()
			for _, sec := range perGuild {
				if sec.name != "" {
					sections = append(sections, sec)
				}
			}

			// Default sticker packs (always available).
			packs := fetchDefaultStickerPacks(token)
			for _, pack := range packs {
				stickers := pack.Stickers
				if query != "" {
					stickers = filterStickers(stickers, query)
				}
				if len(stickers) == 0 {
					continue
				}
				sections = append(sections, stickerSection{name: pack.Name, stickers: stickers})
			}

			glib.IdleAdd(func() {
				clearBox(stickerBox)

				if len(sections) == 0 {
					label := gtk.NewLabel("No stickers found")
					label.AddCSSClass("mod-sticker-guild-header")
					stickerBox.Append(label)
					return
				}

				for _, sec := range sections {
					header := gtk.NewLabel(sec.name)
					header.AddCSSClass("mod-sticker-guild-header")
					header.SetXAlign(0)
					stickerBox.Append(header)
					stickerBox.Append(addStickerFlow(sec.stickers))
				}
			})
		}()
	}

	search.ConnectSearchChanged(func() { populate(search.Text()) })
	popover.ConnectShow(func() { populate(search.Text()) })

	return popover
}

func filterStickers(stickers []guildSticker, query string) []guildSticker {
	var out []guildSticker
	for _, s := range stickers {
		if s.matchesQuery(query) {
			out = append(out, s)
		}
	}
	return out
}
