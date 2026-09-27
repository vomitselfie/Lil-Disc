package mods

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/vomitselfie/Lil-Disc/internal/asyncop"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableSearch = prefs.NewBool(true, prefs.PropMeta{
	Name:    "Message Search",
	Section: "Mods",
	Description: "Ctrl+F searches the open channel, Ctrl+Shift+F searches " +
		"every message LilDisc has cached.",
})

var searchCSS = lilcss.Applier("mod-search", `
	.mod-search-entry {
		margin: {$space_md};
	}
	.mod-search-scope {
		margin: 0 {$space_lg} {$space_xs} {$space_lg};
		font-size: {$font_small};
		color: @lil_text_faint;
	}
	button.mod-search-result {
		padding: {$space_md} {$space_lg};
		border-radius: {$radius_md};
		margin: 1px {$row_inset};
		background: none;
	}
	button.mod-search-result:hover,
	button.mod-search-result:focus-visible {
		background: @lil_hover;
	}
	.mod-search-author {
		font-weight: bold;
		margin-right: {$space_md};
	}
	.mod-search-where,
	.mod-search-time {
		font-size: {$font_small};
		color: @lil_text_faint;
	}
	.mod-search-content {
		margin-top: {$space_hair};
	}
	.mod-search-status {
		padding: {$space_lg};
		color: @lil_text_faint;
	}
`)

// searchScope is what a search covers.
type searchScope int

const (
	// scopeChannel is the open channel: cached messages as you type, then
	// Discord's server search on Enter.
	scopeChannel searchScope = iota
	// scopeCached is every message LilDisc has cached, across channels.
	scopeCached
)

// maxSearchResults caps each result list.
const maxSearchResults = 50

// InitSearch registers the search shortcuts on the window.
// Called from HookState since we need Discord state for search.
func InitSearch(ctx context.Context, win ActionWidget) {
	if !enableSearch.Value() {
		return
	}

	gtkutil.AddActions(win, map[string]func(){
		"message-search": func() {
			if ch := ActiveChannel(); ch.IsValid() {
				showSearchDialog(ctx, win, scopeChannel, ch)
			} else {
				showSearchDialog(ctx, win, scopeCached, 0)
			}
		},
		"message-search-all": func() { showSearchDialog(ctx, win, scopeCached, 0) },
	})

	gtkutil.AddActionShortcuts(win, map[string]string{
		"<Ctrl>F":        "win.message-search",
		"<Ctrl><Shift>F": "win.message-search-all",
	})
}

type searchDialog struct {
	ctx    context.Context
	state  *gtkcord.State
	scope  searchScope
	chID   discord.ChannelID
	chName string

	dialog  *adw.Dialog
	entry   *gtk.SearchEntry
	scopeLb *gtk.Label
	results *gtk.Box

	latest asyncop.Latest
}

func showSearchDialog(ctx context.Context, win gtk.Widgetter, scope searchScope, chID discord.ChannelID) {
	state := gtkcord.FromContext(ctx)
	if state == nil {
		return
	}

	d := &searchDialog{ctx: ctx, state: state, scope: scope, chID: chID}
	if scope == scopeChannel {
		d.chName = gtkcord.ChannelNameFromID(ctx, chID)
	}

	d.entry = gtk.NewSearchEntry()
	d.entry.AddCSSClass("mod-search-entry")
	d.entry.SetHExpand(true)

	d.scopeLb = gtk.NewLabel("")
	d.scopeLb.AddCSSClass("mod-search-scope")
	d.scopeLb.SetXAlign(0)
	d.scopeLb.SetWrap(true)

	d.results = gtk.NewBox(gtk.OrientationVertical, 0)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(d.results)
	scroll.SetVExpand(true)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(d.entry)
	content.Append(d.scopeLb)
	content.Append(scroll)
	searchCSS(content)

	toolbarView := adw.NewToolbarView()
	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(true)
	toolbarView.AddTopBar(header)
	toolbarView.SetContent(content)

	d.dialog = adw.NewDialog()
	d.dialog.SetContentWidth(520)
	d.dialog.SetContentHeight(480)
	d.dialog.SetChild(toolbarView)
	d.dialog.ConnectClosed(d.latest.Cancel)

	if scope == scopeChannel {
		d.dialog.SetTitle(locale.Sprintf("Search %s", d.chName))
		d.entry.SetPlaceholderText(locale.Get("Search this channel…"))
	} else {
		d.dialog.SetTitle(locale.Get("Search Cached Messages"))
		d.entry.SetPlaceholderText(locale.Get("Search every cached message…"))
	}
	d.showIdle()

	var debounce glib.SourceHandle
	d.entry.ConnectSearchChanged(func() {
		if debounce != 0 {
			glib.SourceRemove(debounce)
			debounce = 0
		}
		query := strings.TrimSpace(d.entry.Text())
		if query == "" {
			d.latest.Cancel()
			d.showIdle()
			return
		}
		// Local search is cheap, so the debounce only smooths typing.
		debounce = glib.TimeoutAdd(150, func() {
			debounce = 0
			d.searchLocal(query)
		})
	})
	d.entry.ConnectActivate(func() {
		query := strings.TrimSpace(d.entry.Text())
		if query == "" {
			return
		}
		if d.scope == scopeChannel {
			d.searchServer(query)
		} else {
			d.focusFirstResult()
		}
	})
	d.entry.ConnectStopSearch(func() { d.dialog.Close() })

	if root := gtk.BaseWidget(win).Root(); root != nil {
		d.dialog.Present(root)
		d.entry.GrabFocus()
	}
}

