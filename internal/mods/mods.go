package mods

import (
	"context"
	"log/slog"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
)

// ActionWidget is a widget that can also have actions and shortcuts attached.
type ActionWidget interface {
	gtk.Widgetter
	gio.ActionMapper
}

// ActiveChannel reports the channel open in the window's current tab, or 0
// when none is. The window installs it; mods cannot import the window.
var ActiveChannel = func() discord.ChannelID { return 0 }

// Init initializes mods that don't require Discord state.
// Call after the application and window are ready.
func Init(ctx context.Context, win ActionWidget) {
	slog.Info("initializing lildisc mods")
	CleanImageCache()
	initCustomCSS(ctx)
	initKeybinds(ctx, win)
	initTray(ctx, win)
}

// HookState initializes mods that require Discord state.
// Call after the gateway connection is established and state is injected.
func HookState(ctx context.Context, state *gtkcord.State, win ActionWidget) {
	slog.Info("initializing state-dependent lildisc mods")
	initNotifications(ctx, state, win)
	InitSearch(ctx, win)
	initHistory(ctx, state, win)
	// mod: autoidle — report idle so Discord hands notifications to mobile
	SetupAutoIdle(state, win)
	// mod: friend nicknames — fetch from API in background
	go state.FetchFriendNicknames()
}
