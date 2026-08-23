package direct

import (
	"context"
	"log/slog"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
	"github.com/diamondburned/ningen/v3/states/read"
	"github.com/dijama/lildisc/internal/gtkcord"
	"github.com/dijama/lildisc/internal/mods"
)

// ChannelView displays a list of direct messaging channels.
// mod: compactsidebar — embeds *adw.ToolbarView directly (like channels.View)
// instead of *adaptive.LoadablePage, so the DM list fills the sidebar width.
type ChannelView struct {
	*adw.ToolbarView

	scroll  *gtk.ScrolledWindow
	list    *gtk.ListBox
	spinner *gtk.Spinner

	// mod: friendlist — collapsible "Friends (N)" section appended under
	// the active DM list. nil if the feature is disabled in preferences.
	friendsExpander *mods.FriendsExpander

	searchBar    *gtk.SearchBar
	searchEntry  *gtk.SearchEntry
	searchString string

	ctx      context.Context
	channels map[discord.ChannelID]*Channel
	selectID discord.ChannelID // delegate to be selected later
}

var _ = cssutil.WriteCSS(`
	.direct-searchbar > revealer > box {
		border-bottom: 0;
		background: none;
		box-shadow: none;
	}
	.direct-searchbar > revealer > box > entry {
		min-height: 28px;
	}
	/* mod: compactsidebar — replaces navigation-sidebar styling */
	.direct-list {
		padding: {$space_sm} 0;
		background: transparent;
	}
	.direct-list > row {
		min-height: {$row_height};
		border-radius: {$radius_md};
		margin: 1px {$row_inset};
	}
	/* Previously this list had a :selected rule and no :hover, while the
	   friend list right below it had :hover and no :selected. Both now carry
	   the full set, using the same states as the channel tree. */
	.direct-list > row:hover {
		background-color: @lil_hover;
	}
	.direct-list > row:selected {
		background-color: @lil_selected;
	}
	.direct-list > row:selected:hover {
		background-color: @lil_active;
	}
	/* The display name is what you scan the list for, so it stays at full
	   strength; the hierarchy against the handle is carried by the handle
	   being smaller and dimmer, not by fading the name. */
	.direct-channel-name {
		color: @lil_text;
	}
	/* The account handle under the display name. Tight leading keeps the two
	   lines reading as one block rather than as two rows. */
	.direct-channel-subname {
		font-size: {$font_small};
		color: @lil_text_dim;
		line-height: {$line_tight};
	}
`)

