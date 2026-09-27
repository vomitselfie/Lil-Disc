// Package sidebar contains the sidebar showing guilds and channels.
package sidebar

import (
	"context"
	"log/slog"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/dijama/lildisc/internal/gtkcord"
	"github.com/dijama/lildisc/internal/lilcss"
	"github.com/dijama/lildisc/internal/mods"
	"github.com/dijama/lildisc/internal/sidebar/channels"
	"github.com/dijama/lildisc/internal/sidebar/direct"
	"github.com/dijama/lildisc/internal/sidebar/directbutton"
	"github.com/dijama/lildisc/internal/sidebar/guilds"
	"github.com/dijama/lildisc/internal/signaling"
)

// ViewKind identifies which list the sidebar is currently showing. The two
// lists want different widths: DM rows stay recognisable as bare avatars,
// while a channel list is nothing but text, so narrowing it to icon width
// leaves the user looking at a column of anonymous hashes.
type ViewKind uint8

const (
	ViewNone ViewKind = iota
	ViewDMs
	ViewGuild
)

// Sidebar is the bar on the left side of the application once it's logged in.
type Sidebar struct {
	*gtk.Box // horizontal

	Left   *gtk.Box
	DMView *directbutton.View
	Guilds *guilds.View
	Right  *gtk.Stack

	// leftCtrl is the window controls in the guild rail. The chat header
	// takes ownership of the start title buttons while the window is
	// collapsed, so these have to be hidden to avoid showing two sets.
	leftCtrl *gtk.WindowControls

	// Keep track of the last child to remove.
	current struct {
		w gtk.Widgetter
		// id discord.GuildID
	}
	placeholder gtk.Widgetter

	// viewChanged fires after the sidebar swaps between the DM list and a
	// guild's channel list, so the chat page can re-apply the width policy
	// for whichever list is now showing.
	viewChanged signaling.Signaler

	ctx context.Context
}

var sidebarCSS = lilcss.Applier("sidebar-sidebar", `
	@define-color sidebar_bg @lil_rail;

	.sidebar-guildside {
		background-color: @sidebar_bg;
	}
	.sidebar-guildside windowcontrols:not(.empty) {
		margin-left: 4px;
		margin-right: 4px;
	}
	.sidebar-guildside windowcontrols:not(.empty) button {
		margin: 0px 0;
	}
`)

