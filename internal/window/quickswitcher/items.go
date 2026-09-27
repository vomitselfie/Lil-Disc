package quickswitcher

import (
	"context"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
	"github.com/vomitselfie/Lil-Disc/internal/sidebar/channels"
)

type indexItem interface {
	Row(context.Context) *gtk.ListBoxRow
	String() string
}

type indexItems []indexItem

func (its indexItems) String(i int) string { return its[i].String() }
func (its indexItems) Len() int            { return len(its) }

type channelItem struct {
	*discord.Channel
	guild  *discord.Guild
	name   string
	search string
}

// TODO: move this to gtkcord
var threadTypes = map[discord.ChannelType]bool{
	discord.GuildAnnouncementThread: true,
	discord.GuildPublicThread:       true,
	discord.GuildPrivateThread:      true,
}

var voiceTypes = map[discord.ChannelType]bool{
	discord.GuildVoice:      true,
	discord.GuildStageVoice: true,
}

func newChannelItem(state *gtkcord.State, guild *discord.Guild, ch *discord.Channel) channelItem {
	item := channelItem{
		Channel: ch,
		guild:   guild,
	}

	if ch.Name != "" {
		item.name = ch.Name
	} else {
		item.name = gtkcord.RecipientNames(ch)
	}

	if threadTypes[ch.Type] {
		parent, _ := state.Cabinet.Channel(ch.ParentID)
		if parent != nil {
			item.name = parent.Name + " › #" + item.name
		}
	}

	if item.guild != nil {
		item.search = item.guild.Name + " " + item.name
	} else {
		item.search = item.name
	}

	return item
}

func (it channelItem) String() string { return it.search }

var channelCSS = lilcss.Applier("quickswitcher-channel", `
	.quickswitcher-channel-icon {
		margin: 2px 8px;
		min-width:  {$inline_emoji_size};
		min-height: {$inline_emoji_size};
	}
	.quickswitcher-channel-hash {
		padding: 0px;
	}
	.quickswitcher-channel-image {
		margin-right: 12px;
	}
	.quickswitcher-channel-guildname {
		font-size: 0.85em;
		color: alpha(@theme_fg_color, 0.75);
		margin: 4px;
		margin-left: 18px;
		margin-bottom: calc(4px - 0.1em);
	}
`)

func (it channelItem) Row(ctx context.Context) *gtk.ListBoxRow {
	tooltip := it.name
	if it.guild != nil {
		tooltip += " (" + it.guild.Name + ")"
	}

	box := gtk.NewBox(gtk.OrientationHorizontal, 0)

	row := gtk.NewListBoxRow()
	row.SetTooltipText(tooltip)
	row.SetChild(box)
	channelCSS(row)

	switch it.Type {
	case discord.DirectMessage, discord.GroupDM:
		icon := onlineimage.NewAvatar(ctx, imgutil.HTTPProvider, gtkcord.InlineEmojiSize)
		icon.AddCSSClass("quickswitcher-channel-icon")
		icon.AddCSSClass("quickswitcher-channel-image")
		icon.SetHAlign(gtk.AlignCenter)
		icon.SetText(it.name)
		if len(it.DMRecipients) == 1 {
			icon.SetFromURL(gtkcord.InjectAvatarSize(it.DMRecipients[0].AvatarURL()))
		}

		anim := icon.EnableAnimation()
		anim.ConnectMotion(row) // TODO: I wonder if this causes memory leaks.

		box.Append(icon)
	default:
		icon := channels.NewChannelIcon(it.Channel, func(t discord.ChannelType) (string, bool) {
			_, isThread := threadTypes[t]
			return "thread-branch-symbolic", isThread
		})

		icon.AddCSSClass("quickswitcher-channel-icon")
		icon.AddCSSClass("quickswitcher-channel-hash")
		icon.SetHAlign(gtk.AlignCenter)

		box.Append(icon)
	}

	name := gtk.NewLabel(it.name)
	name.AddCSSClass("quickswitcher-channel-name")
	name.SetHExpand(true)
	name.SetXAlign(0)
	name.SetEllipsize(pango.EllipsizeEnd)

	box.Append(name)

	if it.guild != nil {
		guildName := gtk.NewLabel(it.guild.Name)
		guildName.AddCSSClass("quickswitcher-channel-guildname")
		guildName.SetEllipsize(pango.EllipsizeEnd)

		box.Append(guildName)
	}

	return row
}

type guildItem struct {
	*discord.Guild
}

func newGuildItem(guild *discord.Guild) guildItem {
	return guildItem{
		Guild: guild,
	}
}

func (it guildItem) String() string { return it.Name }

var guildCSS = lilcss.Applier("quickswitcher-guild", `
	.quickswitcher-guild-icon {
		margin: 2px 8px;
		min-width:  {$inline_emoji_size};
		min-height: {$inline_emoji_size};
	}
`)

func (it guildItem) Row(ctx context.Context) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	guildCSS(row)

	icon := onlineimage.NewAvatar(ctx, imgutil.HTTPProvider, gtkcord.InlineEmojiSize)
	icon.AddCSSClass("quickswitcher-guild-icon")
	icon.SetText(it.Name)
	icon.SetFromURL(it.IconURL())
	icon.SetHAlign(gtk.AlignCenter)

	anim := icon.EnableAnimation()
	anim.ConnectMotion(row)

	name := gtk.NewLabel(it.Name)
	name.AddCSSClass("quickswitcher-guild-name")
	name.SetHExpand(true)
	name.SetXAlign(0)

	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.Append(icon)
	box.Append(name)

	row.SetChild(box)
	return row
}

// commandItem is an application action offered in ">" mode, which makes the
// switcher a small command palette.
type commandItem struct {
	name   string
	action string // detailed action name, e.g. "app.preferences"
	icon   string
}

// commands are the actions the palette offers. Each is an existing window
// or application action, so the palette adds no behaviour of its own.
var commands = []commandItem{
	{"Preferences", "app.preferences", "preferences-system-symbolic"},
	{"Search This Channel", "win.message-search", "system-search-symbolic"},
	{"Search Cached Messages", "win.message-search-all", "system-search-symbolic"},
	{"Open Direct Messages", "win.open-dms", "chat-bubbles-empty-symbolic"},
	{"Set Status: Online", "win.set-online", "user-available-symbolic"},
	{"Set Status: Idle", "win.set-idle", "user-idle-symbolic"},
	{"Set Status: Do Not Disturb", "win.set-dnd", "user-busy-symbolic"},
	{"Set Status: Invisible", "win.set-invisible", "user-invisible-symbolic"},
	{"Keyboard Shortcuts", "win.keybind-help", "input-keyboard-symbolic"},
	{"Clear Message History", "win.clear-history", "user-trash-symbolic"},
	{"Logs", "app.logs", "dialog-information-symbolic"},
	{"About LilDisc", "app.about", "help-about-symbolic"},
	{"Quit", "app.quit", "application-exit-symbolic"},
}

func (it commandItem) String() string { return it.name }

func (it commandItem) Row(ctx context.Context) *gtk.ListBoxRow {
	icon := gtk.NewImageFromIconName(it.icon)
	icon.AddCSSClass("quickswitcher-guild-icon")

	name := gtk.NewLabel(it.name)
	name.SetHExpand(true)
	name.SetXAlign(0)

	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.Append(icon)
	box.Append(name)

	row := gtk.NewListBoxRow()
	guildCSS(row)
	row.SetChild(box)
	return row
}
