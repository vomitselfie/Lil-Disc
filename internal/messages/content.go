package messages

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/vomitselfie/Lil-Disc/chatkit/components/author"
	"github.com/vomitselfie/Lil-Disc/chatkit/md"
	"github.com/vomitselfie/Lil-Disc/chatkit/md/mdrender"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"
	"github.com/diamondburned/ningen/v3/discordmd"
	"libdb.so/ctxt"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
	"github.com/vomitselfie/Lil-Disc/internal/mods"
)

// Content is the message content widget.
type Content struct {
	*gtk.Box
	ctx    context.Context
	view   *View
	menu   *gio.Menu
	mdview *mdrender.MarkdownViewer
	react  *contentReactions
	child  []gtk.Widgetter

	// appendTo redirects append while a forwarded message's body is being
	// built, so the whole forwarded block lands inside one bordered box
	// rather than flat among the sender's own widgets. nil means c.Box.
	appendTo *gtk.Box

	chID  discord.ChannelID
	msgID discord.MessageID
}

var contentCSS = lilcss.Applier("message-content-box", `
	.message-content-box {
		margin-right: 4px;
	}
	.message-content-box > *:not(:first-child) {
		margin-top: 0.15em;
	}
	.message-content-box .thumbnail-embed {
		border-width: 0;
		border-radius: 8px; /* stolen from Discord mobile */
	}
	.message-header-blockquote {
		margin-bottom: 0;
	}
	/* Blockquotes inside messages: chatkit's :not(:last-child) adds 3px
	   bottom margin between consecutive > lines — halve it for tighter feel. */
	.message-content-box .md-blockquote:not(:last-child) {
		margin-bottom: 1px;
	}
	.message-header-blockquote > *,
	.message-header-blockquote .mauthor-chip,
	.message-reply-content link {
		color: mix(@theme_bg_color, @theme_fg_color, 0.85);
	}
	.message-header-blockquote > * {
		font-size: 0.9em;
	}
	.message-reply-thumb {
		border-radius: {$radius_sm};
		background-color: @lil_surface_raised;
	}
	.message-interaction-name {
		margin-left: 0.25em;
		font-family: monospace;
	}
	/* Forwarded messages. Discord sets the forwarded body apart with a rule
	   down its left edge and a muted label above it, so that a forward is
	   never mistaken for something the sender wrote. */
	.message-forward-header {
		color: @lil_text_faint;
		font-size: {$font_small};
		margin-bottom: 1px;
	}
	.message-forward-header image {
		margin-right: {$space_xs};
	}
	.message-forward-body {
		border-left: 2px solid @lil_border_strong;
		padding-left: {$space_md};
	}
`)

// NewContent creates a new Content widget.
func NewContent(ctx context.Context, v *View) *Content {
	c := Content{
		ctx:   ctx,
		view:  v,
		child: make([]gtk.Widgetter, 0, 2),
		chID:  v.ChannelID(),
	}
	c.Box = gtk.NewBox(gtk.OrientationVertical, 0)
	contentCSS(c.Box)

	return &c
}

// MessageID returns the message ID.
func (c *Content) MessageID() discord.MessageID {
	return c.msgID
}

// ChannelID returns the channel ID.
func (c *Content) ChannelID() discord.ChannelID {
	return c.chID
}

// SetExtraMenu implements ExtraMenuSetter.
func (c *Content) SetExtraMenu(menu gio.MenuModeller) {
	c.menu = gio.NewMenu()
	c.menu.InsertSection(0, locale.Get("Message"), menu)

	if c.mdview != nil {
		c.setMenu()
	}
}

type extraMenuSetter interface{ SetExtraMenu(gio.MenuModeller) }

var (
	_ extraMenuSetter = (*gtk.TextView)(nil)
	_ extraMenuSetter = (*gtk.Label)(nil)
)

func (c *Content) setMenu() {
	var menu gio.MenuModeller
	if c.menu != nil {
		menu = c.menu // because a nil interface{} != nil *T
	}

	for _, child := range c.child {
		// Manually check on child to allow certain widgets to override the
		// method.
		s, ok := child.(extraMenuSetter)
		if ok {
			s.SetExtraMenu(menu)
		}

		gtkutil.WalkWidget(c.Box, func(w gtk.Widgetter) bool {
			s, ok := w.(extraMenuSetter)
			if ok {
				s.SetExtraMenu(menu)
			}
			return false
		})
	}
}

var systemContentCSS = lilcss.Applier("message-system-content", `
	.message-system-content {
		font-style: italic;
		color: alpha(@theme_fg_color, 0.9);
	}
`)

