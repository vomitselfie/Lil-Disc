package mods

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableFriendList = prefs.NewBool(true, prefs.PropMeta{
	Name:        "More Dropdown in DMs",
	Section:     "Mods",
	Description: "Show a collapsible \"More\" list of friends without an active DM under the DM list. Clicking a friend opens a new DM with them.",
})

var _ = lilcss.WriteCSS(`
	/* Separated from the DM list by space and a soft rule, matching how
	   channel categories separate their groups. */
	.mod-friend-list-expander {
		margin: {$space_lg} {$row_inset} {$space_hair} {$row_inset};
		border-top: 1px solid @lil_border;
		padding-top: {$space_sm};
	}
	.mod-friend-list-expander > title {
		padding: {$space_xs} {$space_xs};
	}
	.mod-friend-list-expander > title > label {
		font-size: {$font_micro};
		font-weight: 700;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: @lil_text_faint;
	}
	.mod-friend-list {
		background: transparent;
	}
	.mod-friend-list > row {
		min-height: {$row_height};
		border-radius: {$radius_md};
		margin: 1px {$row_inset};
	}
	.mod-friend-list > row:hover {
		background-color: @lil_hover;
	}
	.mod-friend-list > row:selected {
		background-color: @lil_selected;
	}
	.mod-friend-list > row:selected:hover {
		background-color: @lil_active;
	}
	.mod-friend-row {
		padding: {$space_xs} {$space_md};
	}
	.mod-friend-row-avatar {
		margin-right: {$space_md};
	}
	.mod-friend-row-name {
		color: @lil_text_dim;
	}
	.mod-friend-list > row:hover .mod-friend-row-name {
		color: @lil_text;
	}
`)

// FriendsExpander is a collapsible section that lists the user's Discord
// friends who don't currently have an open DM channel. Clicking a friend
// opens (or creates) a DM and navigates to it via the window's
// win.open-channel action.
//
// The expander owns a gtk.ListBox. Each Invalidate() diffs the current
// friend set against the existing rows and adds / removes / updates as
// needed so scroll position and focus are preserved.
type FriendsExpander struct {
	*gtk.Expander

	ctx  context.Context
	list *gtk.ListBox
	rows map[discord.UserID]*friendRow
}

type friendRow struct {
	*gtk.ListBoxRow
	avatar *onlineimage.Avatar
	name   *gtk.Label
}

// NewFriendsExpander constructs the collapsible friends widget. Returns
// nil if the feature is disabled in preferences — callers should nil-
// check and simply skip appending it.
func NewFriendsExpander(ctx context.Context) *FriendsExpander {
	if !enableFriendList.Value() {
		return nil
	}

	fe := &FriendsExpander{
		ctx:  ctx,
		rows: make(map[discord.UserID]*friendRow),
	}

	fe.list = gtk.NewListBox()
	fe.list.AddCSSClass("mod-friend-list")
	fe.list.SetSelectionMode(gtk.SelectionSingle)
	fe.list.SetActivateOnSingleClick(true)
	fe.list.ConnectRowActivated(func(r *gtk.ListBoxRow) {
		fe.handleRowClick(r)
	})

	fe.Expander = gtk.NewExpander("More")
	fe.Expander.AddCSSClass("mod-friend-list-expander")
	fe.Expander.SetChild(fe.list)
	fe.Expander.SetExpanded(true)

	return fe
}

