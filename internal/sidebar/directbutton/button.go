package directbutton

import (
	"context"
	"math"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/dijama/lildisc/internal/gtkcord"
	"github.com/dijama/lildisc/internal/lilcss"
	"github.com/dijama/lildisc/internal/sidebar/sidebutton"
)

type Button struct {
	*gtk.Overlay
	Pill   *sidebutton.Pill
	Button *gtk.Button

	ctx context.Context
}

var dmButtonCSS = lilcss.Applier("sidebar-dm-button-overlay", `
	.sidebar-dm-button {
		padding: {$space_xs} {$space_lg};
		border-radius: 0;
	}
	.sidebar-dm-button image {
		padding-top: {$space_xs};
		padding-bottom: {$space_xs};
		border-radius: calc({$guild_icon_size} / 2);
		transition: 200ms ease;
		transition-property: all;
	}
	/* The DM button sits in the same rail as the guild buttons but is a plain
	   gtk.Button with its own class, so it never picked up their hover
	   treatment — the one rail had two different responses to the pointer. */
	.sidebar-dm-button:hover image {
		border-radius: calc({$guild_icon_size} / 4);
		background-color: alpha(@lil_accent, 0.35);
	}
`)

func NewButton(ctx context.Context) *Button {
	b := Button{ctx: ctx}

	icon := gtk.NewImageFromIconName("chat-bubbles-empty-symbolic")
	icon.SetIconSize(gtk.IconSizeLarge)
	icon.SetPixelSize(int(math.Round(gtkcord.GuildIconSize * 0.85)))

	b.Button = gtk.NewButton()
	b.Button.AddCSSClass("sidebar-dm-button")
	b.Button.SetTooltipText("Direct Messages")
	b.Button.SetChild(icon)
	b.Button.SetHasFrame(false)
	b.Button.ConnectClicked(func() {
		b.Pill.State = sidebutton.PillActive
		b.Pill.Invalidate()

		parent := gtk.BaseWidget(b.Button.Parent())
		parent.ActivateAction("win.open-dms", nil)
	})

	b.Pill = sidebutton.NewPill()

	b.Overlay = gtk.NewOverlay()
	b.Overlay.SetChild(b.Button)
	b.Overlay.AddOverlay(b.Pill)

	dmButtonCSS(b)
	return &b
}
