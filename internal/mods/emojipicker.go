package mods

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/diamondburned/arikawa/v3/discord"
	unicodeemoji "github.com/enescakir/emoji"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"
	"github.com/sahilm/fuzzy"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableEmojiPicker = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Server Emoji Picker",
	Section:     "Mods",
	Description: "Custom emoji picker showing all server emojis organized by guild.",
})

var enableFakeNitro = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Nitro-Free Emoji (FakeNitro)",
	Section:     "Mods",
	Description: "Send external server emojis as image URLs when you don't have Nitro. May violate Discord's Terms of Service.",
})

var emojiPickerCSS = lilcss.Applier("mod-emoji-picker", `
	.mod-emoji-picker {
		min-width: 340px;
		min-height: 400px;
	}
	.mod-emoji-search {
		margin: {$space_md};
	}
	.mod-emoji-grid {
		padding: {$space_xs};
	}
	.mod-emoji-guild-header {
		font-weight: bold;
		font-size: {$font_micro};
		color: @lil_text_faint;
		padding: {$space_md} {$space_md} {$space_xs} {$space_md};
	}
	.mod-emoji-item {
		padding: {$space_xs};
		border-radius: {$radius_md};
	}
	.mod-emoji-item:hover {
		background: @lil_hover;
	}
	.mod-emoji-item image,
	.mod-emoji-item .onlineimage {
		background: transparent;
	}
	.mod-emoji-unicode {
		font-size: 28px;
	}
`)

const emojiPickerSize = 48

// maxEmojiInitialRender caps how many custom emoji are realised on an empty
// query. A user in many servers can have 5000+ emoji; rendering all of them
// upfront stalls the GTK main thread. The full set is still reachable by
// typing — search filters cheaply and only renders matches.
const maxEmojiInitialRender = 200

// EmojiPickResult contains the result of an emoji selection.
type EmojiPickResult struct {
	Text     string           // message text to insert
	Reaction discord.APIEmoji // API format for reactions
}

// --- Recents ---

const maxRecents = 24

var (
	recentsMu    sync.Mutex
	recentsCache []recentEntry
	recentsPath  string
)

type recentEntry struct {
	// For custom emoji:
	EmojiID   string `json:"id,omitempty"`
	EmojiName string `json:"name"`
	Animated  bool   `json:"animated,omitempty"`
	// For Unicode emoji:
	Unicode string `json:"unicode,omitempty"`
}