// NewSidebar creates a new Sidebar.
func NewSidebar(ctx context.Context) *Sidebar {
	s := Sidebar{
		ctx: ctx,
	}

	s.Guilds = guilds.NewView(ctx)
	s.Guilds.Invalidate()

	s.DMView = directbutton.NewView(ctx)
	s.DMView.Invalidate()

	dmSeparator := gtk.NewSeparator(gtk.OrientationHorizontal)
	dmSeparator.AddCSSClass("sidebar-dm-separator")

	// leftBox holds just the DM button and the guild view, as opposed to s.Left
	// which holds the scrolled window and the window controls.
	leftBox := gtk.NewBox(gtk.OrientationVertical, 0)
	leftBox.Append(s.DMView)
	leftBox.Append(dmSeparator)
	leftBox.Append(s.Guilds)

	leftScroll := gtk.NewScrolledWindow()
	leftScroll.SetVExpand(true)
	leftScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyExternal)
	leftScroll.SetChild(leftBox)

	s.leftCtrl = gtk.NewWindowControls(gtk.PackStart)
	s.leftCtrl.SetHAlign(gtk.AlignCenter)

	s.Left = gtk.NewBox(gtk.OrientationVertical, 0)
	s.Left.AddCSSClass("sidebar-guildside")
	s.Left.Append(s.leftCtrl)
	s.Left.Append(leftScroll)

	s.placeholder = gtk.NewWindowHandle()

	s.Right = gtk.NewStack()
	// mod: compactsidebar — removed fixed width to allow sidebar to shrink
	s.Right.SetSizeRequest(0, -1)
	s.Right.SetVExpand(true)
	s.Right.SetHExpand(true)
	s.Right.AddChild(s.placeholder)
	s.Right.SetVisibleChild(s.placeholder)
	s.Right.SetTransitionType(gtk.StackTransitionTypeCrossfade)

	userBar := newUserBar(ctx, []gtkutil.PopoverMenuItem{
		gtkutil.MenuItem("Quick Switcher", "win.quick-switcher"),
		gtkutil.MenuSeparator("User Settings"),
		gtkutil.Submenu("Set _Status", []gtkutil.PopoverMenuItem{
			gtkutil.MenuItem("_Online", "win.set-online"),
			gtkutil.MenuItem("_Idle", "win.set-idle"),
			gtkutil.MenuItem("_Do Not Disturb", "win.set-dnd"),
			gtkutil.MenuItem("In_visible", "win.set-invisible"),
		}),
		gtkutil.MenuItem("_Refresh Avatar", "win.refresh-avatar"),
		gtkutil.MenuSeparator(""),
		gtkutil.MenuItem("_Preferences", "app.preferences"),
		gtkutil.MenuItem("_About", "app.about"),
		gtkutil.MenuItem("_Logs", "app.logs"),
		gtkutil.MenuItem("_Quit", "app.quit"),
	})

	// TODO: consider if we can merge this ToolbarView with the one in channels
	// and direct.
	rightWrap := adw.NewToolbarView()
	rightWrap.SetHExpand(true) // mod: compactsidebar — fill available sidebar width
	rightWrap.AddBottomBar(userBar)
	rightWrap.SetContent(s.Right)

	// mod: compactsidebar — rightWrap carries the compact CSS class so the
	// mod's stylesheet reaches both the list and the user bar in the
	// ToolbarView's bottom bar. The widths themselves are decided by the
	// chat page, which owns the Paned; individual rows opt into icon-only
	// behaviour at construction via mods.BindCompactSidebar*.
	mods.SetupCompactSidebar(rightWrap)

	s.Box = gtk.NewBox(gtk.OrientationHorizontal, 0)
	s.Box.SetHExpand(true) // fill the Paned's allocated width so names expand
	s.Box.Append(s.Left)
	s.Box.Append(rightWrap)
	sidebarCSS(s)

	return &s
}

// RailWidth returns the guild rail's width, falling back to its intended
// size before the first allocation.
func (s *Sidebar) RailWidth() int {
	if w := s.Left.Width(); w > 0 {
		return w
	}
	// GuildIconSize plus the .sidebar-button horizontal padding.
	return gtkcord.GuildIconSize + 24
}

// CompactWidth is the sidebar's total width when the list column is reduced
// to icon-only: the guild rail plus a single avatar column.
func (s *Sidebar) CompactWidth() int {
	return s.RailWidth() + mods.CompactWidth
}

// ListWidth converts a total sidebar width into the width actually left for
// the DM/channel list column, which is what decides icon-only mode.
func (s *Sidebar) ListWidth(totalWidth int) int {
	return totalWidth - s.RailWidth()
}

// SetShowWindowControls toggles the guild rail's window controls.
func (s *Sidebar) SetShowWindowControls(show bool) {
	s.leftCtrl.SetVisible(show)
}

// Kind reports which list the sidebar is currently showing.
func (s *Sidebar) Kind() ViewKind {
	switch s.current.w.(type) {
	case *direct.ChannelView:
		return ViewDMs
	case *channels.View:
		return ViewGuild
	default:
		return ViewNone
	}
}

// ConnectViewChanged registers f to run whenever the sidebar switches between
// the DM list and a guild's channel list. The returned function disconnects.
func (s *Sidebar) ConnectViewChanged(f func()) func() {
	return s.viewChanged.Connect(f)
}

// GuildID returns the guild ID that the channel list is showing for, if any.
// If not, 0 is returned.
func (s *Sidebar) GuildID() discord.GuildID {
	ch, ok := s.current.w.(*channels.View)
	if !ok {
		return 0
	}
	return ch.GuildID()
}

