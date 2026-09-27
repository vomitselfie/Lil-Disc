package composer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/diamondburned/gotkit/gtkutil/mediautil"
	"github.com/pkg/errors"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
	"github.com/vomitselfie/Lil-Disc/internal/mods"
)

const (
	// MessageLengthLimitNonNitro is the maximum number of characters allowed in a message.
	MessageLengthLimitNonNitro = 2000
	// MessageLengthLimitNitro is the maximum number of characters allowed in a
	// message if the user has Nitro.
	MessageLengthLimitNitro = 4000
)

var showAllEmojis = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Show All Emojis",
	Section:     "Composer",
	Description: "Show (and autocomplete) all emojis even if the user doesn't have Nitro.",
})

// File contains the filename and a callback to open the file that's called
// asynchronously.
type File struct {
	Name string
	Type string // MIME type
	Size int64
	Open func() (io.ReadCloser, error)
}

const spoilerPrefix = "SPOILER_"

// IsSpoiler returns whether the file is spoilered or not.
func (f File) IsSpoiler() bool { return strings.HasPrefix(f.Name, spoilerPrefix) }

// SetSpoiler sets the spoilered state of the file.
func (f *File) SetSpoiler(spoiler bool) {
	if spoiler {
		if !f.IsSpoiler() {
			f.Name = spoilerPrefix + f.Name
		}
	} else {
		if f.IsSpoiler() {
			f.Name = strings.TrimPrefix(f.Name, spoilerPrefix)
		}
	}
}

// SendingMessage is the message created to be sent.
type SendingMessage struct {
	Content      string
	Files        []*File
	ReplyingTo   discord.MessageID
	ReplyMention bool
	StickerIDs   []discord.StickerID // mod: stickerpicker
}

// Controller is the parent Controller for a View.
type Controller interface {
	SendMessage(SendingMessage)
	StopEditing()
	StopReplying()
	EditLastMessage() bool
	AddReaction(discord.MessageID, discord.APIEmoji)
	AddToast(*adw.Toast)
}

type typer struct {
	Markup string
	UserID discord.UserID
	Time   discord.UnixTimestamp
}

func findTyper(typers []typer, userID discord.UserID) *typer {
	for i, t := range typers {
		if t.UserID == userID {
			return &typers[i]
		}
	}
	return nil
}

const typerTimeout = 10 * time.Second

type replyingState uint8

const (
	notReplying replyingState = iota
	replyingMention
	replyingNoMention
)

type View struct {
	*gtk.Widget

	Input        *Input
	Placeholder  *gtk.Label
	UploadTray   *UploadTray
	EmojiChooser *gtk.EmojiChooser

	ctx  context.Context
	ctrl Controller
	chID discord.ChannelID

	bigBox *gtk.Box
	topBox *gtk.Box

	rightBox      *gtk.Box
	emojiButton   *gtk.MenuButton
	gifButton     *gtk.MenuButton    // mod: gifpicker
	stickerButton *gtk.MenuButton    // mod: stickerpicker
	sendButton    *gtk.Button

	leftBox      *gtk.Box
	uploadButton *gtk.Button

	msgLengthLabel *gtk.Label
	msgLengthToast *adw.Toast
	isOverLimit    bool

	replyBar *mods.ReplyPreviewBar // mod: replypreview

	state struct {
		id       discord.MessageID
		editing  bool
		replying replyingState
	}
}