// Update replaces Content with the message.
func (c *Content) Update(m *discord.Message, customs ...gtk.Widgetter) {
	c.msgID = m.ID
	c.clear()

	// Prevent the Markdown parser from crashing the client.
	// See https://github.com/diamondburned/dissent/issues/275.
	defer func() {
		if r := recover(); r != nil {
			slog.Error(
				"recovered from a panic while parsing markdown",
				"message_id", m.ID,
				"content", m.Content,
				"panic", r)

			app.Error(c.ctx, errors.New(
				locale.Get("Panic caught while parsing markdown, please check logs.")))
			c.Redact()
		}
	}()

	state := gtkcord.FromContext(c.ctx)

	// mod: forwards — a forward carries none of its own content. Discord puts
	// the text, attachments and embeds in a snapshot taken when it was
	// forwarded, leaves the outer message's fields empty, and points the
	// reference at the original, which is usually in a channel this account
	// cannot read. Rendering the outer message alone therefore produced an
	// empty body under a stray "Unknown message." from the reply box.
	//
	// Everything below renders `body` rather than `m`. The two differ only
	// for a forward; `m` stays the message that owns the reactions and the
	// context menu.
	body := m
	if snapshot := gtkcord.ForwardedSnapshot(m); snapshot != nil {
		c.append(newForwardHeader())

		forwardBody := gtk.NewBox(gtk.OrientationVertical, 0)
		forwardBody.AddCSSClass("message-forward-body")
		c.append(forwardBody)
		c.appendTo = forwardBody

		forwarded := *m
		forwarded.Content = snapshot.Content
		forwarded.Embeds = snapshot.Embeds
		forwarded.Attachments = snapshot.Attachments
		forwarded.Stickers = snapshot.Stickers
		forwarded.Mentions = snapshot.Mentions
		forwarded.MentionRoleIDs = snapshot.MentionRoleIDs
		// Keep the outer Type: it is always Default for a forward, and the
		// system-message switch below indexes Mentions for some types.
		//
		// Drop the reference so the reply box is not also drawn; it points at
		// the forwarded-from message, not at something being replied to.
		forwarded.Reference = nil

		body = &forwarded
	} else if m.Reference != nil {
		w := c.newReplyBox(m)
		c.append(w)
	}

	if m.Interaction != nil {
		w := c.newInteractionBox(m)
		c.append(w)
	}

	var messageMarkup string
	switch m.Type {
	case discord.GuildMemberJoinMessage:
		messageMarkup = locale.Get("Joined the server.")
	case discord.CallMessage:
		messageMarkup = locale.Get("Calling you.")
	case discord.ChannelIconChangeMessage:
		messageMarkup = locale.Get("Changed the channel icon.")
	case discord.ChannelNameChangeMessage:
		messageMarkup = locale.Get("Changed the channel name to #%s.", html.EscapeString(m.Content))
	case discord.ChannelPinnedMessage:
		messageMarkup = locale.Get(`Pinned <a href="#message/%d">a message</a>.`, m.ID)
	case discord.RecipientAddMessage, discord.RecipientRemoveMessage:
		mentioned := state.MemberMarkup(m.GuildID, &m.Mentions[0], author.WithMinimal())
		switch m.Type {
		case discord.RecipientAddMessage:
			messageMarkup = locale.Get("Added %s to the group.", mentioned)
		case discord.RecipientRemoveMessage:
			messageMarkup = locale.Get("Removed %s from the group.", mentioned)
		}
	case discord.NitroBoostMessage:
		messageMarkup = locale.Get("Boosted the server!")
	case discord.NitroTier1Message:
		messageMarkup = locale.Get("The server is now Nitro Boosted to Tier 1.")
	case discord.NitroTier2Message:
		messageMarkup = locale.Get("The server is now Nitro Boosted to Tier 2.")
	case discord.NitroTier3Message:
		messageMarkup = locale.Get("The server is now Nitro Boosted to Tier 3.")
	}

	c.mdview = nil

	switch {
	case messageMarkup != "":
		msg := gtk.NewLabel("")
		msg.SetMarkup(messageMarkup)
		msg.SetHExpand(true)
		msg.SetXAlign(0)
		msg.SetWrap(true)
		msg.SetWrapMode(pango.WrapWordChar)
		msg.ConnectActivateLink(func(uri string) bool {
			if !strings.HasPrefix(uri, "#") {
				return false // not our link
			}

			parts := strings.SplitN(uri, "/", 2)
			if len(parts) != 2 {
				return true // pretend we've handled this because of #
			}

			switch strings.TrimPrefix(parts[0], "#") {
			case "message":
				if id, _ := discord.ParseSnowflake(parts[1]); id.IsValid() {
					c.view.ScrollToMessage(discord.MessageID(id))
				}
			}

			return true
		})
		systemContentCSS(msg)
		fixNatWrap(msg)
		c.append(msg)

	// We render a big content if the content itself is literally a Unicode
	// emoji.
	case body.Content != "" && md.IsUnicodeEmoji(body.Content):
		l := gtk.NewLabel(body.Content)
		l.SetAttributes(gtkcord.EmojiAttrs)
		l.SetHExpand(true)
		l.SetXAlign(0)
		l.SetSelectable(true)
		l.SetWrap(true)
		l.SetWrapMode(pango.WrapWordChar)
		c.append(l)

	// We don't render the message content if all it is is the URL to the
	// embedded image, because that's what the official client does.
	case body.Content != "" &&
		!(len(body.Embeds) == 1 && body.Embeds[0].Type == discord.ImageEmbed && body.Embeds[0].URL == body.Content):

		src := []byte(body.Content)
		node := discordmd.ParseWithMessage(src, *state.Cabinet, body, true)

		c.mdview = mdrender.NewMarkdownViewer(
			ctxt.With(c.ctx, newMarkdownState()),
			src, node, renderers...)
		// mod: links — add underline to link tags. Chatkit's LinkTags
		// only sets foreground color; we add underline for clarity.
		// Use raw int (1 = PangoUnderlineSingle) since gotk4 may not
		// serialize the pango.Underline enum through SetObjectProperty.
		if aTag := c.mdview.TagTable().Lookup("a"); aTag != nil {
			aTag.SetObjectProperty("underline", 1)
			aTag.SetObjectProperty("underline-set", true)
		}
		if aHover := c.mdview.TagTable().Lookup("a:hover"); aHover != nil {
			aHover.SetObjectProperty("underline", 1)
			aHover.SetObjectProperty("underline-set", true)
		}
		c.append(c.mdview)
	}

	for i := range body.Stickers {
		v := newSticker(c.ctx, &body.Stickers[i])
		c.append(v)
	}

	for i := range body.Attachments {
		v := newAttachment(c.ctx, &body.Attachments[i])
		c.append(v)
	}

	// mod: embeds — group consecutive embeds with the same URL (e.g. Twitter
	// multi-image posts) so extra images render inside the first embed's card.
	for i := 0; i < len(body.Embeds); i++ {
		// Collect consecutive image-bearing embeds that share a URL.
		group := mods.GroupEmbeds(body.Embeds, i)
		if len(group) > 1 {
			v := newEmbedGroup(c.ctx, body, group)
			c.append(v)
			i += len(group) - 1 // skip grouped embeds
		} else {
			v := newEmbed(c.ctx, body, &body.Embeds[i])
			c.append(v)
		}
	}

	// End of the forwarded block, if there was one. What follows belongs to
	// this message rather than the one it quotes: the upload progress label
	// and the reactions are the sender's own.
	c.appendTo = nil

	for _, custom := range customs {
		c.append(custom)
	}

	c.SetReactions(m.Reactions)
	c.setMenu()
}