func (s *Sidebar) stackSelect(w gtk.Widgetter) {
	if w == s.current.w {
		return
	}

	old := s.current.w
	s.current.w = w
	s.viewChanged.Signal()

	if w == nil {
		s.Right.SetVisibleChild(s.placeholder)
	} else {
		// This should do nothing if the widget is already in the stack.
		// Maybe???
		s.Right.AddChild(w)
		s.Right.SetVisibleChild(w)

		w := gtk.BaseWidget(w)
		w.GrabFocus()
	}

	if old != nil {
		gtkutil.NotifyProperty(s.Right, "transition-running", func() bool {
			// Remove the widget when the transition is done.
			if !s.Right.TransitionRunning() {
				s.Right.Remove(old)

				w := gtk.BaseWidget(old)
				slog.Debug(
					"sidebar: right stack transition done, removed widget",
					"widget", w.Type().String()+"."+strings.Join(w.CSSClasses(), "."))

				return true
			} else {
				slog.Debug("sidebar: right stack transition started")
				return false
			}
		})
	}
}

// OpenDMs opens the DMs view. It automatically loads the DMs on first open, so
// the returned ChannelView is guaranteed to be ready.
func (s *Sidebar) OpenDMs() *direct.ChannelView {
	if direct, ok := s.current.w.(*direct.ChannelView); ok {
		// we're already there
		return direct
	}

	s.unselect()
	s.DMView.SetSelected(true)

	direct := direct.NewChannelView(s.ctx)
	direct.SetVExpand(true)
	direct.Invalidate()

	s.stackSelect(direct)

	return direct
}

func (s *Sidebar) openGuild(guildID discord.GuildID) *channels.View {
	chs, ok := s.current.w.(*channels.View)
	if ok && chs.GuildID() == guildID {
		// We're already there.
		return chs
	}

	s.unselect()
	s.Guilds.SetSelectedGuild(guildID)

	chs = channels.NewView(s.ctx, guildID)
	chs.SetVExpand(true)
	chs.InvalidateHeader()

	s.stackSelect(chs)
	return chs
}

func (s *Sidebar) unselect() {
	s.Guilds.Unselect()
	s.DMView.Unselect()
	s.stackSelect(nil)
}

// Unselect unselects the current guild or channel.
func (s *Sidebar) Unselect() {
	s.unselect()
	s.Right.SetVisibleChild(s.placeholder)
}

// SetSelectedGuild marks the guild with the given ID as selected.
func (s *Sidebar) SetSelectedGuild(guildID discord.GuildID) {
	s.Guilds.SetSelectedGuild(guildID)
	s.openGuild(guildID)
}

// // SelectGuild selects and activates the guild with the given ID.
// func (s *Sidebar) SelectGuild(guildID discord.GuildID) {
// 	if s.Guilds.SelectedGuildID() != guildID {
// 		s.Guilds.SetSelectedGuild(guildID)
//
// 		parent := gtk.BaseWidget(s.Parent())
// 		parent.ActivateAction("win.open-guild", gtkcord.NewGuildIDVariant(guildID))
// 	}
// }

// SelectChannel selects and activates the channel with the given ID. It ensures
// that the sidebar is at the right place then activates the controller.
// This function acts the same as if the user clicked on the channel, meaning it
// funnels down to a single widget that then floats up to the controller.
func (s *Sidebar) SelectChannel(chID discord.ChannelID) {
	state := gtkcord.FromContext(s.ctx)
	ch, _ := state.Cabinet.Channel(chID)
	if ch == nil {
		slog.Error(
			"cannot select channel in sidebar since it's not found in state",
			"channel_id", chID)
		return
	}

	s.Guilds.SetSelectedGuild(ch.GuildID)

	if ch.GuildID.IsValid() {
		guild := s.openGuild(ch.GuildID)
		guild.SelectChannel(chID)
	} else {
		direct := s.OpenDMs()
		direct.SelectChannel(chID)
	}
}