// Invalidate rebuilds the friend list. activeDMRecipients is the set of
// user IDs that already have an open DM channel — those friends are
// filtered out so the expander only shows "friends you could start a new
// chat with." Must be called on the GTK main thread.
func (fe *FriendsExpander) Invalidate(activeDMRecipients map[discord.UserID]struct{}) {
	if fe == nil {
		return
	}

	state := gtkcord.FromContext(fe.ctx)
	if state == nil {
		return
	}

	// Collect every friend who isn't already in an active DM channel.
	// The friend cache is maintained by state.FetchFriendNicknames and
	// populated from the /users/@me/relationships REST endpoint (and its
	// on-disk cache for warm starts).
	var friends []gtkcord.FriendRecord
	state.EachFriend(func(rec gtkcord.FriendRecord) (stop bool) {
		if _, active := activeDMRecipients[rec.User.ID]; active {
			return false
		}
		friends = append(friends, rec)
		return false
	})

	// Stable alphabetical sort by displayed name so the list doesn't
	// reshuffle on every invalidate.
	sort.Slice(friends, func(i, j int) bool {
		return strings.ToLower(friendDisplayName(friends[i])) <
			strings.ToLower(friendDisplayName(friends[j]))
	})

	// Diff: drop rows whose user isn't in the new set.
	kept := make(map[discord.UserID]struct{}, len(friends))
	for _, rec := range friends {
		kept[rec.User.ID] = struct{}{}
	}
	for uid, row := range fe.rows {
		if _, ok := kept[uid]; !ok {
			fe.list.Remove(row)
			delete(fe.rows, uid)
		}
	}

	// Add or update rows. ListBox doesn't guarantee order on Append, so
	// we re-index by removing and re-appending in sorted order — cheap
	// for tens to low hundreds of rows.
	for _, row := range fe.rows {
		fe.list.Remove(row)
	}
	for _, rec := range friends {
		row, exists := fe.rows[rec.User.ID]
		if !exists {
			row = fe.newRow()
			fe.rows[rec.User.ID] = row
		}
		row.update(rec)
		fe.list.Append(row)
	}

	fe.Expander.SetLabel(fmt.Sprintf("More (%d)", len(friends)))
}

// handleRowClick looks up the clicked row in the rows map, then opens
// (or creates) a DM channel with that friend and navigates to it.
func (fe *FriendsExpander) handleRowClick(r *gtk.ListBoxRow) {
	var uid discord.UserID
	for id, row := range fe.rows {
		if row.ListBoxRow == r {
			uid = id
			break
		}
	}
	if !uid.IsValid() {
		return
	}

	state := gtkcord.FromContext(fe.ctx)
	if state == nil {
		return
	}

	slog.Info("friendlist: opening DM with friend", "user_id", uid)
	go func() {
		ch, err := state.CreatePrivateChannel(uid)
		if err != nil || ch == nil {
			slog.Warn("friendlist: failed to open DM",
				"user_id", uid, "err", err)
			return
		}
		chID := ch.ID
		glib.IdleAdd(func() {
			gtk.BaseWidget(fe.Expander).ActivateAction(
				"win.open-channel", gtkcord.NewChannelIDVariant(chID))
		})
	}()
}

// newRow constructs a reusable ListBoxRow for a single friend. The
// returned row is empty; the caller fills it via update().
func (fe *FriendsExpander) newRow() *friendRow {
	row := &friendRow{}

	row.avatar = onlineimage.NewAvatar(fe.ctx, imgutil.HTTPProvider, 24)
	row.avatar.AddCSSClass("mod-friend-row-avatar")

	row.name = gtk.NewLabel("")
	row.name.AddCSSClass("mod-friend-row-name")
	row.name.SetXAlign(0)
	row.name.SetHExpand(true)
	row.name.SetWrap(false)
	row.name.SetEllipsize(pango.EllipsizeEnd)

	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("mod-friend-row")
	box.Append(row.avatar)
	box.Append(row.name)

	row.ListBoxRow = gtk.NewListBoxRow()
	row.ListBoxRow.SetChild(box)

	// mod: compactsidebar — hide the name and centre the avatar when the
	// sidebar is icon-only.
	BindCompactSidebarRow(box, row.name)

	return row
}

func (r *friendRow) update(rec gtkcord.FriendRecord) {
	name := friendDisplayName(rec)
	r.name.SetText(name)
	r.avatar.SetText(name)
	if url := gtkcord.InjectAvatarSize(rec.User.AvatarURL()); url != "" {
		r.avatar.SetFromURL(url)
	}
	r.ListBoxRow.SetTooltipText(rec.User.Username)
}

// friendDisplayName mirrors the sidebar's userName but also respects the
// personal nickname (if any) set on the relationship. This is the same
// shortname the chat author label uses — see gtkcord.MemberMarkup.
func friendDisplayName(rec gtkcord.FriendRecord) string {
	if rec.Nickname != "" {
		return rec.Nickname
	}
	if rec.User.DisplayName != "" {
		return rec.User.DisplayName
	}
	return rec.User.Username
}
