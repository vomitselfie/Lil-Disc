package window

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/diamondburned/adaptive"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/diamondburned/ningen/v3/states/read"
	"libdb.so/ctxt"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
	"github.com/vomitselfie/Lil-Disc/internal/messages"
	"github.com/vomitselfie/Lil-Disc/internal/mods"
	"github.com/vomitselfie/Lil-Disc/internal/sidebar"
	"github.com/vomitselfie/Lil-Disc/internal/sidebar/channels"
	"github.com/vomitselfie/Lil-Disc/internal/window/backbutton"
	"github.com/vomitselfie/Lil-Disc/internal/window/quickswitcher"
)

var lastGuildKey = app.NewSingleStateKey[discord.GuildID]("last-guild-state")
var lastChannelKey = app.NewStateKey[discord.ChannelID]("guild-last-open")

// openTabsKey remembers the open tabs and which was active, so a restart
// comes back to the same set of channels rather than a single one.
var openTabsKey = app.NewSingleStateKey[savedTabs]("open-tabs")

// recentChannelsKey persists the quick switcher's recency list.
var recentChannelsKey = app.NewSingleStateKey[[]discord.ChannelID]("recent-channels")

type savedTabs struct {
	Channels []discord.ChannelID `json:"channels"`
	Active   int                 `json:"active"`
}

// maxRestoredTabs bounds how many tabs a restart reopens. Each tab loads
// its channel's messages, so an old session with dozens of tabs would
// otherwise start with a burst of requests.
const maxRestoredTabs = 8

// mod: resizable sidebar — remember the dragged width across launches, per
// list kind. The DM list and a guild's channel list want different widths, so
// they get their own entries rather than fighting over one.
var sidebarWidthKey = app.NewStateKey[int]("sidebar-width")

const (
	// defaultSidebarWidth is the width a sidebar with no remembered width
	// opens at.
	defaultSidebarWidth = 300
	// minSidebarWidth is the narrowest width worth remembering. It sits
	// below the icon-only width so that dragging down to avatars persists,
	// but above the position a hidden sidebar parks at — restoring to 0
	// would look like the sidebar had failed to open.
	minSidebarWidth = 100
)

// sidebarWidthStateKey names the stored width for a kind of list.
func sidebarWidthStateKey(kind sidebar.ViewKind) string {
	if kind == sidebar.ViewGuild {
		return "guild"
	}
	return "dms"
}

// TODO: refactor this to support TabOverview. We do this by refactoring Sidebar
// out completely and merging it into ChatPage. We can then get rid of the logic
// to keep the Sidebar in sync with the ChatPage, since each tab will have its
// own Sidebar.

type ChatPage struct {
	*gtk.Paned // mod: resizable sidebar via draggable pane
	Sidebar     *sidebar.Sidebar
	RightHeader *adw.HeaderBar
	rightTitle  *adw.Bin

	tabView *adw.TabView

	// Responsive collapse: when the window is narrow, the sidebar hides
	// and a toggle button appears in the header.
	back        *backbutton.BackButton
	isCollapsed bool

	sidebarWidthState *app.TypedState[int]

	lastGuildState   *app.TypedSingleState[discord.GuildID]
	lastChannelState *app.TypedState[discord.ChannelID]
	openTabsState    *app.TypedSingleState[savedTabs]
	recentState      *app.TypedSingleState[[]discord.ChannelID]
	// restoringTabs suppresses saving while tabs are being reopened, so a
	// half-restored set does not overwrite the saved one.
	restoringTabs bool

	lastGuild discord.GuildID

	// lastButtons keeps tracks of the header buttons of the previous view.
	// On view change, these buttons will be removed.
	lastButtons []gtk.Widgetter

	tabs map[uintptr]*chatTab // K: *adw.TabPage
	ctx  context.Context
}

type chatPageView struct {
	body          gtk.Widgetter
	headerButtons []gtk.Widgetter
}

