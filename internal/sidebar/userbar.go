package sidebar

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"
	"github.com/dijama/lildisc/internal/gtkcord"
	"github.com/dijama/lildisc/internal/lilcss"
	"github.com/dijama/lildisc/internal/mods"
)

type userBar struct {
	// *gtk.ActionBar
	*gtk.Box
	avatar        *onlineimage.Avatar
	avatarOverlay *gtk.Overlay // mod: presence dot overlay
	name          *gtk.Label
	menu          *gtk.ToggleButton

	ctx context.Context

	// mod: avatar — when the user's avatar URL 404s (Discord-side data
	// integrity bug, account migration, broken upload, etc.) we install
	// the user's default Discord avatar as a fallback so they don't sit
	// staring at initials. Tracks which fallback URL we last installed
	// so we don't loop if the default also fails. Reset on manual refresh.
	fallbackURL string
}

var userBarCSS = lilcss.Applier("user-bar", `
	/* The user bar had no background, no separator and no hover feedback at
	   all, so it read as loose widgets at the bottom of the sidebar rather
	   than a bar. */
	.user-bar {
		border-top: 1px solid @lil_border;
		background-color: @lil_surface_sunken;
		padding: {$space_hair} 0;
	}
	.user-bar-avatar {
		padding: {$space_sm};
		border-radius: {$radius_pill};
	}
	.user-bar-avatar:hover {
		background-color: @lil_hover;
	}
	.user-bar-name {
		font-weight: 600;
		margin-left: {$space_hair};
	}
	.user-bar-menu {
		margin: 0 {$space_sm};
		border-radius: {$radius_md};
	}
`)