func recentsFile() string {
	if recentsPath != "" {
		return recentsPath
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	recentsPath = filepath.Join(dir, "lildisc", "emoji_recents.json")
	return recentsPath
}

func loadRecents() []recentEntry {
	recentsMu.Lock()
	defer recentsMu.Unlock()

	if recentsCache != nil {
		return recentsCache
	}

	path := recentsFile()
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	json.Unmarshal(data, &recentsCache)
	return recentsCache
}

func addRecent(entry recentEntry) {
	recentsMu.Lock()
	defer recentsMu.Unlock()

	// Remove duplicate if exists.
	key := entry.Unicode
	if key == "" {
		key = entry.EmojiID
	}
	filtered := make([]recentEntry, 0, len(recentsCache)+1)
	for _, e := range recentsCache {
		k := e.Unicode
		if k == "" {
			k = e.EmojiID
		}
		if k != key {
			filtered = append(filtered, e)
		}
	}

	// Prepend new entry.
	recentsCache = append([]recentEntry{entry}, filtered...)
	if len(recentsCache) > maxRecents {
		recentsCache = recentsCache[:maxRecents]
	}

	// Persist.
	path := recentsFile()
	if path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := json.Marshal(recentsCache)
	os.WriteFile(path, data, 0o644)
}

// --- Common Unicode emoji list (popular subset) ---

var commonUnicode = []struct {
	Name    string
	Unicode string
}{
	{"thumbs_up", "👍"}, {"thumbs_down", "👎"}, {"heart", "❤️"},
	{"joy", "😂"}, {"fire", "🔥"}, {"eyes", "👀"},
	{"thinking", "🤔"}, {"100", "💯"}, {"clap", "👏"},
	{"wave", "👋"}, {"pray", "🙏"}, {"skull", "💀"},
	{"sob", "😭"}, {"rocket", "🚀"}, {"tada", "🎉"},
	{"ok_hand", "👌"}, {"raised_hands", "🙌"}, {"star_struck", "🤩"},
	{"rolling_eyes", "🙄"}, {"smirk", "😏"}, {"sunglasses", "😎"},
	{"cry", "😢"}, {"scream", "😱"}, {"angry", "😠"},
	{"sparkles", "✨"}, {"check", "✅"}, {"x", "❌"},
	{"warning", "⚠️"}, {"question", "❓"}, {"exclamation", "❗"},
	{"zzz", "💤"}, {"musical_note", "🎵"}, {"crown", "👑"},
	{"gem", "💎"}, {"rainbow", "🌈"}, {"sun", "☀️"},
	{"moon", "🌙"}, {"star", "⭐"}, {"cloud", "☁️"},
	{"pizza", "🍕"}, {"beer", "🍺"}, {"coffee", "☕"},
}

// --- Shared picker builder ---

func clearBox(box *gtk.Box) {
	for {
		child := box.FirstChild()
		if child == nil {
			break
		}
		box.Remove(child)
	}
}

func addSection(box *gtk.Box, title string) {
	header := gtk.NewLabel(title)
	header.AddCSSClass("mod-emoji-guild-header")
	header.SetXAlign(0)
	box.Append(header)
}

func newFlow() *gtk.FlowBox {
	flow := gtk.NewFlowBox()
	flow.SetSelectionMode(gtk.SelectionNone)
	flow.SetMaxChildrenPerLine(8)
	flow.SetMinChildrenPerLine(4)
	flow.SetHomogeneous(true)
	return flow
}

func newUnicodeButton(name, unicode string, onClick func(), popover *gtk.Popover) gtk.Widgetter {
	label := gtk.NewLabel(unicode)
	label.AddCSSClass("mod-emoji-unicode")

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("mod-emoji-item")
	box.Append(label)
	box.SetTooltipText(name)

	click := gtk.NewGestureClick()
	click.ConnectReleased(func(n int, x, y float64) {
		onClick()
		popover.Popdown()
	})
	box.AddController(click)

	return box
}

func newCustomEmojiButton(ctx context.Context, em *discord.Emoji, guildName string, onClick func(), popover *gtk.Popover) gtk.Widgetter {
	img := onlineimage.NewPicture(ctx, imgutil.HTTPProvider)
	img.SetSizeRequest(emojiPickerSize, emojiPickerSize)
	img.SetContentFit(gtk.ContentFitContain)
	img.SetURL(gtkcord.EmojiURL(em.ID.String(), em.Animated))

	tooltip := html.EscapeString(em.Name)
	if guildName != "" {
		tooltip += "\n" + fmt.Sprintf(
			`<span size="smaller" fgalpha="75%%">%s</span>`,
			html.EscapeString(guildName),
		)
	}

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("mod-emoji-item")
	box.Append(img)
	box.SetTooltipMarkup(tooltip)

	click := gtk.NewGestureClick()
	click.ConnectReleased(func(n int, x, y float64) {
		onClick()
		popover.Popdown()
	})
	box.AddController(click)

	return box
}

func buildPickerShell() (*gtk.SearchEntry, *gtk.Box, *gtk.Popover) {
	search := gtk.NewSearchEntry()
	search.AddCSSClass("mod-emoji-search")
	search.SetPlaceholderText("Search emoji...")

	emojiBox := gtk.NewBox(gtk.OrientationVertical, 0)
	emojiBox.AddCSSClass("mod-emoji-grid")

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(emojiBox)
	scroll.SetVExpand(true)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(search)
	content.Append(scroll)
	emojiPickerCSS(content)

	popover := gtk.NewPopover()
	popover.AddCSSClass("mod-emoji-picker")
	popover.SetChild(content)
	popover.SetSizeRequest(340, 400)

	return search, emojiBox, popover
}

// --- Message emoji picker ---

// NewEmojiPickerPopover creates a custom emoji picker for composing messages.
func NewEmojiPickerPopover(ctx context.Context, guildID discord.GuildID, onPick func(EmojiPickResult)) *gtk.Popover {
	if !enableEmojiPicker.Value() {
		return nil
	}

	state := gtkcord.FromContext(ctx)
	if state == nil {
		return nil
	}

	hasNitro := state.EmojiState.HasNitro()
	search, emojiBox, popover := buildPickerShell()

	populate := func(query string) {
		clearBox(emojiBox)
		recents := loadRecents()
		query = strings.ToLower(query)

		// Recents section
		if query == "" && len(recents) > 0 {
			addSection(emojiBox, "Recent")
			flow := newFlow()
			for _, r := range recents {
				r := r
				if r.Unicode != "" {
					flow.Append(newUnicodeButton(r.EmojiName, r.Unicode, func() {
						addRecent(r)
						onPick(EmojiPickResult{
							Text:     r.Unicode,
							Reaction: discord.APIEmoji(r.Unicode),
						})
					}, popover))
				} else {
					em := &discord.Emoji{
						ID:       discord.EmojiID(mustSnowflake(r.EmojiID)),
						Name:     r.EmojiName,
						Animated: r.Animated,
					}
					flow.Append(newCustomEmojiButton(ctx, em, "", func() {
						addRecent(r)
						result := resolveEmojiPick(em, 0, guildID, hasNitro)
						onPick(result)
					}, popover))
				}
			}
			emojiBox.Append(flow)
		}

		// Server emoji
		guilds, err := state.EmojiState.AllEmojis()
		if err == nil {
			rendered := 0
			truncated := false
			for _, guild := range guilds {
				var matched []discord.Emoji
				if query == "" {
					matched = guild.Emojis
					if remaining := maxEmojiInitialRender - rendered; remaining <= 0 {
						truncated = true
						break
					} else if len(matched) > remaining {
						matched = matched[:remaining]
						truncated = true
					}
				} else {
					matched = filterEmojis(guild.Emojis, query)
				}
				if len(matched) == 0 {
					continue
				}

				addSection(emojiBox, guild.Name)
				flow := newFlow()
				for _, em := range matched {
					em := em
					gName := guild.Name
					gID := guild.ID
					flow.Append(newCustomEmojiButton(ctx, &em, gName, func() {
						addRecent(recentEntry{
							EmojiID:   em.ID.String(),
							EmojiName: em.Name,
							Animated:  em.Animated,
						})
						result := resolveEmojiPick(&em, gID, guildID, hasNitro)
						onPick(result)
					}, popover))
				}
				emojiBox.Append(flow)
				rendered += len(matched)
			}
			if truncated {
				appendTruncationHint(emojiBox)
			}
		}

		// Unicode emoji
		addUnicodeSection(emojiBox, query, func(name, unicode string) {
			addRecent(recentEntry{EmojiName: name, Unicode: unicode})
			onPick(EmojiPickResult{
				Text:     unicode,
				Reaction: discord.APIEmoji(unicode),
			})
		}, popover)
	}

	populate("")
	search.ConnectSearchChanged(func() { populate(search.Text()) })
	// Refresh recents each time the picker is shown.
	popover.ConnectShow(func() { populate(search.Text()) })

	return popover
}

// appendTruncationHint adds a muted footer label indicating more results are
// available via search. Used when the empty-query render is capped.
func appendTruncationHint(box *gtk.Box) {
	hint := gtk.NewLabel("Type to search for more emoji")
	hint.AddCSSClass("mod-emoji-guild-header")
	hint.SetXAlign(0)
	box.Append(hint)
}

// --- Reaction emoji picker ---

// NewReactionPickerPopover creates an emoji picker for adding reactions.
func NewReactionPickerPopover(ctx context.Context, guildID discord.GuildID, onPick func(discord.APIEmoji)) *gtk.Popover {
	if !enableEmojiPicker.Value() {
		return nil
	}

	state := gtkcord.FromContext(ctx)
	if state == nil {
		return nil
	}

	hasNitro := state.EmojiState.HasNitro()
	search, emojiBox, popover := buildPickerShell()

	populate := func(query string) {
		clearBox(emojiBox)
		recents := loadRecents()
		query = strings.ToLower(query)

		// Recents section
		if query == "" && len(recents) > 0 {
			addSection(emojiBox, "Recent")
			flow := newFlow()
			for _, r := range recents {
				r := r
				if r.Unicode != "" {
					flow.Append(newUnicodeButton(r.EmojiName, r.Unicode, func() {
						addRecent(r)
						onPick(discord.APIEmoji(r.Unicode))
					}, popover))
				} else {
					// Skip cross-guild custom emoji for non-Nitro
					if !hasNitro {
						id := discord.EmojiID(mustSnowflake(r.EmojiID))
						if !isEmojiInGuild(state, guildID, id) {
							continue
						}
					}
					em := &discord.Emoji{
						ID:       discord.EmojiID(mustSnowflake(r.EmojiID)),
						Name:     r.EmojiName,
						Animated: r.Animated,
					}
					flow.Append(newCustomEmojiButton(ctx, em, "", func() {
						addRecent(r)
						onPick(discord.NewAPIEmoji(em.ID, em.Name))
					}, popover))
				}
			}
			emojiBox.Append(flow)
		}

		// Server emoji (filtered by guild for non-Nitro)
		guilds, err := state.EmojiState.AllEmojis()
		if err == nil {
			rendered := 0
			truncated := false
			for _, guild := range guilds {
				if !hasNitro && guild.ID != guildID {
					continue
				}

				var matched []discord.Emoji
				if query == "" {
					matched = guild.Emojis
					if remaining := maxEmojiInitialRender - rendered; remaining <= 0 {
						truncated = true
						break
					} else if len(matched) > remaining {
						matched = matched[:remaining]
						truncated = true
					}
				} else {
					matched = filterEmojis(guild.Emojis, query)
				}
				if len(matched) == 0 {
					continue
				}

				addSection(emojiBox, guild.Name)
				flow := newFlow()
				for _, em := range matched {
					em := em
					flow.Append(newCustomEmojiButton(ctx, &em, guild.Name, func() {
						addRecent(recentEntry{
							EmojiID:   em.ID.String(),
							EmojiName: em.Name,
							Animated:  em.Animated,
						})
						onPick(discord.NewAPIEmoji(em.ID, em.Name))
					}, popover))
				}
				emojiBox.Append(flow)
				rendered += len(matched)
			}
			if truncated {
				appendTruncationHint(emojiBox)
			}
		}

		// Unicode emoji (always available for reactions)
		addUnicodeSection(emojiBox, query, func(name, unicode string) {
			addRecent(recentEntry{EmojiName: name, Unicode: unicode})
			onPick(discord.APIEmoji(unicode))
		}, popover)
	}

	populate("")
	search.ConnectSearchChanged(func() { populate(search.Text()) })
	popover.ConnectShow(func() { populate(search.Text()) })

	return popover
}

// --- Unicode emoji section ---

func addUnicodeSection(box *gtk.Box, query string, onPick func(name, unicode string), popover *gtk.Popover) {
	if query == "" {
		// Show curated common set
		addSection(box, "Emoji")
		flow := newFlow()
		for _, e := range commonUnicode {
			e := e
			flow.Append(newUnicodeButton(e.Name, e.Unicode, func() {
				onPick(e.Name, e.Unicode)
			}, popover))
		}
		box.Append(flow)
	} else {
		// Search full Unicode emoji set
		allEmoji := unicodeemoji.Map()
		var matches []struct{ name, unicode string }
		for name, unicode := range allEmoji {
			if strings.Contains(strings.ToLower(name), query) {
				matches = append(matches, struct{ name, unicode string }{name, unicode})
			}
			if len(matches) >= 50 {
				break // cap results
			}
		}
		if len(matches) > 0 {
			addSection(box, "Emoji")
			flow := newFlow()
			for _, m := range matches {
				m := m
				flow.Append(newUnicodeButton(m.name, m.unicode, func() {
					onPick(m.name, m.unicode)
				}, popover))
			}
			box.Append(flow)
		}
	}
}

// --- Helpers ---

func isEmojiInGuild(state *gtkcord.State, guildID discord.GuildID, emojiID discord.EmojiID) bool {
	guilds, err := state.EmojiState.AllEmojis()
	if err != nil {
		return false
	}
	for _, g := range guilds {
		if g.ID != guildID {
			continue
		}
		for _, e := range g.Emojis {
			if e.ID == emojiID {
				return true
			}
		}
	}
	return false
}

func mustSnowflake(s string) discord.Snowflake {
	sf, _ := discord.ParseSnowflake(s)
	return sf
}

func filterEmojis(emojis []discord.Emoji, query string) []discord.Emoji {
	names := make(emojiNames, len(emojis))
	for i, e := range emojis {
		names[i] = e.Name
	}

	results := fuzzy.FindFrom(query, names)
	filtered := make([]discord.Emoji, 0, len(results))
	for _, r := range results {
		filtered = append(filtered, emojis[r.Index])
	}
	return filtered
}

type emojiNames []string

func (e emojiNames) Len() int            { return len(e) }
func (e emojiNames) String(i int) string { return e[i] }

func resolveEmojiPick(em *discord.Emoji, emojiGuild, currentGuild discord.GuildID, hasNitro bool) EmojiPickResult {
	reaction := discord.NewAPIEmoji(em.ID, em.Name)

	// When FakeNitro is enabled, only use Discord's emoji syntax if the
	// emoji belongs to the current guild (where it's always usable).
	// For cross-server/DM usage, always send the CDN URL instead.
	canUseNatively := emojiGuild == currentGuild && currentGuild.IsValid()
	if !enableFakeNitro.Value() {
		canUseNatively = canUseNatively || hasNitro
	}
	if canUseNatively {
		if em.Animated {
			return EmojiPickResult{
				Text:     fmt.Sprintf("<a:%s:%s>", em.Name, em.ID),
				Reaction: reaction,
			}
		}
		return EmojiPickResult{
			Text:     fmt.Sprintf("<:%s:%s>", em.Name, em.ID),
			Reaction: reaction,
		}
	}

	ext := "png"
	size := 128
	if em.Animated {
		ext = "gif"
		size = 256
	}
	return EmojiPickResult{
		Text:     fmt.Sprintf("https://cdn.discordapp.com/emojis/%s.%s?size=%d&quality=lossless", em.ID, ext, size),
		Reaction: reaction,
	}
}