var chatPageCSS = lilcss.Applier("window-chatpage", `
	.window-chatpage-rightbox > .top-bar > windowhandle > .collapse-spacing {
		padding: 0;
	}
	.right-header {
		border-radius: 0;
		box-shadow: none;
	}
	.right-header-label {
		font-weight: 650;
		letter-spacing: -0.01em;
	}
	.right-header-channel-icon {
		margin-right: {$space_sm};
		color: @lil_text_faint;
	}
`)

func NewChatPage(ctx context.Context, w *Window) *ChatPage {
	p := ChatPage{
		ctx:              ctx,
		tabs:             make(map[uintptr]*chatTab),
		lastGuildState:   lastGuildKey.Acquire(ctx),
		lastChannelState: lastChannelKey.Acquire(ctx),
		openTabsState:    openTabsKey.Acquire(ctx),
		recentState:      recentChannelsKey.Acquire(ctx),
	}

	p.tabView = adw.NewTabView()
	p.tabView.AddCSSClass("window-chatpage-tabview")
	p.tabView.SetDefaultIcon(gio.NewThemedIcon("channel-symbolic"))
	p.tabView.NotifyProperty("selected-page", func() {
		p.onActiveTabChange(p.tabView.SelectedPage())
		p.saveTabs()
	})
	p.tabView.ConnectClosePage(func(page *adw.TabPage) bool {
		tab, ok := p.tabs[page.Native()]
		if ok {
			if tab.messageView != nil {
				tab.messageView.SaveScrollAnchor()
			}
			delete(p.tabs, page.Native())
			p.tabView.ClosePageFinish(page, true)
			p.saveTabs()
		}
		return gdk.EVENT_STOP
	})

	p.Sidebar = sidebar.NewSidebar(ctx)
	p.Sidebar.SetHAlign(gtk.AlignFill)

	p.rightTitle = adw.NewBin()
	p.rightTitle.AddCSSClass("right-header-bin")
	p.rightTitle.SetHExpand(true)

	p.back = backbutton.New()

	newTabButton := gtk.NewButtonFromIconName("list-add-symbolic")
	newTabButton.SetTooltipText("Open a New Tab")
	newTabButton.ConnectClicked(func() { p.newTab() })

	p.RightHeader = adw.NewHeaderBar()
	p.RightHeader.AddCSSClass("titlebar")
	p.RightHeader.AddCSSClass("right-header")
	p.RightHeader.SetShowStartTitleButtons(false)
	p.RightHeader.SetShowEndTitleButtons(true)
	p.RightHeader.SetShowBackButton(false)
	p.RightHeader.SetShowTitle(false)
	p.RightHeader.PackStart(p.back)
	p.RightHeader.PackStart(p.rightTitle)
	p.RightHeader.PackEnd(newTabButton)

	tabBar := adw.NewTabBar()
	tabBar.AddCSSClass("window-chatpage-tabbar")
	tabBar.SetView(p.tabView)
	tabBar.SetAutohide(true)

	rightBox := adw.NewToolbarView()
	rightBox.AddCSSClass("window-chatpage-rightbox")
	rightBox.SetTopBarStyle(adw.ToolbarFlat)
	rightBox.SetHExpand(true)
	rightBox.AddTopBar(p.RightHeader)
	rightBox.AddTopBar(tabBar)
	rightBox.SetContent(p.tabView)

	// mod: resizable sidebar — use Paned for draggable divider
	p.Sidebar.SetSizeRequest(0, -1)
	// mod: responsive chat — allow the chat side to shrink below its
	// children's natural min width, and declare a min-width of 0 on the
	// box itself. Without this, the Paned refuses to size below the chat
	// column's requisition and the whole window hits a floor well above
	// the 550sp breakpoint.
	rightBox.SetSizeRequest(0, -1)
	p.Paned = gtk.NewPaned(gtk.OrientationHorizontal)
	p.Paned.SetStartChild(p.Sidebar)
	p.Paned.SetEndChild(rightBox)
	p.Paned.SetResizeStartChild(false)
	p.Paned.SetShrinkStartChild(false)
	p.Paned.SetResizeEndChild(true)
	p.Paned.SetShrinkEndChild(true)
	p.Paned.SetPosition(defaultSidebarWidth)
	p.Paned.SetWideHandle(true)

	// syncCompact derives icon-only mode from the width the list column
	// actually ends up with, which is the sidebar minus the guild rail.
	//
	// The two thresholds form a dead band. Entering compact hides labels,
	// which changes the sidebar's requested width, which can feed back into
	// this calculation — a single threshold would let that oscillate while
	// the user rests the drag handle on it.
	syncCompact := func() {
		if p.isCollapsed || !p.Sidebar.Visible() {
			return
		}
		// Icon-only is a DM-list idea. A DM row collapses to an avatar and is
		// still recognisable; a channel row is text and an anonymous hash
		// glyph, so the same width just hides the information.
		if p.Sidebar.Kind() == sidebar.ViewGuild {
			mods.SetSidebarCompact(false)
			return
		}
		switch listWidth := p.Sidebar.ListWidth(p.Paned.Position()); {
		case listWidth < mods.CompactEnterWidth:
			mods.SetSidebarCompact(true)
		case listWidth > mods.CompactLeaveWidth:
			mods.SetSidebarCompact(false)
		}
	}

	// Dragging the divider is the only thing that changes the position at
	// normal widths, so this is both the compact trigger and the point at
	// which the width is worth remembering. Deliberately remembers widths
	// down to icon-only: dragging the sidebar to avatars and leaving it
	// there is a normal way to use the app, not a degenerate state.
	//
	// gotkit's config store coalesces concurrent saves, so writing on every
	// notify during a drag does not turn into a write per pixel.
	// Remembered width per list kind, so switching between DMs and a server
	// restores whatever width that list was last used at.
	sidebarWidths := map[sidebar.ViewKind]int{}

	p.Paned.NotifyProperty("position", func() {
		if p.isCollapsed || !p.Sidebar.Visible() {
			return
		}
		syncCompact()
		if pos := p.Paned.Position(); pos >= minSidebarWidth {
			kind := p.Sidebar.Kind()
			sidebarWidths[kind] = pos
			p.sidebarWidthState.Set(sidebarWidthStateKey(kind), pos)
		}
	})

	// Seeded after the notify handler is connected, so restoring a narrow
	// width also restores icon-only mode through the same path a drag takes.
	p.sidebarWidthState = sidebarWidthKey.Acquire(ctx)
	for _, kind := range []sidebar.ViewKind{sidebar.ViewDMs, sidebar.ViewGuild} {
		p.sidebarWidthState.Get(sidebarWidthStateKey(kind), func(width int) {
			if width < minSidebarWidth {
				return
			}
			sidebarWidths[kind] = width
			// These loads are asynchronous. If one lands after the sidebar has
			// already restored a view, apply it now — otherwise the remembered
			// width would be held but never used until the next view switch.
			if !p.isCollapsed && p.Sidebar.Visible() && p.Sidebar.Kind() == kind {
				p.Paned.SetPosition(width)
			}
		})
	}

	// widthFor returns the width the sidebar should take for a list kind: the
	// width that list was last left at, or the default if it has none.
	//
	// Keeping these separate is the whole fix. The DM list degrades gracefully
	// to bare avatars, so people leave it narrow; a channel list at that width
	// is a column of identical hash glyphs. Sharing one width meant collapsing
	// the DM list and then clicking a server handed the channel list a width
	// that could not show a single name. A guild that has never been resized
	// now opens at the default rather than inheriting whatever the DM list was
	// last dragged to.
	//
	// Deliberately no floor for guilds: if someone narrows a channel list on
	// purpose, that is their call, and clamping it here would be re-saved by
	// the position handler below and quietly overwrite what they chose.
	widthFor := func(kind sidebar.ViewKind) int {
		if width, ok := sidebarWidths[kind]; ok && width >= minSidebarWidth {
			return width
		}
		return defaultSidebarWidth
	}

	// Applying the width on every view swap is what stops a sidebar collapsed
	// to DM avatars from carrying that width into a server, where it would
	// leave a column of channel icons with no names.
	p.ConnectDestroy(p.Sidebar.ConnectViewChanged(func() {
		if p.isCollapsed || !p.Sidebar.Visible() {
			return
		}
		kind := p.Sidebar.Kind()
		if kind == sidebar.ViewNone {
			return
		}
		p.Paned.SetPosition(widthFor(kind))
		syncCompact()
	}))

	// Toggling the preference off has to un-collapse anything already
	// collapsed; toggling it on re-applies it to the current width.
	p.ConnectDestroy(mods.SubscribeCompactSidebarEnabled(func() {
		if !mods.CompactSidebarEnabled() {
			mods.SetSidebarCompact(false)
			return
		}
		if p.isCollapsed && p.Sidebar.Visible() {
			mods.SetSidebarCompact(true)
			return
		}
		syncCompact()
	}))

	showSidebar := func(show bool) {
		p.Sidebar.SetVisible(show)
		p.back.SetActive(show)

		if !show {
			p.Paned.SetPosition(0)
			return
		}

		// While collapsed the sidebar is always icon-only: the window is
		// only a few hundred pixels wide, and the point of opening it is to
		// jump somewhere, not to read names next to a sliver of chat. A guild's
		// channel list is exempt — collapsing text to icon width would leave
		// nothing to jump by.
		if p.isCollapsed && p.Sidebar.Kind() != sidebar.ViewGuild {
			mods.SetSidebarCompact(true)
			p.Paned.SetPosition(p.Sidebar.CompactWidth())
			return
		}

		p.Paned.SetPosition(widthFor(p.Sidebar.Kind()))
		syncCompact()
	}

	p.back.ConnectClicked(func() {
		if !p.isCollapsed {
			return
		}
		showSidebar(!p.Sidebar.Visible())
	})

	breakpoint := adw.NewBreakpoint(adw.BreakpointConditionParse("max-width: 550sp"))
	breakpoint.ConnectApply(func() {
		// Remember the width for the list currently showing before collapsing
		// parks the divider at zero.
		if pos := p.Paned.Position(); pos >= minSidebarWidth {
			sidebarWidths[p.Sidebar.Kind()] = pos
		}
		p.isCollapsed = true
		p.back.SetVisible(true)
		showSidebar(false)
		// The chat header owns the start title buttons while collapsed, so
		// the rail's own controls must go or both sets show at once when
		// the sidebar is revealed.
		p.RightHeader.SetShowStartTitleButtons(true)
		p.Sidebar.SetShowWindowControls(false)
	})
	breakpoint.ConnectUnapply(func() {
		p.isCollapsed = false
		p.back.SetVisible(false)
		p.RightHeader.SetShowStartTitleButtons(false)
		p.Sidebar.SetShowWindowControls(true)
		showSidebar(true)
	})
	w.AddBreakpoint(breakpoint)

	state := gtkcord.FromContext(ctx)
	w.ConnectDestroy(state.AddHandler(
		func(*gateway.MessageCreateEvent) { p.updateWindowTitle() },
		func(*gateway.MessageUpdateEvent) { p.updateWindowTitle() },
		func(*gateway.MessageDeleteEvent) { p.updateWindowTitle() },
		func(*read.UpdateEvent) { p.updateWindowTitle() },
	))

	chatPageCSS(p)
	return &p
}