// newForwardHeader builds the muted "Forwarded" line that sits above a
// forwarded body, so a forward is never read as something the sender wrote.
//
// There is no author to name: Discord omits one from the snapshot, which is
// why this says only that the message was forwarded.
func newForwardHeader() gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("message-forward-header")
	box.SetHAlign(gtk.AlignStart)

	icon := gtk.NewImageFromIconName("mail-forward-symbolic")
	icon.SetPixelSize(12)

	label := gtk.NewLabel(locale.Get("Forwarded"))
	label.SetXAlign(0)

	box.Append(icon)
	box.Append(label)

	return box
}

func (c *Content) newReplyBox(m *discord.Message) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("md-blockquote")
	box.AddCSSClass("message-header-blockquote")
	box.AddCSSClass("message-reply-box")

	state := gtkcord.FromContext(c.ctx)

	referencedMsg := m.ReferencedMessage
	if referencedMsg == nil {
		referencedMsg, _ = state.Cabinet.Message(m.Reference.ChannelID, m.Reference.MessageID)
	}

	if referencedMsg == nil {
		slog.Warn(
			"Cannot display message reference because the message is not found",
			"channel_id", m.ChannelID,
			"guild_id", m.GuildID,
			"id", m.ID,
			"id_reference", m.Reference.MessageID)

		header := gtk.NewLabel("Unknown message.")
		header.AddCSSClass("message-reply-header")
		box.Append(header)

		return box
	}

	if !showBlockedMessages.Value() && state.UserIsBlocked(referencedMsg.Author.ID) {
		header := gtk.NewLabel("Blocked user.")
		header.AddCSSClass("message-reply-header")
		box.Append(header)

		blockedCSS(box)
		return box
	}

	member, _ := state.Cabinet.Member(m.Reference.GuildID, referencedMsg.Author.ID)
	chip := newAuthorChip(c.ctx, m.GuildID, &discord.GuildUser{
		User:   referencedMsg.Author,
		Member: member,
	})
	chip.SetHAlign(gtk.AlignStart)
	chip.Unpad()
	box.Append(chip)

	imgURL, imgProxy, imgW, imgH := replyImage(referencedMsg)
	// When the message content is exactly the URL of its single image
	// embed, Discord (and our main content renderer) drops the text —
	// mirror that here so the reply preview doesn't show the raw URL.
	isBareImageURL := imgURL != "" &&
		referencedMsg.Content != "" &&
		len(referencedMsg.Embeds) == 1 &&
		referencedMsg.Embeds[0].Type == discord.ImageEmbed &&
		string(referencedMsg.Embeds[0].URL) == referencedMsg.Content

	preview := state.MessagePreview(referencedMsg)
	showText := preview != "" && !isBareImageURL
	// If the preview text is only the attachment-filename fallback
	// (content empty, MessagePreview returned filenames), the thumbnail
	// alone is cleaner.
	if imgURL != "" && referencedMsg.Content == "" {
		showText = false
	}

	if showText || imgURL != "" {
		inner := gtk.NewBox(gtk.OrientationHorizontal, 8)

		activateScroll := func() {
			if !c.ActivateAction("messages.scroll-to",
				gtkcord.NewMessageIDVariant(m.ID)) {
				slog.Error(
					"Failed to activate messages.scroll-to",
					"id", m.ID)
			}
		}

		if showText {
			singleLine := strings.ReplaceAll(preview, "\n", " ")
			markup := fmt.Sprintf(
				`<a href="lildisc://reply">%s</a>`,
				html.EscapeString(singleLine),
			)

			reply := gtk.NewLabel(markup)
			reply.AddCSSClass("message-reply-content")
			reply.SetUseMarkup(true)
			reply.SetTooltipText(preview)
			reply.SetEllipsize(pango.EllipsizeEnd)
			reply.SetLines(1)
			reply.SetXAlign(0)
			reply.SetHExpand(true)
			reply.ConnectActivateLink(func(link string) bool {
				slog.Debug(
					"Activated message reference link",
					"link", link,
					"message_id", m.ID,
					"reference_id", referencedMsg.ID)

				if link != "lildisc://reply" {
					return false
				}
				activateScroll()
				return true
			})

			inner.Append(reply)
		}

		if imgURL != "" {
			thumb := onlineimage.NewPicture(c.ctx, imgutil.HTTPProvider)
			thumb.AddCSSClass("message-reply-thumb")
			thumb.SetSizeRequest(40, 40)
			thumb.SetContentFit(gtk.ContentFitCover)
			thumb.SetCanShrink(true)
			thumb.SetHAlign(gtk.AlignEnd)
			thumb.SetVAlign(gtk.AlignCenter)

			src := imgProxy
			if src == "" {
				src = imgURL
			}
			thumb.SetURL(gtkcord.InjectSizeUnscaled(src, 80))

			tooltip := "View referenced image"
			if imgW > 0 && imgH > 0 {
				tooltip = fmt.Sprintf("%dx%d", imgW, imgH)
			}
			thumb.SetTooltipText(tooltip)

			click := gtk.NewGestureClick()
			click.ConnectReleased(func(_ int, _, _ float64) { activateScroll() })
			thumb.AddController(click)

			inner.Append(thumb)
		}

		box.Append(inner)
	}

	if state.UserIsBlocked(referencedMsg.Author.ID) {
		blockedCSS(box)
	}

	return box
}

