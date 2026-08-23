package mods

import (
	"context"
	"log/slog"
	"strings"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
	"github.com/dijama/lildisc/internal/gtkcord"
)

var enableSearch = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Message Search",
	Section:     "Mods",
	Description: "Add Ctrl+F message search within channels.",
})

var searchCSS = cssutil.Applier("mod-search", `
	.mod-search-entry {
		margin: 8px;
	}
	.mod-search-result {
		padding: {$space_md} {$space_lg};
		border-radius: {$radius_md};
		margin: 1px {$row_inset};
	}
	.mod-search-result:hover {
		background: @lil_hover;
	}
	.mod-search-author {
		font-weight: bold;
		margin-right: 8px;
	}
	.mod-search-time {
		font-size: {$font_small};
		color: @lil_text_faint;
	}
	.mod-search-content {
		margin-top: 2px;
	}
	.mod-search-status {
		padding: 12px;
		color: @lil_text_faint;
	}
`)

// InitSearch registers the search shortcut on the window.
// Called from HookState since we need Discord state for search.
func InitSearch(ctx context.Context, win ActionWidget) {
	if !enableSearch.Value() {
		return
	}

	gtkutil.AddActions(win, map[string]func(){
		"message-search": func() { showSearchDialog(ctx, win) },
	})

	gtkutil.AddActionShortcuts(win, map[string]string{
		"<Ctrl>F": "win.message-search",
	})
}

func showSearchDialog(ctx context.Context, win gtk.Widgetter) {
	state := gtkcord.FromContext(ctx)
	if state == nil {
		return
	}

	entry := gtk.NewSearchEntry()
	entry.AddCSSClass("mod-search-entry")
	entry.SetPlaceholderText("Search messages...")
	entry.SetHExpand(true)

	resultBox := gtk.NewBox(gtk.OrientationVertical, 0)

	statusLabel := gtk.NewLabel("Type to search messages in this channel")
	statusLabel.AddCSSClass("mod-search-status")
	resultBox.Append(statusLabel)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(resultBox)
	scroll.SetVExpand(true)

	contentBox := gtk.NewBox(gtk.OrientationVertical, 0)
	contentBox.Append(entry)
	contentBox.Append(scroll)
	searchCSS(contentBox)

	toolbarView := adw.NewToolbarView()
	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(true)
	toolbarView.AddTopBar(header)
	toolbarView.SetContent(contentBox)

	dialog := adw.NewDialog()
	dialog.SetTitle("Search Messages")
	dialog.SetContentWidth(500)
	dialog.SetContentHeight(450)
	dialog.SetChild(toolbarView)

	var searchTimeout glib.SourceHandle

	entry.ConnectSearchChanged(func() {
		if searchTimeout != 0 {
			glib.SourceRemove(searchTimeout)
		}

		query := entry.Text()
		if query == "" {
			clearResults(resultBox)
			statusLabel.SetText("Type to search messages in this channel")
			resultBox.Append(statusLabel)
			return
		}

		searchTimeout = glib.TimeoutAdd(400, func() {
			searchTimeout = 0
			performSearch(ctx, state, resultBox, query, dialog)
		})
	})

	// Handle Escape key to close
	keyCtrl := gtk.NewEventControllerKey()
	keyCtrl.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		if keyval == gdk.KEY_Escape {
			dialog.Close()
			return true
		}
		return false
	})
	entry.AddController(keyCtrl)

	base := gtk.BaseWidget(win)
	root := base.Root()
	if root != nil {
		dialog.Present(root)
		entry.GrabFocus()
	}
}