// NewChannelView creates a new view.
func NewChannelView(ctx context.Context) *ChannelView {
	v := ChannelView{
		ctx:      ctx,
		channels: make(map[discord.ChannelID]*Channel, 50),
	}

	v.list = gtk.NewListBox()
	// mod: compactsidebar — removed "navigation-sidebar" class which constrains
	// row widths; we style the list ourselves for full-width name labels.
	v.list.SetCSSClasses([]string{"direct-list"})
	v.list.SetHExpand(true)
	v.list.SetSortFunc(v.sort)
	v.list.SetFilterFunc(v.filter)
	v.list.SetSelectionMode(gtk.SelectionBrowse)
	v.list.SetActivateOnSingleClick(true)

	var currentCh discord.ChannelID

	v.list.ConnectRowSelected(func(r *gtk.ListBoxRow) {
		if r == nil {
			// This should not happen.
			return
		}

		// Invalidate our selection state.
		v.selectID = 0

		ch := v.rowChannel(r)
		if ch == nil || ch.id == currentCh {
			return
		}

		currentCh = ch.id
		parent := gtk.BaseWidget(v.list.Parent())
		parent.ActivateAction("win.open-channel", gtkcord.NewChannelIDVariant(ch.id))
	})

	// mod: friendlist — wrap the active-DM list in a vertical Box so we
	// can append the collapsible Friends expander underneath it. Both
	// live inside the same ScrolledWindow and scroll as one unit.
	contentBox := gtk.NewBox(gtk.OrientationVertical, 0)
	contentBox.SetHExpand(true)
	contentBox.Append(v.list)
	v.friendsExpander = mods.NewFriendsExpander(ctx)
	if v.friendsExpander != nil {
		contentBox.Append(v.friendsExpander)
	}

	v.scroll = gtk.NewScrolledWindow()
	v.scroll.SetPropagateNaturalHeight(true)
	v.scroll.SetHExpand(true)
	v.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	v.scroll.SetChild(contentBox)

	v.searchEntry = gtk.NewSearchEntry()
	v.searchEntry.SetHExpand(true)
	v.searchEntry.SetVAlign(gtk.AlignCenter)
	v.searchEntry.SetObjectProperty("placeholder-text", locale.Get("Search Users"))
	v.searchEntry.ConnectSearchChanged(func() {
		v.searchString = strings.ToLower(v.searchEntry.Text())
		v.list.InvalidateFilter()
	})

	v.searchBar = gtk.NewSearchBar()
	v.searchBar.AddCSSClass("titlebar")
	v.searchBar.AddCSSClass("direct-searchbar")
	v.searchBar.ConnectEntry(&v.searchEntry.EditableTextWidget)
	v.searchBar.SetSearchMode(true)
	v.searchBar.SetShowCloseButton(false)
	v.searchBar.SetChild(v.searchEntry)

	// mod: compactsidebar — a search field is unusable at avatar width.
	mods.BindCompactSidebar(v.searchBar, func(compact bool) {
		v.searchBar.SetVisible(!compact)
	})

	// mod: compactsidebar — show spinner while loading, swap to scroll on data
	v.spinner = gtk.NewSpinner()
	v.spinner.SetSizeRequest(16, 16)
	v.spinner.SetHAlign(gtk.AlignCenter)
	v.spinner.SetVAlign(gtk.AlignCenter)
	v.spinner.Start()

	v.ToolbarView = adw.NewToolbarView()
	v.ToolbarView.SetTopBarStyle(adw.ToolbarFlat)
	v.ToolbarView.SetContent(v.spinner) // show spinner until Invalidate()
	v.ToolbarView.AddTopBar(v.searchBar)

	vis := gtkutil.WithVisibility(ctx, v)

	state := gtkcord.FromContext(ctx)

	// mod: friendlist — re-invalidate when the friend cache finishes
	// loading (disk first, REST fallback). Without this the friends
	// dropdown would stay empty on a cold first launch until some other
	// gateway event nudged the view into re-rendering.
	gtkcord.FriendCacheRefreshed.Connect(func() { v.Invalidate() })

	state.BindHandler(vis, func(ev gateway.Event) {
		// TODO: Channel events

		switch ev := ev.(type) {
		case *gateway.ChannelCreateEvent:
			if !ev.GuildID.IsValid() {
				v.Invalidate() // recreate everything
			}
		case *gateway.ChannelDeleteEvent:
			v.deleteCh(ev.ID)

		case *gateway.MessageCreateEvent:
			if ch, ok := v.channels[ev.ChannelID]; ok {
				ch.Invalidate()
			}
		case *read.UpdateEvent:
			if ch, ok := v.channels[ev.ChannelID]; ok {
				ch.Invalidate()
			}
		}
	},
		(*gateway.ChannelCreateEvent)(nil),
		(*gateway.ChannelDeleteEvent)(nil),
		(*gateway.MessageCreateEvent)(nil),
		(*read.UpdateEvent)(nil),
	)

	return &v
}

// SelectChannel selects a known channel. If none is known, then it is selected
// later when the list is changed or never selected if the user selects
// something else.
func (v *ChannelView) SelectChannel(chID discord.ChannelID) {
	ch, ok := v.channels[chID]
	if !ok {
		v.selectID = chID
		return
	}

	v.selectID = 0
	v.list.SelectRow(ch.ListBoxRow)

	slog.Debug(
		"selected DM channel immediately",
		"channel_id", chID)
}