// OpenQuickSwitcher opens the Quick Switcher dialog.
func (p *ChatPage) OpenQuickSwitcher() { quickswitcher.ShowDialog(p.ctx) }

// ResetView switches out of any channel view and into the placeholder view.
// This method is used when the guild becomes unavailable.
func (p *ChatPage) ResetView() { p.SwitchToPlaceholder() }

// SwitchToPlaceholder switches to the empty placeholder view.
func (p *ChatPage) SwitchToPlaceholder() {
	tab := p.currentTab()
	tab.switchToPlaceholder()

	p.onActiveTabChange(p.tabView.Page(tab))
}

// SwitchToMessages reopens a new message page of the same channel ID if the
// user is opening one. Otherwise, the placeholder is seen.
func (p *ChatPage) SwitchToMessages() {
	tab := p.currentTab()
	tab.switchToPlaceholder()

	p.recentState.Get(gtkcord.SetRecentChannels)

	// Reopen the saved tabs; fall back to the last guild's last channel.
	p.openTabsState.Exists(func(exists bool) {
		if !exists {
			p.restoreLastGuild()
			return
		}
		p.openTabsState.Get(func(saved savedTabs) {
			if !p.restoreTabs(saved) {
				p.restoreLastGuild()
			}
		})
	})
}

func (p *ChatPage) restoreLastGuild() {
	p.lastGuildState.Get(func(id discord.GuildID) {
		if id.IsValid() {
			p.OpenGuild(id)
		} else {
			p.OpenDMs()
		}
	})
}