func newUserBar(ctx context.Context, menuActions []gtkutil.PopoverMenuItem) *userBar {
	b := &userBar{ctx: ctx}

	// mod: avatar — install an error callback on the context the avatar
	// widget will use for HTTP fetches. When Discord's CDN 404s the
	// user's avatar (their hash points at a missing CDN object — happens
	// with broken uploads, mid-migration accounts, deleted avatars whose
	// hash is still in the cabinet, etc.), the official Discord client
	// quietly substitutes the user's default avatar. Mirror that here so
	// the user-bar shows _something_ instead of initials forever.
	avatarCtx := imgutil.WithOpts(ctx, imgutil.WithErrorFn(func(err error) {
		client := gtkcord.FromContext(ctx)
		me, _ := client.Me()
		if me == nil {
			slog.Warn("userBar: avatar fetch failed but no me to compute fallback", "err", err)
			return
		}
		// Construct the default avatar URL by calling AvatarURL on a User
		// stripped of the (broken) hash. arikawa returns the embed-avatar
		// URL when Avatar is empty, which is exactly the fallback path
		// every Discord client uses.
		defaultUser := discord.User{ID: me.ID, Discriminator: me.Discriminator}
		defaultURL := defaultUser.AvatarURL()
		if defaultURL == "" || b.fallbackURL == defaultURL {
			slog.Warn("userBar: avatar fetch failed and fallback unavailable / already failed",
				"err", err, "current_fallback", b.fallbackURL)
			return
		}
		b.fallbackURL = defaultURL
		slog.Warn("userBar: avatar URL failed, falling back to default Discord avatar",
			"err", err, "fallback_url", defaultURL)
		b.avatar.SetFromURL(defaultURL)
	}))
	b.avatar = onlineimage.NewAvatar(avatarCtx, imgutil.HTTPProvider, gtkcord.UserBarAvatarSize)
	b.avatar.AddCSSClass("user-bar-avatar")

	// mod: presence — overlay status dot on avatar, same as DM channel avatars
	b.avatarOverlay = gtk.NewOverlay()
	b.avatarOverlay.SetChild(b.avatar)

	b.name = gtk.NewLabel("")
	b.name.AddCSSClass("user-bar-name")
	b.name.SetSelectable(true)
	b.name.SetXAlign(0)
	b.name.SetHExpand(true)
	b.name.SetWrap(false)
	b.name.SetEllipsize(pango.EllipsizeEnd)

	b.menu = gtk.NewToggleButton()
	b.menu.AddCSSClass("user-bar-menu")
	b.menu.SetIconName("menu-large-symbolic")
	b.menu.SetTooltipText("Main Menu")
	b.menu.SetHasFrame(false)
	b.menu.SetVAlign(gtk.AlignCenter)
	b.menu.ConnectClicked(func() {
		p := gtkutil.NewPopoverMenuCustom(b.menu, gtk.PosTop, menuActions)
		p.ConnectHide(func() { b.menu.SetActive(false) })
		gtkutil.PopupFinally(p)
	})

	// mod: compactsidebar — clicking the avatar opens the menu,
	// so the menu is accessible when the menu button is hidden.
	avatarClick := gtk.NewGestureClick()
	avatarClick.ConnectReleased(func(n int, x, y float64) {
		p := gtkutil.NewPopoverMenuCustom(b.avatarOverlay, gtk.PosTop, menuActions)
		gtkutil.PopupFinally(p)
	})
	b.avatarOverlay.AddController(avatarClick)

	b.Box = gtk.NewBox(gtk.OrientationHorizontal, 0)
	b.Box.Append(b.avatarOverlay)
	b.Box.Append(b.name)
	b.Box.Append(b.menu)
	userBarCSS(b)

	// mod: compactsidebar — icon-only mode leaves just the avatar. The menu
	// stays reachable via the avatar click gesture set up above.
	mods.BindCompactSidebarRow(b.Box, b.name, b.menu)

	anim := b.avatar.EnableAnimation()
	anim.ConnectMotion(b)

	// mod: avatar — use BindWidget rather than BindHandler here. BindHandler
	// wraps the context in a visibility-gated canceller which starts in the
	// cancelled state until the widget maps, and only OnRenew()s the handler
	// registration once the widget is visible. By that time the ReadyEvent
	// has already been dispatched and we silently miss it — which is why
	// the user's own avatar never triggered a fetch in the previous version.
	// BindWidget registers the handler immediately and ties its lifetime to
	// the widget directly, so Ready arrives even if it fires before we're
	// mapped.
	client := gtkcord.FromContext(ctx)
	client.BindWidget(b,
		func(ev gateway.Event) {
			switch ev := ev.(type) {
			case *gateway.UserUpdateEvent:
				b.updateUser(&ev.User)
			case *gateway.ReadyEvent:
				// ReadyEvent has the full user with avatar hash.
				b.updateUser(&ev.User)
			}
		},
		(*gateway.UserUpdateEvent)(nil),
		(*gateway.ReadyEvent)(nil),
	)

	me, _ := client.Me()
	if me != nil {
		b.updateUser(me)

		// mod: presence — status dot on avatar, same as DM channel list
		dot := mods.NewPresenceDot(ctx, me.ID, 0)
		if dot != nil {
			dot.SetHAlign(gtk.AlignEnd)
			dot.SetVAlign(gtk.AlignEnd)
			b.avatarOverlay.AddOverlay(dot)
		}
	}

	// mod: avatar — listen for manual refresh requests
	// (win.refresh-avatar from the user menu). Bust the gotkit HTTP
	// cache entry for the current avatar URL, reset the fallback flag
	// so we'll try the primary URL again, Disable() the widget so its
	// same-URL dedup doesn't skip the reload, then re-set the URL.
	mods.AvatarRefreshSignaler.Connect(func() {
		curr, _ := client.Me()
		if curr == nil {
			return
		}
		url := gtkcord.InjectAvatarSize(curr.AvatarURL())
		if url != "" {
			mods.BustImageCache(url)
		}
		b.fallbackURL = ""
		b.avatar.Disable()
		b.updateUser(curr)
	})

	return b
}

func (b *userBar) updateUser(me *discord.User) {
	tag := me.Username
	if v, _ := strconv.Atoi(me.Discriminator); v != 0 {
		tag += `<span size="smaller">` + "#" + me.Discriminator + "</span>"
	}

	var name string
	if me.DisplayName != "" {
		name = me.DisplayName + "\n" + `<span size="smaller">` + tag + "</span>"
	} else {
		name = tag
	}

	displayName := me.DisplayName
	if displayName == "" {
		displayName = me.Username
	}

	b.avatar.SetText(displayName)
	if url := gtkcord.InjectAvatarSize(me.AvatarURL()); url != "" {
		b.avatar.SetFromURL(url)
	}
	b.name.SetMarkup(name)
	b.name.SetTooltipMarkup(name)
}