// replyImage finds the best preview image for a referenced message
// used in a reply header. Returns empty strings and zero dimensions
// when the message has no image-shaped content.
func replyImage(msg *discord.Message) (url, proxy string, w, h uint) {
	for i := range msg.Attachments {
		a := &msg.Attachments[i]
		if isImageAttachment(a) {
			return string(a.URL), string(a.Proxy), a.Width, a.Height
		}
	}
	for i := range msg.Embeds {
		e := &msg.Embeds[i]
		if e.Type != discord.ImageEmbed && e.Type != discord.GIFVEmbed {
			continue
		}
		if e.Image != nil {
			return string(e.Image.URL), string(e.Image.Proxy),
				e.Image.Width, e.Image.Height
		}
		if e.Thumbnail != nil {
			return string(e.Thumbnail.URL), string(e.Thumbnail.Proxy),
				e.Thumbnail.Width, e.Thumbnail.Height
		}
	}
	return "", "", 0, 0
}

func isImageAttachment(a *discord.Attachment) bool {
	if strings.HasPrefix(a.ContentType, "image/") {
		return true
	}
	if a.ContentType != "" {
		return false
	}
	// Older messages and some clients omit ContentType — fall back to
	// the filename extension.
	lower := strings.ToLower(a.Filename)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func (c *Content) newInteractionBox(m *discord.Message) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("md-blockquote")
	box.AddCSSClass("message-header-blockquote")
	box.AddCSSClass("message-interaction-box")

	state := gtkcord.FromContext(c.ctx)

	if !showBlockedMessages.Value() && state.UserIsBlocked(m.Interaction.User.ID) {
		header := gtk.NewLabel("Blocked user.")
		header.AddCSSClass("message-reply-header")
		box.Append(header)

		blockedCSS(box)
		return box
	}

	chip := newAuthorChip(c.ctx, m.GuildID, &discord.GuildUser{
		User:   m.Interaction.User,
		Member: m.Interaction.Member,
	})
	chip.SetHAlign(gtk.AlignStart)
	chip.Unpad()
	box.Append(chip)

	nameLabel := gtk.NewLabel(m.Interaction.Name)
	nameLabel.AddCSSClass("message-interaction-name")
	if m.Interaction.Type == discord.CommandInteractionType {
		nameLabel.SetText("/" + m.Interaction.Name)
		nameLabel.AddCSSClass("message-interaction-command")
	}
	nameLabel.SetTooltipText(m.Interaction.Name)
	nameLabel.SetEllipsize(pango.EllipsizeEnd)
	nameLabel.SetXAlign(0)
	box.Append(nameLabel)

	if state.UserIsBlocked(m.Interaction.User.ID) {
		blockedCSS(box)
	}

	return box
}