// restoreTabs reopens saved tabs whose channels this account can still see.
// It reports whether it opened any.
func (p *ChatPage) restoreTabs(saved savedTabs) bool {
	state := gtkcord.FromContext(p.ctx).Offline()

	var channels []discord.ChannelID
	active := 0
	for i, id := range saved.Channels {
		if _, err := state.Channel(id); err != nil {
			continue
		}
		if i == saved.Active {
			active = len(channels)
		}
		channels = append(channels, id)
		if len(channels) == maxRestoredTabs {
			break
		}
	}
	if len(channels) == 0 {
		return false
	}

	p.restoringTabs = true
	for i, id := range channels {
		if i > 0 {
			p.newTab()
		}
		p.OpenChannel(id)
	}
	if page := p.tabView.NthPage(active); page != nil {
		p.tabView.SetSelectedPage(page)
	}
	p.restoringTabs = false
	p.saveTabs()
	return true
}

// saveTabs records the open tabs and the active one.
func (p *ChatPage) saveTabs() {
	if p.restoringTabs {
		return
	}

	var saved savedTabs
	selected := p.tabView.SelectedPage()
	for i := 0; i < p.tabView.NPages(); i++ {
		page := p.tabView.NthPage(i)
		tab := p.tabs[page.Native()]
		if tab == nil || !tab.channelID().IsValid() {
			continue
		}
		if selected != nil && page.Native() == selected.Native() {
			saved.Active = len(saved.Channels)
		}
		saved.Channels = append(saved.Channels, tab.channelID())
	}
	p.openTabsState.Set(saved)
}