var viewCSS = lilcss.Applier("composer-view", `
	.composer-view * {
		/* Fix spacing for certain GTK themes such as stock Adwaita. */
		min-height: 0;
	}
	/* The composer floats: a raised, rounded card inset from the window
	   edges, rather than a strip of stock buttons pinned to the bottom. */
	.composer-view {
		margin: 0 {$space_lg} {$space_lg} {$space_lg};
		background-color: @lil_surface_raised;
		border: 1px solid @lil_border;
		border-radius: {$radius_xl};
		box-shadow: 0 4px 18px alpha(black, 0.10);
		transition: border-color 150ms ease, box-shadow 150ms ease;
	}
	.composer-view:focus-within {
		border-color: alpha(@lil_accent, 0.55);
		box-shadow: 0 0 0 3px alpha(@lil_accent, 0.12),
		            0 4px 18px alpha(black, 0.10);
	}
	.composer-view.composer-editing,
	.composer-view.composer-replying {
		border-color: alpha(@lil_accent, 0.45);
	}
	.composer-left-actions button,
	.composer-right-actions button {
		padding: {$space_sm};
		border-radius: {$radius_md};
		color: @lil_text_dim;
		background: none;
	}
	.composer-left-actions button:hover,
	.composer-right-actions button:hover {
		color: @lil_text;
		background: @lil_hover;
	}
	.composer-left-actions {
		margin: {$space_xs} {$space_sm};
	}
	.composer-right-actions button.toggle:checked {
		background-color: @lil_selected;
		color: @lil_accent_text;
	}
	.composer-right-actions {
		margin: {$space_xs} {$space_sm} {$space_xs} 0;
	}
	.composer-right-actions > *:not(:first-child) {
		margin-left: {$space_hair};
	}
	.composer-right-actions .composer-send {
		color: @lil_accent_text;
	}
	.composer-placeholder {
		padding: {$space_lg} {$space_hair};
		color: @lil_text_faint;
	}
	.composer-msg-length {
		font-size: {$font_micro};
		font-feature-settings: "tnum";
		margin: {$space_xs} {$space_md};
		opacity: 0;
		transition: opacity 0.1s;
	}
	.composer-msg-length.over-limit {
		color: @destructive_color;
		opacity: 1;
	}
`)

const (
	sendIcon   = "paper-plane-symbolic"
	emojiIcon  = "sentiment-satisfied-symbolic"
	editIcon   = "document-edit-symbolic"
	stopIcon   = "edit-clear-all-symbolic"
	replyIcon  = "mail-reply-sender-symbolic"
	uploadIcon = "list-add-symbolic"
)