func (d *searchDialog) showIdle() {
	d.clear()
	if d.scope == scopeChannel {
		d.scopeLb.SetText(locale.Get("Type to filter recent messages. Press Enter to search the whole channel on Discord."))
	} else {
		d.scopeLb.SetText(locale.Get("Searches messages LilDisc has already loaded, in every channel, newest first."))
	}
}

// searchLocal filters cached messages. It is fast and never touches the
// network, so it runs as you type.
func (d *searchDialog) searchLocal(query string) {
	_, gen := d.latest.Begin(d.ctx)

	var matches []discord.Message
	if d.scope == scopeChannel {
		matches = matchCached(d.state, []discord.ChannelID{d.chID}, query)
	} else {
		matches = matchCached(d.state, cachedChannels(d.state), query)
	}

	if !d.latest.IsCurrent(gen) {
		return
	}

	if d.scope == scopeChannel {
		d.scopeLb.SetText(locale.Sprintf(
			"%d recent matches. Press Enter to search all of %s on Discord.",
			len(matches), d.chName))
	} else {
		d.scopeLb.SetText(locale.Sprintf("%d matches in cached messages.", len(matches)))
	}
	d.show(matches, d.scope == scopeCached)
}

// searchServer runs Discord's search for the open channel.
func (d *searchDialog) searchServer(query string) {
	ctx, gen := d.latest.Begin(d.ctx)

	d.clear()
	d.status(locale.Get("Searching Discord…"))

	client := d.state.Online().WithContext(ctx)
	chID := d.chID

	go func() {
		data := api.SearchData{Content: query, ChannelID: chID}

		var resp api.SearchResponse
		var err error
		ch, _ := d.state.Cabinet.Channel(chID)
		if ch != nil && ch.GuildID.IsValid() {
			resp, err = client.Search(ch.GuildID, data)
		} else {
			resp, err = client.SearchDirectMessages(data)
		}

		glib.IdleAdd(func() {
			if !d.latest.IsCurrent(gen) {
				return
			}
			if err != nil {
				slog.Warn("message search failed", "channel", chID, "err", err)
				d.clear()
				d.status(locale.Get("Discord search failed. The results below are from cache."))
				d.show(matchCached(d.state, []discord.ChannelID{chID}, query), false)
				return
			}

			// Discord returns each hit inside a small context window; the
			// hit is the one flagged, or the only entry.
			var hits []discord.Message
			for _, group := range resp.Messages {
				hits = append(hits, searchHit(group))
			}
			d.scopeLb.SetText(locale.Sprintf(
				"%d results on Discord, newest first.", resp.TotalResults))
			d.show(hits, false)
		})
	}()
}

// searchHit picks the matched message out of a result's context window.
// Discord marks it with "hit": true, which arikawa does not model, so fall
// back on the middle entry, which is where Discord puts it.
func searchHit(group []discord.Message) discord.Message {
	if len(group) == 0 {
		return discord.Message{}
	}
	return group[len(group)/2]
}

