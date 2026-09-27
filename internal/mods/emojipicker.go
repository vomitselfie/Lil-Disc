package mods

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	unicodeemoji "github.com/enescakir/emoji"
	"github.com/sahilm/fuzzy"
	"github.com/vomitselfie/Lil-Disc/internal/components/pickergrid"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableEmojiPicker = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Server Emoji Picker",
	Section:     "Mods",
	Description: "Custom emoji picker showing all server emojis organized by guild. Applies to channels opened afterwards.",
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
`)

const emojiPickerSize = 48

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

// newPickerPopover wraps a search entry and a grid in a popover. Enter in
// the entry picks the first result and Down moves into the grid, so a
// picker can be used without the mouse.
func newPickerPopover(grid *pickergrid.Grid, placeholder, class string, width, height int) (*gtk.SearchEntry, *gtk.Popover) {
	search := gtk.NewSearchEntry()
	search.AddCSSClass("mod-emoji-search")
	search.SetPlaceholderText(placeholder)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(search)
	content.Append(grid)
	emojiPickerCSS(content)

	popover := gtk.NewPopover()
	popover.AddCSSClass(class)
	popover.SetChild(content)
	popover.SetSizeRequest(width, height)

	grid.OnActivate(popover.Popdown)
	search.ConnectActivate(func() { grid.ActivateFirst() })
	search.ConnectStopSearch(popover.Popdown)

	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		if keyval == gdk.KEY_Down {
			return grid.FocusFirst()
		}
		return false
	})
	search.AddController(keys)

	return search, popover
}

func newEmojiGrid(ctx context.Context) *pickergrid.Grid {
	return pickergrid.New(ctx, pickergrid.Options{
		Columns:  6,
		CellSize: emojiPickerSize,
		Class:    "mod-emoji-grid",
	})
}

// emojiTarget says where picked emoji go and which are usable there.
type emojiTarget struct {
	state    *gtkcord.State
	guildID  discord.GuildID
	hasNitro bool
	// reaction limits custom emoji to the current guild without Nitro,
	// because a reaction cannot fall back on an image link.
	reaction bool

	pickCustom  func(em discord.Emoji, emojiGuild discord.GuildID)
	pickUnicode func(name, unicode string)
}

func customEmojiItem(em discord.Emoji, guildName string, pick func()) pickergrid.Item {
	return pickergrid.Item{
		ImageURL: gtkcord.EmojiURL(em.ID.String(), em.Animated),
		Label:    ":" + em.Name + ":",
		Detail:   guildName,
		Activate: pick,
	}
}

func unicodeEmojiItem(name, unicode string, pick func()) pickergrid.Item {
	return pickergrid.Item{
		Text:     unicode,
		Label:    strings.ReplaceAll(name, "_", " "),
		Activate: pick,
	}
}

// emojiSections builds the picker's contents for a query: recents (on an
// empty query), each guild's emoji, then Unicode emoji.
func emojiSections(t emojiTarget, query string) []pickergrid.Section {
	query = strings.ToLower(strings.TrimSpace(query))
	var sections []pickergrid.Section

	if query == "" {
		var recent []pickergrid.Item
		for _, r := range loadRecents() {
			r := r
			if r.Unicode != "" {
				recent = append(recent, unicodeEmojiItem(r.EmojiName, r.Unicode, func() {
					addRecent(r)
					t.pickUnicode(r.EmojiName, r.Unicode)
				}))
				continue
			}
			id := discord.EmojiID(mustSnowflake(r.EmojiID))
			if t.reaction && !t.hasNitro && !isEmojiInGuild(t.state, t.guildID, id) {
				continue
			}
			em := discord.Emoji{ID: id, Name: r.EmojiName, Animated: r.Animated}
			recent = append(recent, customEmojiItem(em, "", func() {
				addRecent(r)
				t.pickCustom(em, 0)
			}))
		}
		sections = append(sections, pickergrid.Section{Title: "Recent", Items: recent})
	}

	if guilds, err := t.state.EmojiState.AllEmojis(); err == nil {
		for _, guild := range guilds {
			if t.reaction && !t.hasNitro && guild.ID != t.guildID {
				continue
			}
			matched := guild.Emojis
			if query != "" {
				matched = filterEmojis(guild.Emojis, query)
			}
			items := make([]pickergrid.Item, 0, len(matched))
			for _, em := range matched {
				em := em
				gID := guild.ID
				items = append(items, customEmojiItem(em, guild.Name, func() {
					addRecent(recentEntry{EmojiID: em.ID.String(), EmojiName: em.Name, Animated: em.Animated})
					t.pickCustom(em, gID)
				}))
			}
			sections = append(sections, pickergrid.Section{Title: guild.Name, Items: items})
		}
	}

	var unicode []pickergrid.Item
	for _, e := range unicodeMatches(query) {
		e := e
		unicode = append(unicode, unicodeEmojiItem(e.Name, e.Unicode, func() {
			addRecent(recentEntry{EmojiName: e.Name, Unicode: e.Unicode})
			t.pickUnicode(e.Name, e.Unicode)
		}))
	}
	sections = append(sections, pickergrid.Section{Title: "Emoji", Items: unicode})

	return sections
}

type namedUnicode struct{ Name, Unicode string }

// unicodeMatches returns the curated set for an empty query, and otherwise
// every Unicode emoji whose name contains the query: names starting with it
// first, then alphabetically. The emoji table is a map, so without sorting
// the order changed on every keystroke.
func unicodeMatches(query string) []namedUnicode {
	if query == "" {
		out := make([]namedUnicode, len(commonUnicode))
		for i, e := range commonUnicode {
			out[i] = namedUnicode{e.Name, e.Unicode}
		}
		return out
	}

	var out []namedUnicode
	seen := make(map[string]bool)
	for name, unicode := range unicodeemoji.Map() {
		name = strings.Trim(strings.ToLower(name), ":")
		if !strings.Contains(name, query) || seen[unicode] {
			continue
		}
		seen[unicode] = true
		out = append(out, namedUnicode{name, unicode})
	}
	slices.SortFunc(out, func(a, b namedUnicode) int {
		ap, bp := strings.HasPrefix(a.Name, query), strings.HasPrefix(b.Name, query)
		if ap != bp {
			if ap {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
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
	target := emojiTarget{
		state:    state,
		guildID:  guildID,
		hasNitro: hasNitro,
		pickCustom: func(em discord.Emoji, emojiGuild discord.GuildID) {
			onPick(resolveEmojiPick(&em, emojiGuild, guildID, hasNitro))
		},
		pickUnicode: func(_, unicode string) {
			onPick(EmojiPickResult{Text: unicode, Reaction: discord.APIEmoji(unicode)})
		},
	}

	grid := newEmojiGrid(ctx)
	search, popover := newPickerPopover(grid, "Search emoji...", "mod-emoji-picker", 340, 400)

	populate := func() { grid.SetSections(emojiSections(target, search.Text())) }
	search.ConnectSearchChanged(populate)
	// Refresh recents each time the picker is shown.
	popover.ConnectShow(populate)

	return popover
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

	target := emojiTarget{
		state:    state,
		guildID:  guildID,
		hasNitro: state.EmojiState.HasNitro(),
		reaction: true,
		pickCustom: func(em discord.Emoji, _ discord.GuildID) {
			onPick(discord.NewAPIEmoji(em.ID, em.Name))
		},
		pickUnicode: func(_, unicode string) { onPick(discord.APIEmoji(unicode)) },
	}

	grid := newEmojiGrid(ctx)
	search, popover := newPickerPopover(grid, "Search emoji...", "mod-emoji-picker", 340, 400)

	populate := func() { grid.SetSections(emojiSections(target, search.Text())) }
	search.ConnectSearchChanged(populate)
	popover.ConnectShow(populate)

	return popover
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