func NewView(ctx context.Context, ctrl Controller, chID discord.ChannelID) *View {
	v := &View{
		ctx:  ctx,
		ctrl: ctrl,
		chID: chID,
	}

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetPropagateNaturalHeight(true)
	scroll.SetMaxContentHeight(1000)

	v.Placeholder = gtk.NewLabel("")
	v.Placeholder.AddCSSClass("composer-placeholder")
	v.Placeholder.SetVAlign(gtk.AlignStart)
	v.Placeholder.SetHAlign(gtk.AlignFill)
	v.Placeholder.SetXAlign(0)
	v.Placeholder.SetEllipsize(pango.EllipsizeEnd)

	revealer := gtk.NewRevealer()
	revealer.SetChild(v.Placeholder)
	revealer.SetCanTarget(false)
	revealer.SetRevealChild(true)
	revealer.SetTransitionType(gtk.RevealerTransitionTypeCrossfade)
	revealer.SetTransitionDuration(75)

	overlay := gtk.NewOverlay()
	overlay.AddCSSClass("composer-placeholder-overlay")
	overlay.SetChild(scroll)
	overlay.AddOverlay(revealer)
	overlay.SetClipOverlay(revealer, true)

	middle := gtk.NewBox(gtk.OrientationVertical, 0)
	middle.Append(overlay)

	v.uploadButton = newActionButton(actionButtonData{
		Name: "Upload File",
		Icon: uploadIcon,
		Func: v.upload,
	})

	v.leftBox = gtk.NewBox(gtk.OrientationHorizontal, 0)
	v.leftBox.AddCSSClass("composer-left-actions")
	v.leftBox.SetVAlign(gtk.AlignCenter)

	// mod: emojipicker — use custom server emoji picker if enabled,
	// fall back to GTK's built-in Unicode-only picker otherwise.
	if customPicker := mods.NewEmojiPickerPopover(ctx, 0, func(result mods.EmojiPickResult) {
		v.insertEmoji(result.Text)
	}); customPicker != nil {
		v.emojiButton = gtk.NewMenuButton()
		v.emojiButton.SetIconName(emojiIcon)
		v.emojiButton.AddCSSClass("flat")
		v.emojiButton.SetTooltipText(locale.Get("Choose Emoji"))
		v.emojiButton.SetPopover(customPicker)
	} else {
		v.EmojiChooser = gtk.NewEmojiChooser()
		v.EmojiChooser.ConnectEmojiPicked(func(emoji string) { v.insertEmoji(emoji) })

		v.emojiButton = gtk.NewMenuButton()
		v.emojiButton.SetIconName(emojiIcon)
		v.emojiButton.AddCSSClass("flat")
		v.emojiButton.SetTooltipText(locale.Get("Choose Emoji"))
		v.emojiButton.SetPopover(v.EmojiChooser)
	}

	// mod: gifpicker — GIF search button
	if gifPicker := mods.NewGifPickerPopover(ctx, func(gifURL string) {
		v.insertEmoji(gifURL)
		v.send()
	}); gifPicker != nil {
		v.gifButton = gtk.NewMenuButton()
		v.gifButton.SetLabel("GIF")
		v.gifButton.AddCSSClass("flat")
		v.gifButton.SetTooltipText(locale.Get("Search GIFs"))
		v.gifButton.SetPopover(gifPicker)
	}

	// mod: stickerpicker — resolve guild ID for sticker scope
	var stickerGuildID discord.GuildID
	if gState := gtkcord.FromContext(ctx); gState != nil {
		if ch, err := gState.Cabinet.Channel(chID); err == nil {
			stickerGuildID = ch.GuildID
		}
	}

	// mod: stickerpicker — Sticker picker button
	if stickerPicker := mods.NewStickerPickerPopover(ctx, stickerGuildID, func(result mods.StickerPickResult) {
		v.ctrl.SendMessage(SendingMessage{
			StickerIDs:   []discord.StickerID{result.StickerID},
			ReplyingTo:   v.state.id,
			ReplyMention: v.state.replying == replyingMention,
		})
		if v.state.replying != notReplying {
			v.ctrl.StopReplying()
		}
	}); stickerPicker != nil {
		v.stickerButton = gtk.NewMenuButton()
		v.stickerButton.SetIconName("emoji-symbols-symbolic")
		v.stickerButton.AddCSSClass("flat")
		v.stickerButton.SetTooltipText(locale.Get("Choose Sticker"))
		v.stickerButton.SetPopover(stickerPicker)
	}

	v.sendButton = gtk.NewButtonFromIconName(sendIcon)
	v.sendButton.AddCSSClass("composer-send")
	v.sendButton.SetTooltipText(locale.Get("Send Message"))
	v.sendButton.SetHasFrame(false)
	v.sendButton.ConnectClicked(v.send)

	v.rightBox = gtk.NewBox(gtk.OrientationHorizontal, 0)
	v.rightBox.AddCSSClass("composer-right-actions")
	v.rightBox.SetVAlign(gtk.AlignCenter)

	v.resetAction()

	v.topBox = gtk.NewBox(gtk.OrientationHorizontal, 0)
	v.topBox.SetVAlign(gtk.AlignEnd)
	v.topBox.Append(v.leftBox)
	v.topBox.Append(middle)
	v.topBox.Append(v.rightBox)

	v.msgLengthLabel = gtk.NewLabel("")
	v.msgLengthLabel.AddCSSClass("composer-msg-length")
	v.msgLengthLabel.SetCanTarget(false)
	v.msgLengthLabel.SetVAlign(gtk.AlignEnd)
	v.msgLengthLabel.SetHAlign(gtk.AlignEnd)

	topBoxOverlay := gtk.NewOverlay()
	topBoxOverlay.SetChild(v.topBox)
	topBoxOverlay.AddOverlay(v.msgLengthLabel)

	// mod: replypreview — reply preview bar above composer
	v.replyBar = mods.NewReplyPreviewBar(func() { v.ctrl.StopReplying() })

	v.bigBox = gtk.NewBox(gtk.OrientationVertical, 0)
	if v.replyBar != nil {
		v.bigBox.Append(v.replyBar)
	}
	v.bigBox.Append(topBoxOverlay)

	v.Input = NewInput(ctx, inputControllerView{v}, chID)
	scroll.SetChild(v.Input)

	v.UploadTray = NewUploadTray()
	v.bigBox.Append(v.UploadTray)

	// mod: mediahost — when a file is too large for Discord, instead of
	// dropping it / nagging about Nitro, push it to an external media host
	// (with 0x0.st as a fallback) and paste the resulting URL into the
	// composer at the cursor position. handleOversizeUpload manages a
	// placeholder text range so the user can see what's uploading.
	v.UploadTray.SetOversizeHandler(func(f *File) {
		v.handleOversizeUpload(f)
	})

	v.Widget = &v.bigBox.Widget
	v.SetPlaceholderMarkup("")

	// Show or hide the placeholder when the buffer is empty or not.
	updatePlaceholderVisibility := func() {
		start, end := v.Input.Buffer.Bounds()
		// Reveal if the buffer has 0 length.
		revealer.SetRevealChild(start.Offset() == end.Offset())
	}
	v.Input.Buffer.ConnectChanged(updatePlaceholderVisibility)
	updatePlaceholderVisibility()

	// mod: dragdrop — enable file drag-and-drop onto composer input
	// Attach to the Input (TextView) directly so we intercept before the
	// TextView's default handler (which would paste the file path as text).
	mods.SetupDragDrop(ctx, v.Input, func(f mods.DroppedFile) {
		v.addFileToTray(&File{
			Name: f.Name,
			Type: f.Type,
			Size: f.Size,
			Open: f.Open,
		})
	})

	viewCSS(v)
	return v
}

