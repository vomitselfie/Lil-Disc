package mods

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/vomitselfie/Lil-Disc/internal/asyncop"
	"github.com/vomitselfie/Lil-Disc/internal/components/pickergrid"
	"github.com/vomitselfie/Lil-Disc/internal/discordident"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableStickerPicker = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Sticker Picker",
	Section:     "Mods",
	Description: "Picker showing all available guild stickers, organized by server. Applies to channels opened afterwards.",
})

var stickerPickerCSS = lilcss.Applier("mod-sticker-picker", `
	.mod-sticker-picker {
		min-width: 380px;
		min-height: 420px;
	}
`)

const stickerPickerSize = 72

// How long sticker lists are trusted from the disk cache. Discord's own packs
// change rarely; a server's stickers change whenever its admins like.
const (
	guildStickerCacheAge = 24 * time.Hour
	stickerPackCacheAge  = 7 * 24 * time.Hour
)

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

	// Try disk cache first. It used to never expire, and nothing refreshed
	// it: arikawa does not model GUILD_STICKERS_UPDATE, so there is no event
	// to invalidate on, and a server's new stickers never appeared. A day's
	// age bounds how stale it can get.
	cacheFile := fmt.Sprintf("stickers_%s.json", guildID)
	var stickers []guildSticker
	if loadCachedJSON(cacheFile, guildStickerCacheAge, &stickers) {
		stickerCacheMu.Lock()
		stickerCache[guildID] = stickers
		stickerCacheMu.Unlock()
		return stickers, nil
	}

	// Fetch from API.
	url := fmt.Sprintf("https://discord.com/api/v9/guilds/%s/stickers", guildID)
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

	url := fmt.Sprintf("https://discord.com/api/v9/channels/%s/messages", channelID)
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
	if loadCachedJSON("default_sticker_packs.json", stickerPackCacheAge, &packs) {
		defaultPacksMu.Lock()
		defaultPacksCache = packs
		defaultPacksMu.Unlock()
		return packs
	}

	// Fetch from API.
	req, err := http.NewRequest("GET", "https://discord.com/api/v9/sticker-packs", nil)
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

// stickerSection is one guild's or pack's stickers.
type stickerSection struct {
	name     string
	stickers []guildSticker
}

// loadStickerCatalog fetches every sticker section the account can use here.
// Guild stickers come from the disk cache when warm; cold guilds are fetched
// with bounded concurrency, because a hundred sequential round trips would
// hold the picker for seconds.
func loadStickerCatalog(ctx context.Context, state *gtkcord.State, guildID discord.GuildID) []stickerSection {
	token := state.Token()

	var guildIDs []discord.GuildID
	if state.EmojiState.HasNitro() {
		if guilds, err := state.Cabinet.Guilds(); err == nil {
			for _, g := range guilds {
				guildIDs = append(guildIDs, g.ID)
			}
		}
	} else if guildID.IsValid() {
		// Without Nitro only the current guild's stickers can be sent.
		guildIDs = []discord.GuildID{guildID}
	}

	guildNames := make(map[discord.GuildID]string)
	if guilds, err := state.Cabinet.Guilds(); err == nil {
		for _, g := range guilds {
			guildNames[g.ID] = g.Name
		}
	}

	perGuild := make([]stickerSection, len(guildIDs))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i, gID := range guildIDs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, gID discord.GuildID) {
			defer wg.Done()
			defer func() { <-sem }()
			stickers, err := fetchGuildStickers(token, gID)
			if err != nil {
				slog.Debug("failed to fetch stickers", "guild", gID, "err", err)
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

	var sections []stickerSection
	for _, sec := range perGuild {
		if sec.name != "" {
			sections = append(sections, sec)
		}
	}
	// Default sticker packs are always available.
	for _, pack := range fetchDefaultStickerPacks(token) {
		sections = append(sections, stickerSection{name: pack.Name, stickers: pack.Stickers})
	}
	return sections
}

// NewStickerPickerPopover creates a sticker picker popover for the composer.
// guildID is the current guild context — non-Nitro users only see that guild's stickers.
//
// The catalog is loaded once each time the picker opens and then filtered
// locally as you type. It used to refetch everything per keystroke, with
// nothing to stop an older, slower fetch overwriting a newer one's results.
func NewStickerPickerPopover(ctx context.Context, guildID discord.GuildID, onPick func(StickerPickResult)) *gtk.Popover {
	if !enableStickerPicker.Value() {
		return nil
	}

	state := gtkcord.FromContext(ctx)
	if state == nil {
		return nil
	}

	grid := pickergrid.New(ctx, pickergrid.Options{
		Columns:  4,
		CellSize: stickerPickerSize,
		Class:    "mod-sticker-grid",
	})
	search, popover := newPickerPopover(grid, "Search stickers...", "mod-sticker-picker", 380, 420)
	stickerPickerCSS(popover)

	var (
		catalog []stickerSection
		loaded  bool
		latest  asyncop.Latest
	)

	render := func() {
		if !loaded {
			return
		}
		query := strings.ToLower(strings.TrimSpace(search.Text()))

		var sections []pickergrid.Section
		for _, sec := range catalog {
			stickers := sec.stickers
			if query != "" {
				stickers = filterStickers(stickers, query)
			}
			items := make([]pickergrid.Item, 0, len(stickers))
			for _, st := range stickers {
				st := st
				// Lottie stickers have no renderer here.
				if st.FormatType == 3 {
					continue
				}
				items = append(items, pickergrid.Item{
					ImageURL: st.staticURL(),
					Label:    st.Name,
					Detail:   st.Tags,
					Activate: func() { onPick(StickerPickResult{StickerID: st.ID, Name: st.Name}) },
				})
			}
			sections = append(sections, pickergrid.Section{Title: sec.name, Items: items})
		}

		if hasItems(sections) {
			grid.SetSections(sections)
		} else {
			grid.SetMessage("No stickers found")
		}
	}

	popover.ConnectShow(func() {
		opCtx, gen := latest.Begin(ctx)
		loaded = false
		grid.SetMessage("Loading stickers...")
		go func() {
			sections := loadStickerCatalog(opCtx, state, guildID)
			glib.IdleAdd(func() {
				if !latest.IsCurrent(gen) {
					return
				}
				catalog, loaded = sections, true
				render()
			})
		}()
	})
	popover.ConnectHide(latest.Cancel)
	search.ConnectSearchChanged(render)

	return popover
}

func hasItems(sections []pickergrid.Section) bool {
	for _, sec := range sections {
		if len(sec.Items) > 0 {
			return true
		}
	}
	return false
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