// SaveViewState records each open channel's reading position. Call it
// before the window closes or the app quits.
func (p *ChatPage) SaveViewState() {
	for _, tab := range p.tabs {
		if tab.messageView != nil {
			tab.messageView.SaveScrollAnchor()
		}
	}
	p.saveTabs()
}

// OpenDMs opens the DMs page.
func (p *ChatPage) OpenDMs() {
	p.lastGuild = 0
	p.lastGuildState.Set(0)
	p.Sidebar.OpenDMs()
	p.restoreLastChannel(0)
}

// OpenGuild opens the guild with the given ID.
func (p *ChatPage) OpenGuild(guildID discord.GuildID) {
	p.lastGuild = guildID
	p.lastGuildState.Set(guildID)
	p.Sidebar.SetSelectedGuild(guildID)
	p.restoreLastChannel(guildID)
}

func (p *ChatPage) restoreLastChannel(guildID discord.GuildID) {
	k := guildID.String()
	p.lastChannelState.Exists(k, func(exists bool) {
		if exists {
			p.lastChannelState.Get(k, func(chID discord.ChannelID) {
				slog.Debug(
					"restoring last channel from state",
					"guild_id", guildID,
					"restored_channel_id", chID)
				p.OpenChannel(chID)
			})
		} else {
			p.SwitchToPlaceholder()
		}
	})
}