// SetPlaceholder sets the composer's placeholder. The default is used if an
// empty string is given.
func (v *View) SetPlaceholderMarkup(markup string) {
	if markup == "" {
		v.ResetPlaceholder()
		return
	}

	v.Placeholder.SetMarkup(markup)
}

func (v *View) ResetPlaceholder() {
	v.Placeholder.SetText("Message " + gtkcord.ChannelNameFromID(v.ctx, v.chID))
}

// actionButton is a button that is used in the composer bar.
type actionButton interface {
	newButton() gtk.Widgetter
}

// existingActionButton is a button that already exists in the composer bar.
type existingActionButton struct{ gtk.Widgetter }

func (a existingActionButton) newButton() gtk.Widgetter { return a }

// actionButtonData is the data that the action button in the composer bar is
// currently doing.
type actionButtonData struct {
	Name locale.Localized
	Icon string
	Func func()
}

func newActionButton(a actionButtonData) *gtk.Button {
	button := gtk.NewButton()
	button.AddCSSClass("composer-action")
	button.SetHasFrame(false)
	button.SetHAlign(gtk.AlignCenter)
	button.SetSensitive(a.Func != nil)
	button.SetIconName(a.Icon)
	button.SetTooltipText(a.Name.String())
	button.ConnectClicked(func() { a.Func() })

	return button
}

func (a actionButtonData) newButton() gtk.Widgetter {
	return newActionButton(a)
}

type actions struct {
	left  []actionButton
	right []actionButton
}

// setAction sets the action of the button in the composer.
func (v *View) setActions(actions actions) {
	gtkutil.RemoveChildren(v.leftBox)
	gtkutil.RemoveChildren(v.rightBox)

	for _, a := range actions.left {
		v.leftBox.Append(a.newButton())
	}
	for _, a := range actions.right {
		v.rightBox.Append(a.newButton())
	}
}

func (v *View) resetAction() {
	// mod: gifpicker, stickerpicker — add picker buttons to right side
	rightButtons := []actionButton{existingActionButton{v.emojiButton}}
	if v.gifButton != nil {
		rightButtons = append(rightButtons, existingActionButton{v.gifButton})
	}
	if v.stickerButton != nil {
		rightButtons = append(rightButtons, existingActionButton{v.stickerButton})
	}
	rightButtons = append(rightButtons, existingActionButton{v.sendButton})

	v.setActions(actions{
		left:  []actionButton{existingActionButton{v.uploadButton}},
		right: rightButtons,
	})
}

func (v *View) upload() {
	d := gtk.NewFileDialog()
	d.SetTitle(app.FromContext(v.ctx).SuffixedTitle(locale.Get("Upload Files")))
	d.OpenMultiple(v.ctx, app.GTKWindowFromContext(v.ctx), func(async gio.AsyncResulter) {
		files, err := d.OpenMultipleFinish(async)
		if err != nil {
			return
		}
		v.addFiles(files)
	})
}