// Invalidate invalidates the whole channel view.
func (v *ChannelView) Invalidate() {
	state := gtkcord.FromContext(v.ctx)

	// Freeze list signals and re-emit it after.
	v.list.FreezeNotify()
	defer v.list.ThawNotify()

	// Temporarily disable the sort function. We'll re-enable it once we're
	// done and force a full re-sort.
	v.list.SetSortFunc(nil)
	defer func() {
		v.list.SetSortFunc(v.sort)
		v.list.InvalidateSort()
	}()

	chs, err := state.Cabinet.PrivateChannels()
	if err != nil {
		slog.Error("failed to load DM channels", "err", err)
		return
	}

	// mod: compactsidebar — swap spinner for scroll content
	v.spinner.Stop()
	v.ToolbarView.SetContent(v.scroll)

	// Keep track of channels that aren't in the list anymore.
	keep := make(map[discord.ChannelID]bool, len(v.channels))
	for id := range v.channels {
		keep[id] = false
	}

	for i, channel := range chs {
		ch, ok := v.channels[channel.ID]
		if !ok {
			ch = NewChannel(v.ctx, channel.ID)
			v.channels[channel.ID] = ch
		}

		ch.Update(&chs[i])

		if _, ok := keep[channel.ID]; ok {
			keep[channel.ID] = true
		} else {
			v.list.Append(ch)
		}
	}

	// Remove channels that didn't appear in the tracking map.
	for id, new := range keep {
		if !new {
			v.deleteCh(id)
		}
	}

	// If we have a channel to be selected, then select it.
	if v.selectID.IsValid() {
		if ch, ok := v.channels[v.selectID]; ok {
			v.list.SelectRow(ch.ListBoxRow)

			slog.Debug(
				"finally found DM channel to select",
				"channel_id", v.selectID)
		}
	}

	// mod: friendlist — refresh the collapsible Friends section with
	// the set of friends who don't already have an open 1:1 DM.
	if v.friendsExpander != nil {
		active := make(map[discord.UserID]struct{}, len(chs))
		for _, ch := range chs {
			if ch.Type == discord.DirectMessage && len(ch.DMRecipients) > 0 {
				active[ch.DMRecipients[0].ID] = struct{}{}
			}
		}
		v.friendsExpander.Invalidate(active)
	}
}

func (v *ChannelView) deleteCh(id discord.ChannelID) {
	ch, ok := v.channels[id]
	if !ok {
		return
	}

	v.list.Remove(ch)
	delete(v.channels, id)
}

func (v *ChannelView) sort(r1, r2 *gtk.ListBoxRow) int { // -1 == less == r1 first
	ch1 := v.rowChannel(r1)
	ch2 := v.rowChannel(r2)
	if ch1 == nil {
		return 1
	}
	if ch2 == nil {
		return -1
	}

	last1 := ch1.LastMessageID()
	last2 := ch2.LastMessageID()

	if !last1.IsValid() {
		return 1
	}
	if !last2.IsValid() {
		return -1
	}
	if last1 > last2 {
		// ch1 is older, put first.
		return -1
	}
	if last1 == last2 {
		return 0
	}
	return 1 // newer
}

func (v *ChannelView) filter(r *gtk.ListBoxRow) bool {
	if v.searchString == "" {
		return true
	}

	ch := v.rowChannel(r)
	if ch == nil {
		return false
	}

	name := strings.ToLower(ch.SearchText())
	return strings.Contains(name, v.searchString)
}

func (v *ChannelView) rowChannel(r *gtk.ListBoxRow) *Channel {
	id, err := discord.ParseSnowflake(r.Name())
	if err != nil {
		slog.Error(
			"failed to parse channel row name as snowflake",
			"row_name", r.Name(),
			"err", err)
		return nil
	}

	ch, ok := v.channels[discord.ChannelID(id)]
	if !ok {
		slog.Warn(
			"ChannelView contains channel with unknown ID",
			"channel_id", id)
		return nil
	}

	return ch
}