// OpenChannel opens the channel with the given ID. Use this method to direct
// the user to a new channel when they request to, e.g. through a notification.
func (p *ChatPage) OpenChannel(chID discord.ChannelID) {
	var tab *chatTab
	var reselect bool
	for _, t := range p.tabs {
		if t.alreadyOpens(chID) {
			tab = t
			reselect = true
			break
		}
	}
	if tab == nil {
		tab = p.currentTab()
	}

	// Open the channel in the message view.
	tab.switchToChannel(chID)
	gtkcord.NoteChannelOpened(chID)
	p.recentState.Set(gtkcord.RecentChannels())

	page := p.tabView.Page(tab)
	updateTabInfo(p.ctx, page, chID)
	if reselect {
		p.tabView.SetSelectedPage(page)
	}

	// This method updates the tab and window, but it also updates the sidebar
	// selection internally.
	p.onActiveTabChange(page)

	state := gtkcord.FromContext(p.ctx).Offline()
	ch, _ := state.Channel(chID)
	if ch != nil {
		// Save the last opened channel for the guild.
		p.lastChannelState.Set(ch.GuildID.String(), chID)
	}

	p.saveTabs()
}

// OpenMessage opens the channel holding the message, loading the history
// around it if needed, and scrolls to and highlights it. Search results,
// notifications and message links all come through here.
func (p *ChatPage) OpenMessage(loc gtkcord.MessageLocation) {
	p.OpenChannel(loc.ChannelID)

	tab := p.currentTab()
	if tab.messageView == nil || tab.messageView.ChannelID() != loc.ChannelID {
		return
	}
	if loc.MessageID.IsValid() {
		tab.messageView.JumpTo(loc.MessageID)
	}
}

func updateTabInfo(ctx context.Context, page *adw.TabPage, chID discord.ChannelID) {
	if chID.IsValid() {
		page.SetIcon(gio.NewThemedIcon("channel-symbolic"))

		title := gtkcord.WindowTitleFromID(ctx, chID)
		// We don't actually want the prefixing # because we already have the
		// tab icon.
		title = strings.TrimPrefix(title, "#")
		page.SetTitle(title)
	} else {
		page.SetIcon(nil)
		page.SetTitle("New Tab")
	}
}

// ActiveChannelID returns the channel open in the selected tab, or 0. Unlike
// currentTab it never creates a tab.
func (p *ChatPage) ActiveChannelID() discord.ChannelID {
	page := p.tabView.SelectedPage()
	if page == nil {
		return 0
	}
	tab := p.tabs[page.Native()]
	if tab == nil {
		return 0
	}
	return tab.channelID()
}

// currentTab returns the current tab. If there is no tab, then it creates one.
func (p *ChatPage) currentTab() *chatTab {
	var tab *chatTab

	page := p.tabView.SelectedPage()
	if page != nil {
		// We already have a tab.
		// Ensure our window gets updated by the end.
		tab = p.tabs[page.Native()]
	} else {
		// We don't have an active tab right now. Create one.
		tab = p.newTab()
	}

	return tab
}

func (p *ChatPage) newTab() *chatTab {
	tab := newChatTab(p.ctx)

	page := p.tabView.Append(tab)
	updateTabInfo(p.ctx, page, 0)

	p.tabs[page.Native()] = tab
	p.tabView.SetSelectedPage(page)

	return tab
}