func (v *View) addFiles(list gio.ListModeller) {
	go func() {
		var i uint
		for v.ctx.Err() == nil {
			obj := list.Item(i)
			if obj == nil {
				break
			}

			file := obj.Cast().(gio.Filer)
			path := file.Path()

			f := &File{
				Name: file.Basename(),
				Type: mediautil.FileMIME(v.ctx, file),
				Size: mediautil.FileSize(v.ctx, file),
			}

			if path != "" {
				f.Open = func() (io.ReadCloser, error) {
					return os.Open(path)
				}
			} else {
				f.Open = func() (io.ReadCloser, error) {
					r, err := file.Read(v.ctx)
					if err != nil {
						return nil, err
					}
					return gioutil.Reader(v.ctx, r), nil
				}
			}

			glib.IdleAdd(func() { v.addFileToTray(f) })
			i++
		}
	}()
}

// addFileToTray is the single entry point for adding a file to the upload
// tray from any code path (file picker, drag-drop, clipboard paste). It
// refreshes the tray's max upload size from the current channel's
// effective Discord limit before delegating to AddFile, so the oversize
// check (and the mediahost fallback handler) sees the correct ceiling.
// Must be called on the GTK main thread.
func (v *View) addFileToTray(f *File) {
	state := gtkcord.FromContext(v.ctx)

	// arikawa derives this from the account's Nitro tier and the guild's boost
	// level, but its free-tier figure is a constant compiled into the library
	// (api.UploadSizeLimit) and Discord has moved that number more than once.
	// Raising the floor here keeps a stale dependency from sending files to a
	// third-party host that Discord would have accepted, without touching the
	// Nitro and boost tiers, which are higher and still come from arikawa.
	maxUploadSize := int64(state.DetermineUploadSize(v.Input.GuildID()))
	if floor := mods.FreeUploadLimit(); floor > maxUploadSize {
		maxUploadSize = floor
	}

	v.UploadTray.SetMaxUploadSize(maxUploadSize)
	v.UploadTray.AddFile(v.ctx, f)
}

// handleOversizeUpload is the oversize handler installed on UploadTray.
// It inserts a placeholder string at the cursor position so the user can
// see what's uploading, kicks off the media-host upload on a background
// goroutine, and replaces the placeholder with the resulting URL (or an
// error marker) when the upload terminates.
//
// The placeholder range is tracked via two gtk.TextMarks with opposing
// gravities so the slot survives the user typing around it.
func (v *View) handleOversizeUpload(f *File) {
	buf := v.Input.Buffer
	placeholder := "[uploading " + f.Name + " to 0x0.st…] "

	// Insert at the current cursor position. CreateMark with leftGravity=true
	// makes startMark stick at the start of the inserted text; the default
	// (leftGravity=false) makes endMark stick at the end. This way the user
	// can keep typing before or after the placeholder without breaking it.
	cursor := buf.IterAtMark(buf.GetInsert())
	startMark := buf.CreateMark("", cursor, true)
	buf.Insert(cursor, placeholder)
	endIter := buf.IterAtMark(buf.GetInsert())
	endMark := buf.CreateMark("", endIter, false)

	mods.UploadToMediaHost(v.ctx, mods.MediaUploadRequest{
		Name:     f.Name,
		MIMEType: f.Type,
		Size:     f.Size,
		Open:     f.Open,
	}, func(url string, err error) {
		// Always on the GTK main thread.
		startIter := buf.IterAtMark(startMark)
		endIter := buf.IterAtMark(endMark)
		buf.Delete(startIter, endIter)

		replaceAt := buf.IterAtMark(startMark)
		var final string
		if err != nil {
			slog.Warn("composer: media host upload failed",
				"name", f.Name, "err", err)
			final = "[upload failed: " + f.Name + "] "
		} else {
			final = url + " "
		}
		buf.Insert(replaceAt, final)

		buf.DeleteMark(startMark)
		buf.DeleteMark(endMark)
	})
}