func performSearch(ctx context.Context, state *gtkcord.State, resultBox *gtk.Box, query string, dialog *adw.Dialog) {
	clearResults(resultBox)

	loading := gtk.NewLabel("Searching...")
	loading.AddCSSClass("mod-search-status")
	resultBox.Append(loading)

	// Get current channel from state context
	// We need to figure out which channel we're searching in.
	// For now, search across the guild or DMs.
	go func() {
		online := state.Online()

		// Try to find which guild/channel we might be searching in.
		// This is a simple implementation - search the whole guild context.
		// A more complete implementation would track the active channel.
		searchData := api.SearchData{
			Content: query,
		}

		// Try guild search first - we need a guild ID.
		// For a simple first implementation, we'll search using the messages
		// we have cached, or do a guild search if we can find the guild ID.
		var resp api.SearchResponse
		var err error

		// We don't have easy access to the "current channel" from here.
		// Use a simple local text search against cached messages as fallback.
		resp, err = searchCachedMessages(state, query)
		if err != nil {
			slog.Warn("search failed", "err", err)
		}

		_ = searchData // Will be used for API search in future
		_ = online

		glib.IdleAdd(func() {
			clearResults(resultBox)

			if len(resp.Messages) == 0 {
				noResults := gtk.NewLabel("No messages found")
				noResults.AddCSSClass("mod-search-status")
				resultBox.Append(noResults)
				return
			}

			for _, group := range resp.Messages {
				for _, msg := range group {
					row := createSearchResult(ctx, state, msg, dialog)
					resultBox.Append(row)
				}
			}
		})
	}()
}

const maxSearchResults = 25

func searchCachedMessages(state *gtkcord.State, query string) (api.SearchResponse, error) {
	offline := state.Offline()
	queryLower := strings.ToLower(query)
	matches := make([]discord.Message, 0, maxSearchResults)

	guilds, _ := offline.Cabinet.Guilds()
	for _, guild := range guilds {
		channels, _ := offline.Cabinet.Channels(guild.ID)
		for _, ch := range channels {
			msgs, _ := offline.Cabinet.Messages(ch.ID)
			for _, msg := range msgs {
				if !strings.Contains(strings.ToLower(msg.Content), queryLower) {
					continue
				}
				matches = append(matches, msg)
				if len(matches) >= maxSearchResults {
					return wrapSearchMatches(matches), nil
				}
			}
		}
	}

	return wrapSearchMatches(matches), nil
}

// wrapSearchMatches packages flat matches into the api.SearchResponse shape
// (one group containing all messages). Discord's real search API groups by
// context window; for our local cabinet search we only have isolated hits.
func wrapSearchMatches(matches []discord.Message) api.SearchResponse {
	if len(matches) == 0 {
		return api.SearchResponse{}
	}
	return api.SearchResponse{
		Messages:     [][]discord.Message{matches},
		TotalResults: uint(len(matches)),
	}
}

func createSearchResult(ctx context.Context, state *gtkcord.State, msg discord.Message, dialog *adw.Dialog) gtk.Widgetter {
	authorLabel := gtk.NewLabel("")
	authorLabel.AddCSSClass("mod-search-author")
	authorLabel.SetMarkup("<b>" + state.AuthorMarkup(&gateway.MessageCreateEvent{Message: msg}) + "</b>")

	timeLabel := gtk.NewLabel(locale.TimeAgo(msg.Timestamp.Time()))
	timeLabel.AddCSSClass("mod-search-time")

	headerBox := gtk.NewBox(gtk.OrientationHorizontal, 4)
	headerBox.Append(authorLabel)
	headerBox.Append(timeLabel)

	contentLabel := gtk.NewLabel(msg.Content)
	contentLabel.AddCSSClass("mod-search-content")
	contentLabel.SetXAlign(0)
	contentLabel.SetWrap(true)
	contentLabel.SetWrapMode(pango.WrapWordChar)
	contentLabel.SetEllipsize(pango.EllipsizeEnd)
	contentLabel.SetLines(2)

	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.AddCSSClass("mod-search-result")
	box.Append(headerBox)
	box.Append(contentLabel)

	// Click to open channel
	click := gtk.NewGestureClick()
	click.ConnectReleased(func(n int, x, y float64) {
		dialog.Close()
		app.FromContext(ctx).ActivateAction("open-channel", gtkcord.NewChannelIDVariant(msg.ChannelID))
	})
	box.AddController(click)

	return box
}

func clearResults(box *gtk.Box) {
	for {
		child := box.FirstChild()
		if child == nil {
			break
		}
		box.Remove(child)
	}
}