func (p *ChatPage) onActiveTabChange(page *adw.TabPage) {
	// Remove the previous header buttons.
	for _, button := range p.lastButtons {
		p.RightHeader.Remove(button)
	}
	p.lastButtons = nil

	p.updateWindowTitle()

	var tab *chatTab
	var chID discord.ChannelID

	if page != nil {
		tab = p.tabs[page.Native()]
		if tab == nil {
			// Ignore this. It's possible that we're still initializing.
			return
		}

		chID = tab.channelID()

		// Add the new header buttons.
		if tab.messageView != nil {
			p.lastButtons = tab.messageView.HeaderButtons()
			for i := len(p.lastButtons) - 1; i >= 0; i-- {
				button := p.lastButtons[i]
				p.RightHeader.PackEnd(button)
			}
		}
	}

	// Update the left guild list and channel list.
	if chID.IsValid() {
		// TODO: it really has to get rid of this SelectChannel call...
		// It's really hard for it to try and have a SetSelectedChannel function
		// because of how the SelectionChanged signal works.
		p.Sidebar.SelectChannel(chID)
	} else {
		// Hack to ensure that the guild item is selected when we have no
		// channel on display.
		if p.lastGuild.IsValid() {
			p.Sidebar.SetSelectedGuild(p.lastGuild)
		}
	}

	// Update the displaying window title.
	if !chID.IsValid() {
		p.rightTitle.SetChild(nil)
		return
	}

	state := gtkcord.FromContext(p.ctx)
	ch, _ := state.Cabinet.Channel(chID)

	chName := gtkcord.ChannelNameWithoutHash(ch)
	label := gtk.NewLabel(chName)
	label.AddCSSClass("right-header-label")
	label.SetEllipsize(pango.EllipsizeEnd)
	label.SetXAlign(0)

	chIcon := channels.NewChannelIcon(ch)
	chIcon.AddCSSClass("right-header-channel-icon")

	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("right-header-channel-box")
	box.Append(chIcon)
	box.Append(label)

	p.rightTitle.SetChild(box)
}

func (p *ChatPage) updateWindowTitle() {
	var title string
	if page := p.tabView.SelectedPage(); page != nil {
		title = page.Title()
	}

	state := gtkcord.FromContext(p.ctx)

	// Add a ping indicator if the user has pings.
	mentions := state.ReadState.TotalMentionCount()
	if mentions > 0 {
		title = fmt.Sprintf("(%d) %s", mentions, title)
	}

	win, _ := ctxt.From[*Window](p.ctx)
	win.SetTitle(title)
}

type chatTab struct {
	*gtk.Stack
	placeholder gtk.Widgetter
	messageView *messages.View // nilable
	ctx         context.Context
}

func newChatTab(ctx context.Context) *chatTab {
	var t chatTab
	t.ctx = ctx
	t.placeholder = newEmptyMessagePlaceholder()

	t.Stack = gtk.NewStack()
	t.Stack.AddCSSClass("window-message-page")
	t.Stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	t.Stack.AddChild(t.placeholder)
	t.Stack.SetVisibleChild(t.placeholder)

	return &t
}

func (t *chatTab) alreadyOpens(id discord.ChannelID) bool {
	return t.channelID() == id
}

func (t *chatTab) channelID() discord.ChannelID {
	if t.messageView == nil {
		return 0
	}
	return t.messageView.ChannelID()
}

func (t *chatTab) switchToPlaceholder() bool {
	return t.switchToChannel(0)
}

func (t *chatTab) switchToChannel(id discord.ChannelID) bool {
	if t.alreadyOpens(id) {
		return false
	}

	old := t.messageView
	if old != nil {
		old.SaveScrollAnchor()
	}

	if id.IsValid() {
		t.messageView = messages.NewView(t.ctx, id)
		t.messageView.FetchBacklog()

		t.Stack.AddChild(t.messageView)
		t.Stack.SetVisibleChild(t.messageView)

		viewWidget := gtk.BaseWidget(t.messageView)
		viewWidget.GrabFocus()
	} else {
		t.messageView = nil
		t.Stack.SetVisibleChild(t.placeholder)
	}

	if old != nil {
		gtkutil.NotifyProperty(t.Stack, "transition-running", func() bool {
			if !t.Stack.TransitionRunning() {
				t.Stack.Remove(old)
				return true
			}
			return false
		})
	}

	return true
}

func newEmptyMessagePlaceholder() gtk.Widgetter {
	status := adaptive.NewStatusPage()
	status.SetIconName("chat-bubbles-empty-symbolic")
	status.Icon.SetOpacity(0.45)
	status.Icon.SetIconSize(gtk.IconSizeLarge)

	return status
}