func (v *View) peekContent() (string, []*File) {
	start, end := v.Input.Buffer.Bounds()
	text := v.Input.Buffer.Text(start, end, false)
	files := v.UploadTray.Files()
	return text, files
}

func (v *View) commitContent() (string, []*File) {
	start, end := v.Input.Buffer.Bounds()
	text := v.Input.Buffer.Text(start, end, false)
	v.Input.Buffer.Delete(start, end)
	files := v.UploadTray.Clear()
	return text, files
}

func (v *View) insertEmoji(emoji string) {
	endIter := v.Input.Buffer.EndIter()
	v.Input.Buffer.Insert(endIter, emoji)
}

func (v *View) send() {
	if v.isOverLimit {
		if v.msgLengthToast == nil {
			v.msgLengthToast = adw.NewToast(locale.Get("Your message is too long."))
			v.msgLengthToast.SetTimeout(0)
			v.msgLengthToast.ConnectDismissed(func() { v.msgLengthToast = nil })

			v.ctrl.AddToast(v.msgLengthToast)
		}
		return
	} else {
		if v.msgLengthToast != nil {
			v.msgLengthToast.Dismiss()
		}
	}

	if v.state.editing {
		v.edit()
		return
	}

	text, files := v.commitContent()
	if text == "" && len(files) == 0 {
		return
	}

	if len(files) == 0 && textBufferIsReaction(text) {
		state := gtkcord.FromContext(v.ctx).Online()

		var targetMessageID discord.MessageID
		if v.state.replying != notReplying {
			targetMessageID = v.state.id
		} else {
			msgs, _ := state.Cabinet.Messages(v.chID)
			if len(msgs) > 0 {
				targetMessageID = msgs[0].ID
			}
		}

		if targetMessageID.IsValid() {
			text = strings.TrimPrefix(text, "+")
			text = strings.TrimSpace(text)
			text = strings.Trim(text, "<>")

			state := gtkcord.FromContext(v.ctx).Online()
			emoji := discord.APIEmoji(text)
			chID := v.chID
			go func() {
				if err := state.React(chID, targetMessageID, emoji); err != nil {
					slog.Error(
						"cannot react to message",
						"channel", chID,
						"message", targetMessageID,
						"emoji", emoji,
						"err", err)
					app.Error(v.ctx, errors.Wrap(err, "cannot react to message"))
				}
			}()

			v.ctrl.StopReplying()
			return
		}
	}

	v.ctrl.SendMessage(SendingMessage{
		Content:      text,
		Files:        files,
		ReplyingTo:   v.state.id,
		ReplyMention: v.state.replying == replyingMention,
	})

	if v.state.replying != notReplying {
		v.ctrl.StopReplying()
	}
}

// textBufferIsReaction returns whether the text buffer is for adding a reaction.
// It is true if the input matches something like "+<emoji>".
func textBufferIsReaction(buffer string) bool {
	buffer = strings.TrimRightFunc(buffer, unicode.IsSpace)
	return strings.HasPrefix(buffer, "+") && !strings.ContainsFunc(buffer, unicode.IsSpace)
}

func (v *View) edit() {
	editingID := v.state.id
	text, _ := v.commitContent()

	state := gtkcord.FromContext(v.ctx).Online()

	gtkutil.Async(v.ctx, func() func() {
		_, err := state.EditMessage(v.chID, editingID, text)
		if err != nil {
			err = errors.Wrap(err, "cannot edit message")
			slog.Error(
				"cannot edit message",
				"err", err)

			return func() {
				toast := adw.NewToast(locale.Get("Cannot edit message"))
				toast.SetTimeout(0)
				toast.SetButtonLabel(locale.Get("Logs"))
				toast.SetActionName("app.logs")
				v.ctrl.AddToast(toast)
			}
		}
		return nil
	})

	v.ctrl.StopEditing()
}

// StartEditing starts editing the given message. The message is edited once the
// user hits send.
func (v *View) StartEditing(msg *discord.Message) {
	v.restart()

	v.state.id = msg.ID
	v.state.editing = true

	v.Input.Buffer.SetText(msg.Content)
	v.SetPlaceholderMarkup(locale.Get("Editing message"))
	v.AddCSSClass("composer-editing")
	v.setActions(actions{
		left: []actionButton{
			actionButtonData{
				Name: "Stop Editing",
				Icon: stopIcon,
				Func: v.ctrl.StopEditing,
			},
		},
		right: []actionButton{
			actionButtonData{
				Name: "Edit",
				Icon: editIcon,
				Func: v.edit,
			},
		},
	})
}