// matchCached returns cached messages in channels whose content contains
// query, case-insensitively, newest first.
func matchCached(state *gtkcord.State, channels []discord.ChannelID, query string) []discord.Message {
	offline := state.Offline()
	needle := strings.ToLower(query)

	var matches []discord.Message
	for _, chID := range channels {
		msgs, _ := offline.Cabinet.Messages(chID)
		for _, msg := range msgs {
			if strings.Contains(strings.ToLower(msg.Content), needle) {
				matches = append(matches, msg)
			}
		}
	}

	// Snowflakes sort by time, so this is newest first across channels.
	slices.SortFunc(matches, func(a, b discord.Message) int {
		switch {
		case a.ID > b.ID:
			return -1
		case a.ID < b.ID:
			return 1
		}
		return 0
	})
	if len(matches) > maxSearchResults {
		matches = matches[:maxSearchResults]
	}
	return matches
}

// cachedChannels lists every guild and private channel in the cabinet.
func cachedChannels(state *gtkcord.State) []discord.ChannelID {
	offline := state.Offline()

	var ids []discord.ChannelID
	if private, err := offline.Cabinet.PrivateChannels(); err == nil {
		for _, ch := range private {
			ids = append(ids, ch.ID)
		}
	}
	guilds, _ := offline.Cabinet.Guilds()
	for _, guild := range guilds {
		channels, _ := offline.Cabinet.Channels(guild.ID)
		for _, ch := range channels {
			ids = append(ids, ch.ID)
		}
	}
	return ids
}

func (d *searchDialog) show(msgs []discord.Message, withChannel bool) {
	d.clear()
	if len(msgs) == 0 {
		d.status(locale.Get("No messages found"))
		return
	}
	for _, msg := range msgs {
		if msg.ID.IsValid() {
			d.results.Append(d.newResult(msg, withChannel))
		}
	}
}

func (d *searchDialog) status(text string) {
	label := gtk.NewLabel(text)
	label.AddCSSClass("mod-search-status")
	label.SetWrap(true)
	d.results.Append(label)
}

func (d *searchDialog) clear() {
	for child := d.results.FirstChild(); child != nil; child = d.results.FirstChild() {
		d.results.Remove(child)
	}
}

func (d *searchDialog) focusFirstResult() {
	if first := d.results.FirstChild(); first != nil {
		gtk.BaseWidget(first).GrabFocus()
	}
}

// newResult builds one result. It is a real button, so results can be
// reached with Tab or the arrow keys and opened with Enter, and screen
// readers announce them as buttons.
func (d *searchDialog) newResult(msg discord.Message, withChannel bool) gtk.Widgetter {
	authorLabel := gtk.NewLabel("")
	authorLabel.AddCSSClass("mod-search-author")
	authorLabel.SetMarkup(d.state.AuthorMarkup(&gateway.MessageCreateEvent{Message: msg}))

	header := gtk.NewBox(gtk.OrientationHorizontal, 0)
	header.Append(authorLabel)

	if withChannel {
		where := gtk.NewLabel(gtkcord.ChannelNameFromID(d.ctx, msg.ChannelID) + " · ")
		where.AddCSSClass("mod-search-where")
		header.Append(where)
	}

	timeLabel := gtk.NewLabel(locale.TimeAgo(msg.Timestamp.Time()))
	timeLabel.AddCSSClass("mod-search-time")
	header.Append(timeLabel)

	text := msg.Content
	if text == "" && len(msg.Attachments) > 0 {
		text = fmt.Sprintf("[%s]", msg.Attachments[0].Filename)
	}
	contentLabel := gtk.NewLabel(text)
	contentLabel.AddCSSClass("mod-search-content")
	contentLabel.SetXAlign(0)
	contentLabel.SetWrap(true)
	contentLabel.SetWrapMode(pango.WrapWordChar)
	contentLabel.SetEllipsize(pango.EllipsizeEnd)
	contentLabel.SetLines(2)

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.Append(header)
	box.Append(contentLabel)

	button := gtk.NewButton()
	button.AddCSSClass("mod-search-result")
	button.SetChild(box)

	loc := gtkcord.MessageLocation{ChannelID: msg.ChannelID, MessageID: msg.ID}
	button.ConnectClicked(func() {
		d.dialog.Close()
		app.FromContext(d.ctx).ActivateAction("open-message", loc.Variant())
	})
	return button
}
