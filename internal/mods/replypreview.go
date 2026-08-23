package mods

import (
	"context"
	"html"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
	"github.com/dijama/lildisc/internal/gtkcord"
)

var enableReplyPreview = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Reply Preview Bar",
	Section:     "Mods",
	Description: "Show a preview bar above the composer when replying to a message.",
})

var _ = cssutil.WriteCSS(`
	.mod-reply-bar {
		padding: 4px 12px;
		border-bottom: 1px solid alpha(@borders, 0.5);
		background: alpha(@theme_selected_bg_color, 0.1);
	}
	.mod-reply-bar-label {
		font-size: 0.85em;
	}
	.mod-reply-bar-close {
		min-height: 20px;
		min-width: 20px;
		padding: 0;
	}
`)

// ReplyPreviewBar is a widget that shows a preview of the message being replied to.
type ReplyPreviewBar struct {
	*gtk.Revealer
	box     *gtk.Box
	label   *gtk.Label
	closeBtn *gtk.Button
	onClose  func()
}

// NewReplyPreviewBar creates a new reply preview bar.
// Returns nil if the mod is disabled.
func NewReplyPreviewBar(onClose func()) *ReplyPreviewBar {
	if !enableReplyPreview.Value() {
		return nil
	}

	bar := &ReplyPreviewBar{onClose: onClose}

	bar.label = gtk.NewLabel("")
	bar.label.AddCSSClass("mod-reply-bar-label")
	bar.label.SetXAlign(0)
	bar.label.SetHExpand(true)
	bar.label.SetEllipsize(pango.EllipsizeEnd)
	bar.label.SetSingleLineMode(true)

	bar.closeBtn = gtk.NewButtonFromIconName("window-close-symbolic")
	bar.closeBtn.AddCSSClass("mod-reply-bar-close")
	bar.closeBtn.AddCSSClass("flat")
	bar.closeBtn.SetTooltipText("Cancel Reply")
	bar.closeBtn.ConnectClicked(func() {
		if bar.onClose != nil {
			bar.onClose()
		}
	})

	bar.box = gtk.NewBox(gtk.OrientationHorizontal, 8)
	bar.box.AddCSSClass("mod-reply-bar")
	bar.box.Append(bar.label)
	bar.box.Append(bar.closeBtn)

	bar.Revealer = gtk.NewRevealer()
	bar.Revealer.SetTransitionType(gtk.RevealerTransitionTypeSlideDown)
	bar.Revealer.SetChild(bar.box)
	bar.Revealer.SetRevealChild(false)

	return bar
}

// ShowReply shows the reply preview for the given message.
func (bar *ReplyPreviewBar) ShowReply(ctx context.Context, msg *discord.Message) {
	if bar == nil {
		return
	}

	state := gtkcord.FromContext(ctx)
	authorMarkup := state.AuthorMarkup(&gateway.MessageCreateEvent{Message: *msg})
	preview := state.MessagePreview(msg)
	// Flatten newlines to single spaces for the single-line bar, and
	// escape the plaintext preview — SetMarkup would otherwise treat
	// "<" / "&" in quoted content as markup and silently drop chars.
	preview = strings.ReplaceAll(preview, "\n", " ")
	preview = html.EscapeString(preview)

	bar.label.SetMarkup("Replying to <b>" + authorMarkup + "</b>: " + preview)
	bar.Revealer.SetRevealChild(true)
}

// Hide hides the reply preview bar.
func (bar *ReplyPreviewBar) Hide() {
	if bar == nil {
		return
	}
	bar.Revealer.SetRevealChild(false)
}