// StopEditing stops editing.
func (v *View) StopEditing() {
	if !v.state.editing {
		return
	}

	v.state.id = 0
	v.state.editing = false
	start, end := v.Input.Buffer.Bounds()
	v.Input.Buffer.Delete(start, end)

	v.SetPlaceholderMarkup("")
	v.RemoveCSSClass("composer-editing")
	v.resetAction()
}

// StartReplyingTo starts replying to the given message. Visually, there is no
// difference except for the send button being different.
func (v *View) StartReplyingTo(msg *discord.Message) {
	v.restart()

	v.state.id = msg.ID
	v.state.replying = replyingMention

	v.AddCSSClass("composer-replying")

	// mod: replypreview — show preview bar
	v.replyBar.ShowReply(v.ctx, msg)

	state := gtkcord.FromContext(v.ctx)
	v.SetPlaceholderMarkup(fmt.Sprintf(
		"Replying to %s",
		state.AuthorMarkup(&gateway.MessageCreateEvent{Message: *msg}),
	))

	mentionToggle := gtk.NewToggleButton()
	mentionToggle.AddCSSClass("composer-mention-toggle")
	mentionToggle.SetIconName("online-symbolic")
	mentionToggle.SetHasFrame(false)
	mentionToggle.SetActive(true)
	mentionToggle.SetHAlign(gtk.AlignCenter)
	mentionToggle.SetVAlign(gtk.AlignCenter)
	mentionToggle.ConnectToggled(func() {
		if mentionToggle.Active() {
			v.state.replying = replyingMention
		} else {
			v.state.replying = replyingNoMention
		}
	})

	v.setActions(actions{
		left: []actionButton{
			existingActionButton{v.uploadButton},
		},
		right: []actionButton{
			existingActionButton{v.emojiButton},
			existingActionButton{mentionToggle},
			actionButtonData{
				Name: "Reply",
				Icon: replyIcon,
				Func: v.send,
			},
		},
	})
}

// StopReplying undoes the start call.
func (v *View) StopReplying() {
	if v.state.replying == 0 {
		return
	}

	v.state.id = 0
	v.state.replying = 0

	// mod: replypreview — hide preview bar
	v.replyBar.Hide()

	v.SetPlaceholderMarkup("")
	v.RemoveCSSClass("composer-replying")
	v.resetAction()
}

func (v *View) restart() bool {
	state := v.state

	if v.state.editing {
		v.ctrl.StopEditing()
	}
	if v.state.replying != notReplying {
		v.ctrl.StopReplying()
	}

	return state.editing || state.replying != notReplying
}

func (v *View) UpdateMessageLength(length int) {
	state := gtkcord.FromContext(v.ctx)
	limit := MessageLengthLimitNonNitro
	if state.EmojiState.HasNitro() {
		limit = MessageLengthLimitNitro
	}

	if length > limit-100 {
		// Hack to not update the label too often.
		v.msgLengthLabel.SetText(fmt.Sprintf("%d / %d", length, limit))
	}

	overLimit := length > limit
	if overLimit == v.isOverLimit {
		return
	}

	v.isOverLimit = overLimit
	if overLimit {
		v.msgLengthLabel.AddCSSClass("over-limit")
	} else {
		v.msgLengthLabel.RemoveCSSClass("over-limit")
	}
}

// inputControllerView implements InputController.
type inputControllerView struct {
	*View
}

func (v inputControllerView) Send()        { v.send() }
func (v inputControllerView) Escape() bool { return v.restart() }

func (v inputControllerView) EditLastMessage() bool {
	return v.ctrl.EditLastMessage()
}

func (v inputControllerView) PasteClipboardFile(file *File) {
	v.addFileToTray(file)
}

func (v inputControllerView) UpdateMessageLength(length int) {
	v.View.UpdateMessageLength(length)
}