func (c *Content) append(w gtk.Widgetter) {
	// Widgets inside a forwarded body belong to the box holding it, which is
	// itself tracked in c.child, so clear() still takes the whole thing down.
	if c.appendTo != nil {
		c.appendTo.Append(w)
		return
	}

	c.Box.Append(w)
	c.child = append(c.child, w)
}

func (c *Content) SetCustomChild(child ...gtk.Widgetter) {
	c.clear()
	for _, w := range child {
		c.append(w)
	}
}

func (c *Content) clear() {
	// Reset the redirect first: a panic part-way through rendering a forward
	// leaves it set, and the recovery path calls clear then appends again.
	c.appendTo = nil

	for i, child := range c.child {
		c.Box.Remove(child)
		c.child[i] = nil
	}
	c.child = c.child[:0]
}

var redactedContentCSS = lilcss.Applier("message-redacted-content", `
	.message-redacted-content {
		font-style: italic;
		color: alpha(@theme_fg_color, 0.75);
	}
`)

// Redact clears the content widget.
func (c *Content) Redact() {
	c.clear()

	red := gtk.NewLabel(locale.Get("Redacted."))
	red.SetXAlign(0)
	redactedContentCSS(red)
	c.append(red)
}

// SetReactions sets the reactions inside the message.
func (c *Content) SetReactions(reactions []discord.Reaction) {
	if c.react == nil {
		if len(reactions) == 0 {
			return
		}
		c.react = newContentReactions(c.ctx, c)
		c.append(c.react)
	}
	c.react.SetReactions(reactions)
}

func newAuthorChip(ctx context.Context, guildID discord.GuildID, user *discord.GuildUser) *author.Chip {
	name := user.DisplayOrUsername()
	color := defaultMentionColor

	if user.Member != nil {
		if user.Member.Nick != "" {
			name = user.Member.Nick
		}

		s := gtkcord.FromContext(ctx)
		c, ok := state.MemberColor(user.Member, func(id discord.RoleID) *discord.Role {
			r, _ := s.Cabinet.Role(guildID, id)
			return r
		})
		if ok {
			color = c.String()
		}
	}

	chip := author.NewChip(ctx, imgutil.HTTPProvider)
	chip.SetName(name)
	chip.SetColor(color)
	chip.SetAvatar(gtkcord.InjectAvatarSize(user.AvatarURL()))

	return chip
}
