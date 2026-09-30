package messages

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"log/slog"
	"slices"
	"time"

	"github.com/diamondburned/adaptive"
	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/utils/sendpart"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/components/autoscroll"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/pkg/errors"
	"github.com/vomitselfie/Lil-Disc/chatkit/components/author"
	"github.com/vomitselfie/Lil-Disc/internal/components/hoverpopover"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
	"github.com/vomitselfie/Lil-Disc/internal/messages/composer"
	"github.com/vomitselfie/Lil-Disc/internal/mods"
)

type messageRow struct {
	*gtk.ListBoxRow
	message Message
	info    messageInfo
}

type messageInfo struct {
	id        discord.MessageID
	author    messageAuthor
	timestamp discord.Timestamp
}

func newMessageInfo(msg *discord.Message) messageInfo {
	return messageInfo{
		id:        msg.ID,
		author:    newMessageAuthor(&msg.Author),
		timestamp: msg.Timestamp,
	}
}

type messageAuthor struct {
	userID  discord.UserID
	userTag string
}

func newMessageAuthor(author *discord.User) messageAuthor {
	return messageAuthor{
		userID:  author.ID,
		userTag: author.Tag(),
	}
}

type viewState struct {
	row      messageRow
	editing  bool
	replying bool
}

// View is a message view widget.
type View struct {
	*adaptive.LoadablePage
	focused gtk.Widgetter

	ToastOverlay    *adw.ToastOverlay
	LoadMore        *gtk.Button
	Scroll          *autoscroll.Window
	List            *gtk.ListBox
	Composer        *composer.View
	TypingIndicator *TypingIndicator

	rows    map[messageKey]messageRow
	chName  string
	guildID discord.GuildID

	summaries map[discord.Snowflake]messageSummaryWidget

	state viewState

	// loadGen numbers each load of the message list (the initial backlog or
	// a jump). An async load only applies its result if no later load has
	// started, so opening a channel and jumping into it cannot clobber each
	// other.
	loadGen uint64
	// detached is set while the list shows a window of older history that
	// does not reach the present. Live messages are not appended then,
	// because they would sit after a silent gap.
	detached   bool
	presentBar *gtk.Revealer

	ctx  context.Context
	chID discord.ChannelID
}

var viewCSS = lilcss.Applier("message-view", `
	.message-list {
		background: none;
	}
	.message-list > row {
		box-shadow: none;
		background: none;
		background-image: none;
		background-color: transparent;
		padding: 0;
	}
	.message-show-more {
		background: none;
		border-radius: {$radius_md};
		margin: {$space_xs} {$space_lg};
		font-size: {$font_small};
		color: @lil_text_dim;
	}
	.message-present-button {
		margin: {$space_xs} {$space_lg};
		border-radius: {$radius_pill};
		background: @lil_selected;
		color: @lil_accent_text;
		font-size: {$font_small};
		font-weight: 600;
	}
	.message-present-button:hover {
		background: alpha(@lil_accent, 0.3);
	}
	.message-show-more:hover {
		background: @lil_hover;
		color: @lil_text;
	}
	/* Narrow layout: reclaim the avatar gutter for text. */
	.message-view-narrow .message-cozy-avatar {
		padding: 0 {$space_xs};
	}
	.message-view-narrow .message-collapsed-timestamp {
		min-width: 0;
	}
	.message-view-narrow .message-box {
		margin: 0;
	}
	.messages-typing-indicator {
		margin-top: -1em;
	}
	.messages-typing-box {
		background-color: @lil_surface;
		color: @lil_text_dim;
		border-radius: {$radius_md} {$radius_md} 0 0;
		margin-left: {$space_xl};
	}
	.message-list,
	.message-scroll scrollbar.vertical {
		margin-bottom: 1em;
	}
`)

const (
	loadMoreBatch = 50 // load this many more messages on scroll
	initialBatch  = 15 // load this many messages on startup
	idealMaxCount = 50 // ideally keep this many messages in the view
)

func applyViewClamp(clamp *adw.Clamp) {
	clamp.SetMaximumSize(messagesWidth.Value())
	// Set tightening threshold to 90% of the clamp's width.
	clamp.SetTighteningThreshold(int(float64(messagesWidth.Value()) * 0.9))
}

// NewView creates a new View widget associated with the given channel ID. All
// methods call on it will act on that channel.
func NewView(ctx context.Context, chID discord.ChannelID) *View {
	v := &View{
		rows: make(map[messageKey]messageRow),
		chID: chID,
		ctx:  ctx,
	}

	v.LoadMore = gtk.NewButton()
	v.LoadMore.AddCSSClass("message-show-more")
	v.LoadMore.SetLabel(locale.Get("Show More"))
	v.LoadMore.SetHExpand(true)
	v.LoadMore.SetVExpand(true) // hack to push the messages to the bottom
	v.LoadMore.SetVAlign(gtk.AlignStart)
	v.LoadMore.SetSensitive(true)
	v.LoadMore.ConnectClicked(v.loadMore)

	v.List = gtk.NewListBox()
	v.List.AddCSSClass("message-list")
	v.List.SetSelectionMode(gtk.SelectionNone)
	v.List.SetVExpand(false)
	v.List.SetVAlign(gtk.AlignEnd)

	clampBox := gtk.NewBox(gtk.OrientationVertical, 0)
	clampBox.SetHExpand(true)
	clampBox.SetVExpand(true)
	clampBox.Append(v.LoadMore)
	clampBox.Append(v.List)

	// Require 2 clamps, one inside the scroll view and another outside the
	// scroll view. This way, the scrollbars will be on the far right rather
	// than being stuck in the middle.
	clampScroll := adw.NewClamp()
	clampScroll.SetChild(clampBox)
	applyViewClamp(clampScroll)

	v.Scroll = autoscroll.NewWindow()
	v.Scroll.AddCSSClass("message-scroll")
	v.Scroll.SetPropagateNaturalWidth(true)
	v.Scroll.SetPropagateNaturalHeight(true)
	// Horizontal policy is Automatic rather than Never so that the column has
	// somewhere to go when it runs out of room. With Never, the scroller's
	// minimum width is its child's minimum width, so anything that refuses to
	// shrink — a wide code block, a fixed-size attachment — becomes a floor on
	// the whole window, and past that point GTK cannot scroll and will not
	// wrap, so it silently truncates. Automatic lets the content scroll as a
	// last resort instead of being cut off, and drops the minimum width the
	// window has to honour. The scrollbar only appears when something really
	// does overflow.
	v.Scroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	v.Scroll.SetChild(clampScroll)
	v.Scroll.OnBottomed(v.onScrollBottomed)

	scrollAdjustment := v.Scroll.VAdjustment()
	scrollAdjustment.ConnectValueChanged(func() {
		// Replicate adw.ToolbarView's behavior: if the user scrolls up, then
		// show a small drop shadow at the bottom of the view. We're not using
		// the actual widget, because it adds a WindowHandle at the bottom,
		// which breaks double-clicking.
		const undershootClass = "undershoot-bottom"

		value := scrollAdjustment.Value()
		upper := scrollAdjustment.Upper()
		psize := scrollAdjustment.PageSize()
		if value < upper-psize {
			v.Scroll.AddCSSClass(undershootClass)
		} else {
			v.Scroll.RemoveCSSClass(undershootClass)
		}
	})

	vp := v.Scroll.Viewport()
	vp.SetScrollToFocus(true)
	vp.SetVScrollPolicy(gtk.ScrollMinimum)
	vp.SetHScrollPolicy(gtk.ScrollMinimum)
	v.List.SetAdjustment(v.Scroll.VAdjustment())

	v.Composer = composer.NewView(ctx, v, chID)
	gtkutil.ForwardTyping(v.List, v.Composer.Input)

	v.TypingIndicator = NewTypingIndicator(ctx, chID)
	v.TypingIndicator.SetHExpand(true)
	v.TypingIndicator.SetVAlign(gtk.AlignStart)

	composerOverlay := gtk.NewOverlay()
	composerOverlay.AddOverlay(v.TypingIndicator)
	composerOverlay.SetChild(v.Composer)

	composerClamp := adw.NewClamp()
	composerClamp.SetChild(composerOverlay)
	applyViewClamp(composerClamp)

	// Re-clamp when the preference changes. Without this the slider in
	// Preferences reads as broken: applyViewClamp runs once per channel view,
	// so a new width only took effect after switching channels and back.
	//
	// Bound to clampScroll rather than to v: View embeds *adaptive.LoadablePage,
	// which is not constructed until further down, so v is not usable as a
	// widget yet.
	clampScroll.ConnectDestroy(messagesWidth.Subscribe(func() {
		applyViewClamp(clampScroll)
		applyViewClamp(composerClamp)
	}))

	outerBox := gtk.NewBox(gtk.OrientationVertical, 0)
	outerBox.SetHExpand(true)
	outerBox.SetVExpand(true)
	jumpPresent := gtk.NewButtonWithLabel(locale.Get("Viewing older messages · Jump to present"))
	jumpPresent.AddCSSClass("message-present-button")
	jumpPresent.ConnectClicked(v.JumpToPresent)

	v.presentBar = gtk.NewRevealer()
	v.presentBar.SetTransitionType(gtk.RevealerTransitionTypeSlideUp)
	v.presentBar.SetChild(jumpPresent)

	outerBox.Append(v.Scroll)
	outerBox.Append(v.presentBar)
	outerBox.Append(composerClamp)

	// Narrow layout. A 42px avatar with 8px either side costs 58px of every
	// row, and collapsed rows reserve the same again to stay aligned — on a
	// 400px window that is a seventh of the width spent on empty gutter. Below
	// the threshold the gutter is reclaimed and the avatar shrinks, so the
	// text gets the space instead.
	narrowBin := adw.NewBreakpointBin()
	// A BreakpointBin cannot derive its own minimum from its children and
	// warns without one. Kept well under any width the chat column is
	// realistically given, so it does not become a floor of its own — the
	// point of this change is to remove floors, not add one.
	narrowBin.SetSizeRequest(150, 150)
	narrowBin.SetChild(outerBox)

	narrow := adw.NewBreakpoint(adw.NewBreakpointConditionLength(
		adw.BreakpointConditionMaxWidth, 500, adw.LengthUnitSp))
	narrow.ConnectApply(func() { outerBox.AddCSSClass("message-view-narrow") })
	narrow.ConnectUnapply(func() { outerBox.RemoveCSSClass("message-view-narrow") })
	narrowBin.AddBreakpoint(narrow)

	v.ToastOverlay = adw.NewToastOverlay()
	v.ToastOverlay.SetVAlign(gtk.AlignStart)

	toastOuterOverlay := gtk.NewOverlay()
	toastOuterOverlay.SetChild(narrowBin)
	toastOuterOverlay.AddOverlay(v.ToastOverlay)

	// This becomes the outermost widget.
	v.focused = toastOuterOverlay

	v.LoadablePage = adaptive.NewLoadablePage()
	v.LoadablePage.SetTransitionDuration(125)
	v.setPageToMain()

	// If the window gains focus, try to carefully mark the channel as read.
	var windowSignal glib.SignalHandle
	v.ConnectMap(func() {
		window := app.GTKWindowFromContext(ctx)
		windowSignal = window.NotifyProperty("is-active", func() {
			if v.IsActive() {
				v.MarkRead()
			}
		})
	})
	// Immediately disconnect the signal when the widget is unmapped.
	// This should prevent v from being referenced forever.
	v.ConnectUnmap(func() {
		window := app.GTKWindowFromContext(ctx)
		window.HandlerDisconnect(windowSignal)
		windowSignal = 0
	})

	state := gtkcord.FromContext(v.ctx)
	if ch, err := state.Cabinet.Channel(v.chID); err == nil {
		v.chName = ch.Name
		v.guildID = ch.GuildID
	}

	state.BindWidget(v, func(ev gateway.Event) {
		switch ev := ev.(type) {
		case *gateway.MessageCreateEvent:
			if ev.ChannelID != v.chID {
				return
			}

			// Use this to update existing messages' members as well.
			if ev.Member != nil {
				v.updateMember(ev.Member)
			}

			if ev.Nonce != "" {
				// Try and look up the nonce.
				key := messageKeyNonce(ev.Nonce)

				if msg, ok := v.rows[key]; ok {
					wasBottomed := v.Scroll.IsBottomed()

					delete(v.rows, key)

					key = messageKeyID(ev.ID)
					// Known sent message. Update this instead.
					v.rows[key] = msg

					msg.ListBoxRow.SetName(string(key))
					msg.message.Update(ev)

					v.keepBottomed(wasBottomed)
					return
				}
			}

			if v.detached {
				// Showing older history; the jump bar brings the present back.
				return
			}

			if !v.ignoreMessage(&ev.Message) {
				wasBottomed := v.Scroll.IsBottomed()

				msg := v.upsertMessage(ev.ID, newMessageInfo(&ev.Message), 0)
				msg.Update(ev)

				v.keepBottomed(wasBottomed)
			}

		case *gateway.MessageUpdateEvent:
			if ev.ChannelID != v.chID {
				return
			}

			m, err := state.Cabinet.Message(ev.ChannelID, ev.ID)
			if err == nil && !v.ignoreMessage(&ev.Message) {
				msg := v.upsertMessage(ev.ID, newMessageInfo(m), 0)
				msg.Update(&gateway.MessageCreateEvent{
					Message: *m,
					Member:  ev.Member,
				})
			}

		case *gateway.MessageDeleteEvent:
			if ev.ChannelID != v.chID {
				return
			}

			v.deleteMessage(ev.ID)

		case *gateway.MessageReactionAddEvent:
			if ev.ChannelID != v.chID {
				return
			}
			v.updateMessageReactions(ev.MessageID)

		case *gateway.MessageReactionRemoveEvent:
			if ev.ChannelID != v.chID {
				return
			}
			v.updateMessageReactions(ev.MessageID)

		case *gateway.MessageReactionRemoveAllEvent:
			if ev.ChannelID != v.chID {
				return
			}
			v.updateMessageReactions(ev.MessageID)

		case *gateway.MessageReactionRemoveEmojiEvent:
			if ev.ChannelID != v.chID {
				return
			}
			v.updateMessageReactions(ev.MessageID)

		case *gateway.MessageDeleteBulkEvent:
			if ev.ChannelID != v.chID {
				return
			}

			for _, id := range ev.IDs {
				v.deleteMessage(id)
			}

		case *gateway.GuildMemberAddEvent:
			slog.Debug(
				"GuildMemberAddEvent not implemented",
				"guildID", ev.GuildID,
				"userID", ev.User.ID)

		case *gateway.GuildMemberUpdateEvent:
			if ev.GuildID != v.guildID {
				return
			}

			member, _ := state.Cabinet.Member(ev.GuildID, ev.User.ID)
			if member != nil {
				v.updateMember(member)
			}

		case *gateway.GuildMemberRemoveEvent:
			slog.Debug(
				"GuildMemberRemoveEvent not implemented",
				"guildID", ev.GuildID,
				"userID", ev.User.ID)

		case *gateway.GuildMembersChunkEvent:
			// TODO: Discord isn't sending us this event. I'm not sure why.
			// Their client has to work somehow. Maybe they use the right-side
			// member list?
			if ev.GuildID != v.guildID {
				return
			}

			for i := range ev.Members {
				v.updateMember(&ev.Members[i])
			}

		case *gateway.ConversationSummaryUpdateEvent:
			if ev.ChannelID != v.chID {
				return
			}

			v.updateSummaries(ev.Summaries)
		}
	})

	gtkutil.BindActionCallbackMap(v.List, map[string]gtkutil.ActionCallback{
		"messages.scroll-to": {
			ArgType: gtkcord.SnowflakeVariant,
			Func: func(args *glib.Variant) {
				// Loads the history around the message when it is not in
				// the list, so a reply to an old message still lands on it.
				v.JumpTo(discord.MessageID(args.Int64()))
			},
		},
	})

	viewCSS(v)
	return v
}

// HeaderButtons returns the header buttons widget for the message view.
// This widget is kept on the header bar for as long as the message view is
// active.
func (v *View) HeaderButtons() []gtk.Widgetter {
	var buttons []gtk.Widgetter

	if v.guildID.IsValid() {
		summariesButton := hoverpopover.NewPopoverButton(v.initSummariesPopover)
		summariesButton.SetIconName("speaker-notes-symbolic")
		summariesButton.SetTooltipText(locale.Get("Message Summaries"))
		buttons = append(buttons, summariesButton)

		state := gtkcord.FromContext(v.ctx)
		if len(state.SummaryState.Summaries(v.chID)) == 0 {
			summariesButton.SetSensitive(false)
			var unbind func()
			unbind = state.AddHandlerForWidget(summariesButton, func(ev *gateway.ConversationSummaryUpdateEvent) {
				if ev.ChannelID == v.chID && len(ev.Summaries) > 0 {
					summariesButton.SetSensitive(true)
					unbind()
				}
			})
		}

		infoButton := hoverpopover.NewPopoverButton(func(popover *gtk.Popover) bool {
			popover.AddCSSClass("message-channel-info-popover")
			popover.SetPosition(gtk.PosBottom)

			label := gtk.NewLabel("")
			label.AddCSSClass("popover-label")
			popover.SetChild(label)

			state := gtkcord.FromContext(v.ctx)
			ch, _ := state.Offline().Channel(v.chID)
			if ch == nil {
				label.SetText(locale.Get("Channel information unavailable."))
				return true
			}

			markup := fmt.Sprintf(
				`<b>%s</b>`,
				html.EscapeString(ch.Name))

			if ch.NSFW {
				markup += fmt.Sprintf(
					"\n<i><small>%s</small></i>",
					locale.Get("This channel is NSFW."))
			}

			if ch.Topic != "" {
				markup += fmt.Sprintf(
					"\n<small>%s</small>",
					html.EscapeString(ch.Topic))
			} else {
				markup += fmt.Sprintf(
					"\n<i><small>%s</small></i>",
					locale.Get("No topic set."))
			}

			label.SetSizeRequest(100, -1)
			label.SetMaxWidthChars(100)
			label.SetWrap(true)
			label.SetWrapMode(pango.WrapWordChar)
			label.SetJustify(gtk.JustifyLeft)
			label.SetXAlign(0)
			label.SetMarkup(markup)
			return true
		})
		infoButton.SetIconName("dialog-information-symbolic")
		infoButton.SetTooltipText(locale.Get("Channel Info"))
		buttons = append(buttons, infoButton)
	}

	return buttons
}

// GuildID returns the guild ID of the channel that the message view is
// displaying for.
func (v *View) GuildID() discord.GuildID {
	return v.guildID
}

// ChannelID returns the channel ID of the message view.
func (v *View) ChannelID() discord.ChannelID {
	return v.chID
}

// ChannelName returns the name of the channel that the message view is
// displaying for.
func (v *View) ChannelName() string {
	return v.chName
}

// messageSummaries returns the message summaries for the channel that the
// message view is displaying for. If showSummaries is false, then nil is
// returned.
func (v *View) messageSummaries() map[discord.MessageID]gateway.ConversationSummary {
	if !showSummaries.Value() {
		return nil
	}

	state := gtkcord.FromContext(v.ctx)
	summaries := state.SummaryState.Summaries(v.chID)
	if len(summaries) == 0 {
		return nil
	}

	summariesMap := make(map[discord.MessageID]gateway.ConversationSummary, len(summaries))
	for _, summary := range summaries {
		summariesMap[summary.EndID] = summary
	}

	return summariesMap
}

// FetchBacklog fetches the initial backlog of messages for the channel that the
// view belongs to.
func (v *View) FetchBacklog() {
	slog.Debug(
		"loading message view",
		"channel", v.chID)

	gen := v.beginLoad()
	v.LoadablePage.SetLoading()
	v.unload()
	v.setDetached(false)

	state := gtkcord.FromContext(v.ctx)

	gtkutil.Async(v.ctx, func() func() {
		msgs, err := state.Online().Messages(v.chID, 15)
		if err != nil {
			return func() {
				if v.isCurrentLoad(gen) {
					v.LoadablePage.SetError(err)
				}
			}
		}

		return func() {
			if !v.isCurrentLoad(gen) {
				return
			}

			state := gtkcord.FromContext(v.ctx)

			ch, _ := state.Cabinet.Channel(v.chID)
			if ch == nil {
				v.LoadablePage.SetError(fmt.Errorf("channel not found"))
				return
			}

			if len(msgs) == 0 && ch.Type == discord.DirectMessage {
				v.LoadablePage.SetError(errors.New(
					"refusing to load DM: please send a message via the official client first"))
				return
			}

			v.AddBacklog(msgs)
			v.restoreScrollAnchor(gen)
		}
	})
}

// AddBacklog adds the given messages to the message view as a backlog.
func (v *View) AddBacklog(msgs []discord.Message) {
	slices.SortFunc(msgs, func(a, b discord.Message) int {
		return cmp.Compare(a.ID, b.ID)
	})

	v.setPageToMain()
	v.Scroll.ScrollToBottom()
	mods.RecordMessages(msgs...)

	summariesMap := v.messageSummaries()

	for _, msg := range msgs {
		w := v.upsertMessage(msg.ID, newMessageInfo(&msg), 0)
		w.Update(&gateway.MessageCreateEvent{Message: msg})
		if summary, ok := summariesMap[msg.ID]; ok {
			v.appendSummary(summary)
		}
	}
}

func (v *View) loadMore() {
	firstRow, ok := v.firstMessage()
	if !ok {
		return
	}

	firstID := firstRow.info.id

	slog.Debug(
		"loading more messages",
		"channel", v.chID)

	ctx := v.ctx
	state := gtkcord.FromContext(ctx).Online()

	// TODO: combine this with AddBacklog.
	upsertMessages := func(msgs []discord.Message) {
		mods.RecordMessages(msgs...)
		unlock := v.Scroll.LockScroll()
		glib.IdleAdd(unlock)

		infos := make([]messageInfo, len(msgs))
		for i := range msgs {
			infos[i] = newMessageInfo(&msgs[i])
		}

		summariesMap := v.messageSummaries()

		for i, msg := range msgs {
			flags := 0 |
				upsertFlagOverrideCollapse |
				upsertFlagPrepend

			// Manually prepend our own messages. This also requires us to
			// manually check if we should collapse the message.
			if i != len(msgs)-1 {
				curr := infos[i]
				last := infos[i+1]
				if shouldBeCollapsed(curr, last) {
					flags |= upsertFlagCollapsed
				}
			}

			// These messages are prepended, so we insert the "end summary" of
			// the message before it.
			if summary, ok := summariesMap[msg.ID]; ok {
				v.appendSummary(summary)
			}

			w := v.upsertMessage(msg.ID, infos[i], flags)
			w.Update(&gateway.MessageCreateEvent{Message: msg})
		}

		// Style the first prepended message to add a visual indicator for the
		// user.
		first := v.rows[messageKeyID(msgs[0].ID)]
		first.message.AddCSSClass("message-first-prepended")

		// Remove this visual indicator after a short while.
		glib.TimeoutSecondsAdd(10, func() {
			first.message.RemoveCSSClass("message-first-prepended")
		})
	}

	stateMessages, err := state.Cabinet.Messages(v.chID)
	if err == nil && len(stateMessages) > 0 {
		// State messages are ordered last first, so we can traverse them.
		var found bool
		for i, m := range stateMessages {
			if m.ID < firstID {
				slog.Debug(
					"while loading more messages, found earlier message in state",
					"message_id", m.ID,
					"content", m.Content)
				stateMessages = stateMessages[i:]
				found = true
				break
			}
		}

		if found {
			if len(stateMessages) > loadMoreBatch {
				stateMessages = stateMessages[:loadMoreBatch]
			}
			upsertMessages(stateMessages)
			return
		}
	}

	gtkutil.Async(ctx, func() func() {
		messages, err := state.MessagesBefore(v.chID, firstID, loadMoreBatch)
		if err != nil {
			app.Error(ctx, fmt.Errorf("failed to load more messages: %w", err))
			return nil
		}

		return func() {
			if len(messages) > 0 {
				upsertMessages(messages)
			}

			if len(messages) < loadMoreBatch {
				// We've reached the end of the channel's history.
				// Disable the load more button.
				v.LoadMore.SetSensitive(false)
			}
		}
	})
}

func (v *View) setPageToMain() {
	v.LoadablePage.SetChild(v.focused)
}

func (v *View) unload() {
	for k, msg := range v.rows {
		v.List.Remove(msg)
		delete(v.rows, k)
	}
}

func (v *View) ignoreMessage(msg *discord.Message) bool {
	state := gtkcord.FromContext(v.ctx)
	return showBlockedMessages.Value() && state.UserIsBlocked(msg.Author.ID)
}

type upsertFlags int

const (
	upsertFlagCollapsed upsertFlags = 1 << iota
	upsertFlagOverrideCollapse
	upsertFlagPrepend
)

// upsertMessage inserts or updates a new message row.
// TODO: move boolean args to flags.
func (v *View) upsertMessage(id discord.MessageID, info messageInfo, flags upsertFlags) Message {
	if flags&upsertFlagOverrideCollapse == 0 && v.shouldBeCollapsed(info) {
		flags |= upsertFlagCollapsed
	}
	return v.upsertMessageKeyed(messageKeyID(id), info, flags)
}

// upsertMessageKeyed inserts or updates a new message row with the given key.
func (v *View) upsertMessageKeyed(key messageKey, info messageInfo, flags upsertFlags) Message {
	if msg, ok := v.rows[key]; ok {
		return msg.message
	}

	msg := v.createMessageKeyed(key, info, flags)
	v.rows[key] = msg

	if flags&upsertFlagPrepend != 0 {
		v.List.Prepend(msg.ListBoxRow)
	} else {
		v.List.Append(msg.ListBoxRow)
	}

	v.List.SetFocusChild(msg.ListBoxRow)
	return msg.message
}

func (v *View) createMessageKeyed(key messageKey, info messageInfo, flags upsertFlags) messageRow {
	var message Message
	if flags&upsertFlagCollapsed != 0 {
		message = NewCollapsedMessage(v.ctx, v)
	} else {
		message = NewCozyMessage(v.ctx, v)
	}

	row := gtk.NewListBoxRow()
	row.AddCSSClass("message-row")
	row.SetName(string(key))
	row.SetChild(message)

	return messageRow{
		ListBoxRow: row,
		message:    message,
		info:       info,
	}
}

// resetMessage resets the message with the given messageRow.
// Its main point is to re-evaluate the collapsed state of the message.
func (v *View) resetMessage(key messageKey) {
	row, ok := v.rows[key]
	if !ok || row.message == nil {
		return
	}

	var message Message

	shouldBeCollapsed := v.shouldBeCollapsed(row.info)
	if _, isCollapsed := row.message.(*collapsedMessage); shouldBeCollapsed == isCollapsed {
		message = row.message
	} else {
		if shouldBeCollapsed {
			message = NewCollapsedMessage(v.ctx, v)
		} else {
			message = NewCozyMessage(v.ctx, v)
		}
	}

	message.Update(&gateway.MessageCreateEvent{
		Message: *row.message.Message(),
	})

	row.message = message
	row.ListBoxRow.SetChild(message)

	v.rows[key] = row
}

// surroundingMessagesResetter creates a function that resets the messages
// surrounding the given message.
func (v *View) surroundingMessagesResetter(key messageKey) func() {
	msg, ok := v.rows[key]
	if !ok {
		slog.Warn(
			"useless surroundingMessagesResetter call on non-existent message",
			"key", key)
		return func() {}
	}

	// Just be really safe.
	resets := make([]func(), 0, 2)
	if key, ok := v.nextMessageKey(msg); ok {
		resets = append(resets, func() { v.resetMessage(key) })
	}
	if key, ok := v.prevMessageKey(msg); ok {
		resets = append(resets, func() { v.resetMessage(key) })
	}

	return func() {
		for _, reset := range resets {
			reset()
		}
	}
}

func (v *View) deleteMessage(id discord.MessageID) {
	key := messageKeyID(id)
	v.deleteMessageKeyed(key)
}

func (v *View) deleteMessageKeyed(key messageKey) {
	msg, ok := v.rows[key]
	if !ok {
		return
	}

	if redactMessages.Value() && msg.message != nil {
		msg.message.Redact()
		return
	}

	reset := v.surroundingMessagesResetter(key)
	defer reset()

	v.List.Remove(msg)
	delete(v.rows, key)
}

func (v *View) shouldBeCollapsed(info messageInfo) bool {
	var last messageRow
	var lastOK bool

	if curr, ok := v.rows[messageKeyID(info.id)]; ok {
		prev, ok := v.prevMessageKey(curr)
		if ok {
			last, lastOK = v.rows[prev]
		}
	} else {
		slog.Debug(
			"shouldBeCollapsed called on non-existent message, assuming last",
			"id", info.id,
			"author_id", info.author.userID,
			"timestamp", info.timestamp.Time())

		// Assume we're about to append a new message.
		last, lastOK = v.lastMessage()
	}

	if !lastOK || last.message == nil {
		return false
	}

	return shouldBeCollapsed(info, last.info)
}

func shouldBeCollapsed(curr, last messageInfo) bool {
	return true &&
		// same author
		last.author == curr.author &&
		last.author.userID.IsValid() &&
		curr.author.userID.IsValid() &&
		// within the last 10 minutes
		last.timestamp.Time().Add(10*time.Minute).After(curr.timestamp.Time())
}

func (v *View) nextMessageKeyFromID(id discord.MessageID) (messageKey, bool) {
	row, ok := v.rows[messageKeyID(id)]
	if !ok {
		return "", false
	}
	return v.nextMessageKey(row)
}

// nextMessageKey returns the key of the message after the given message.
func (v *View) nextMessageKey(row messageRow) (messageKey, bool) {
	next, _ := row.NextSibling().(*gtk.ListBoxRow)
	if next != nil {
		return messageKeyRow(next), true
	}
	return "", false
}

func (v *View) prevMessageKeyFromID(id discord.MessageID) (messageKey, bool) {
	row, ok := v.rows[messageKeyID(id)]
	if !ok {
		return "", false
	}
	return v.prevMessageKey(row)
}

// prevMessageKey returns the key of the message before the given message.
func (v *View) prevMessageKey(row messageRow) (messageKey, bool) {
	prev, _ := row.PrevSibling().(*gtk.ListBoxRow)
	if prev != nil {
		return messageKeyRow(prev), true
	}
	return "", false
}

func (v *View) lastMessage() (messageRow, bool) {
	row, _ := v.List.LastChild().(*gtk.ListBoxRow)
	if row != nil {
		msg, ok := v.rows[messageKeyRow(row)]
		return msg, ok
	}

	return messageRow{}, false
}

func (v *View) lastUserMessage() Message {
	state := gtkcord.FromContext(v.ctx)
	me, _ := state.Me()
	if me == nil {
		return nil
	}

	var msg Message
	v.eachMessageFromUser(me.ID, func(row messageRow) bool {
		msg = row.message
		return true
	})

	return msg
}

func (v *View) firstMessage() (messageRow, bool) {
	row, _ := v.List.FirstChild().(*gtk.ListBoxRow)
	if row != nil {
		msg, ok := v.rows[messageKeyRow(row)]
		return msg, ok
	}

	return messageRow{}, false
}

// eachMessage iterates over each message in the view, starting from the bottom.
// If the callback returns true, the loop will break.
func (v *View) eachMessage(f func(messageRow) bool) {
	row, _ := v.List.LastChild().(*gtk.ListBoxRow)
	for row != nil {
		key := messageKey(row.Name())

		m, ok := v.rows[key]
		if ok {
			if f(m) {
				break
			}
		}

		// This repeats until index is -1, at which the loop will break.
		row, _ = row.PrevSibling().(*gtk.ListBoxRow)
	}
}

func (v *View) eachMessageFromUser(id discord.UserID, f func(messageRow) bool) {
	v.eachMessage(func(row messageRow) bool {
		if row.info.author.userID == id {
			return f(row)
		}
		return false
	})
}

func (v *View) updateMember(member *discord.Member) {
	v.eachMessageFromUser(member.User.ID, func(msg messageRow) bool {
		m, ok := msg.message.(MessageWithUser)
		if ok {
			m.UpdateMember(member)
		}
		return false // keep looping
	})
}

func (v *View) updateMessageReactions(id discord.MessageID) {
	widget, ok := v.rows[messageKeyID(id)]
	if !ok || widget.message == nil {
		return
	}

	state := gtkcord.FromContext(v.ctx)

	msg, _ := state.Cabinet.Message(v.chID, id)
	if msg == nil {
		return
	}

	content := widget.message.Content()
	content.SetReactions(msg.Reactions)
}

// SendMessage implements composer.Controller.
func (v *View) SendMessage(sendingMsg composer.SendingMessage) {
	state := gtkcord.FromContext(v.ctx)

	if v.detached {
		// A sent message belongs at the end of the channel, not after an
		// old window. Reloading the present unloads synchronously, so the
		// pending row added below survives and the gateway echo finds it.
		v.JumpToPresent()
	}

	me, _ := state.Cabinet.Me()
	if me == nil {
		// The session is gone or not ready (a logout or reconnect racing the
		// send). Files are opened lazily further down, so nothing is held
		// yet; report and drop the send rather than killing the client.
		app.Error(v.ctx, errors.New("cannot send message: not logged in"))
		return
	}

	info := messageInfo{
		author:    newMessageAuthor(me),
		timestamp: discord.Timestamp(time.Now()),
	}

	var flags upsertFlags
	if v.shouldBeCollapsed(info) {
		flags |= upsertFlagCollapsed
	}

	key := messageKeyLocal()
	msg := v.upsertMessageKeyed(key, info, flags)

	m := discord.Message{
		ChannelID: v.chID,
		GuildID:   v.guildID,
		Content:   sendingMsg.Content,
		Timestamp: discord.NowTimestamp(),
		Author:    *me,
	}

	if sendingMsg.ReplyingTo.IsValid() {
		m.Reference = &discord.MessageReference{
			ChannelID: v.chID,
			GuildID:   v.guildID,
			MessageID: sendingMsg.ReplyingTo,
		}
	}

	msg.AddCSSClass("message-sending")
	msg.Update(&gateway.MessageCreateEvent{Message: m})

	uploading := newUploadingLabel(v.ctx, len(sendingMsg.Files))
	uploading.SetVisible(len(sendingMsg.Files) > 0)

	content := msg.Content()
	content.Update(&m, uploading)

	// Use the Background context so things keep getting updated when we switch
	// away.
	gtkutil.Async(context.Background(), func() func() {
		// mod: stickerpicker — sticker messages use raw API since arikawa
		// v3's SendMessageData doesn't have a sticker_ids field.
		if len(sendingMsg.StickerIDs) > 0 {
			err := mods.SendSticker(state.Token(), m.ChannelID, sendingMsg.StickerIDs[0], m.Reference)
			return func() {
				msg.RemoveCSSClass("message-sending")
				if err != nil {
					uploading.AppendError(err)
				}
			}
		}

		sendData := api.SendMessageData{
			Content:   m.Content,
			Reference: m.Reference,
			Nonce:     key.Nonce(),
			AllowedMentions: &api.AllowedMentions{
				RepliedUser: &sendingMsg.ReplyMention,
				Parse: []api.AllowedMentionType{
					api.AllowUserMention,
					api.AllowRoleMention,
					api.AllowEveryoneMention,
				},
			},
		}

		// Ensure that we open ALL files and defer-close them. Otherwise, we'll
		// leak files.
		for _, file := range sendingMsg.Files {
			// mod: privacy — randomize filename and strip image metadata
			openFn := mods.StripImageMetadata(file.Open, file.Type)
			name := mods.RandomizeFilename(file.Name)

			f, err := openFn()
			if err != nil {
				glib.IdleAdd(func() { uploading.AppendError(err) })
				continue
			}

			// This defer executes once we return (like all defers do).
			defer f.Close()

			sendData.Files = append(sendData.Files, sendpart.File{
				Name:   name,
				Reader: wrappedReader{f, uploading},
			})
		}

		state := state.Online()
		_, err := state.SendMessageComplex(m.ChannelID, sendData)

		return func() {
			msg.RemoveCSSClass("message-sending")

			if err != nil {
				uploading.AppendError(err)
			}

			// We'll let the gateway echo back our own event that's identified
			// using the nonce.
			uploading.SetVisible(uploading.HasErrored())
		}
	})
}

// channelAnchorKey remembers, per channel, the message at the top of the
// view when the user left while scrolled up, so reopening the channel
// returns to where they were reading. Leaving at the bottom clears it, so
// the usual case still opens at the latest messages.
var channelAnchorKey = app.NewStateKey[discord.MessageID]("channel-anchor")

// SaveScrollAnchor records where the user is reading, or forgets it if they
// are at the latest messages. Call it when the view is about to go away.
func (v *View) SaveScrollAnchor() {
	anchors := channelAnchorKey.Acquire(v.ctx)
	key := v.chID.String()

	if v.Scroll.IsBottomed() && !v.detached {
		anchors.Delete(key)
		return
	}

	row := v.List.RowAtY(int(v.Scroll.VAdjustment().Value()))
	if row == nil {
		return
	}
	for k, r := range v.rows {
		if r.ListBoxRow.Index() == row.Index() && k.IsEvent() {
			anchors.Set(key, k.ID())
			return
		}
	}
}

// restoreScrollAnchor scrolls back to the saved reading position, if any,
// once the initial backlog is in.
func (v *View) restoreScrollAnchor(gen uint64) {
	anchors := channelAnchorKey.Acquire(v.ctx)
	key := v.chID.String()
	anchors.Exists(key, func(exists bool) {
		if !exists {
			return
		}
		anchors.Get(key, func(id discord.MessageID) {
			if v.isCurrentLoad(gen) && id.IsValid() {
				v.jumpTo(id, false)
			}
		})
	})
}

// beginLoad starts a new load of the message list and returns its number.
func (v *View) beginLoad() uint64 {
	v.loadGen++
	return v.loadGen
}

// isCurrentLoad reports whether no load has started since gen.
func (v *View) isCurrentLoad(gen uint64) bool {
	return gen == v.loadGen
}

func (v *View) setDetached(detached bool) {
	v.detached = detached
	v.presentBar.SetRevealChild(detached)
}

// JumpToPresent reloads the latest messages, leaving older history.
func (v *View) JumpToPresent() {
	v.FetchBacklog()
}

// jumpAroundLimit is how many messages are loaded around a jump target.
const jumpAroundLimit = 50

// JumpTo shows the message with the given ID, loading the history around it
// if it is not already in the list, then focuses and briefly highlights it.
func (v *View) JumpTo(id discord.MessageID) {
	v.jumpTo(id, true)
}

// jumpTo is JumpTo with the highlight optional: a restored reading position
// should not flash.
func (v *View) jumpTo(id discord.MessageID, highlight bool) {
	if row, ok := v.rows[messageKeyID(id)]; ok {
		v.focusRow(row, highlight)
		// Deferred: right after a fresh channel switch the window may not
		// yet register as active/mapped for a frame or two (e.g. a
		// notification click racing the window being raised), and
		// IsActive() would read stale.
		glib.IdleAdd(func() { v.markReadIfLatest(id) })
		return
	}

	gen := v.beginLoad()
	v.LoadablePage.SetLoading()

	state := gtkcord.FromContext(v.ctx).Online()
	chID := v.chID

	gtkutil.Async(v.ctx, func() func() {
		msgs, err := state.MessagesAround(chID, id, jumpAroundLimit)
		if err != nil {
			return func() {
				if v.isCurrentLoad(gen) {
					v.LoadablePage.SetError(fmt.Errorf("cannot load message: %w", err))
				}
			}
		}

		ch, _ := state.Cabinet.Channel(chID)

		return func() {
			if !v.isCurrentLoad(gen) {
				return
			}

			slices.SortFunc(msgs, func(a, b discord.Message) int {
				return cmp.Compare(a.ID, b.ID)
			})
			mods.RecordMessages(msgs...)

			v.unload()
			v.setPageToMain()

			// The window reaches the present only if it holds the channel's
			// newest message; otherwise live messages would land after a gap.
			reachesPresent := len(msgs) == 0 ||
				(ch != nil && msgs[len(msgs)-1].ID >= ch.LastMessageID)
			v.setDetached(!reachesPresent)

			summaries := v.messageSummaries()
			for _, msg := range msgs {
				w := v.upsertMessage(msg.ID, newMessageInfo(&msg), 0)
				w.Update(&gateway.MessageCreateEvent{Message: msg})
				if summary, ok := summaries[msg.ID]; ok {
					v.appendSummary(summary)
				}
			}

			row, ok := v.rows[messageKeyID(id)]
			if !ok {
				app.Error(v.ctx, errors.New("that message no longer exists"))
				return
			}
			// Focus once the rows have been allocated, or the scroll has
			// nowhere to go.
			glib.IdleAdd(func() {
				v.focusRow(row, highlight)
				if reachesPresent && v.IsActive() {
					v.MarkRead()
				}
			})
		}
	})
}

// keepBottomed re-asserts the scroll position at the bottom of the list,
// once GTK has settled the layout for whatever was just added, if the view
// was at the bottom before that addition.
//
// gotkit's autoscroll.Window already tries to stay stuck to the bottom on
// its own: it watches the scroll adjustment's "changed" signal (fired for
// both content-height and viewport-height changes) and re-scrolls while it
// believes it is still bottomed, deferring the actual scroll to the next
// idle tick. That covers most cases, including the composer or a reply bar
// growing at the same time as a message arrives. But it is a chain of
// heuristics reacting to signals rather than a guarantee, and the composer,
// the reply bar and the upload tray can all resize the message list's
// viewport (an animated GtkRevealer among them) independently of any
// message being appended — there is room for a message to land while that
// settling is mid-flight and end up a few pixels short of the true bottom,
// tucked behind the composer until the user scrolls by hand.
//
// This adds a deterministic backstop specifically for the moment a message
// actually lands: deferring with glib.IdleAdd lets GTK's own layout pass
// for this frame finish first, so the corrective call below reads the
// final, settled page size and content height rather than a value that
// might still be moving. Checking wasBottomed against the state from
// before the new content was added — not after — keeps this from yanking
// the view down if the user had actually scrolled up to read history;
// IsBottomed() read right after inserting a row would not reliably tell
// the two cases apart.
func (v *View) keepBottomed(wasBottomed bool) {
	if !wasBottomed {
		return
	}
	glib.IdleAdd(func() {
		if !v.detached {
			v.Scroll.ScrollToBottom()
		}
	})
}

// markReadIfLatest marks the channel read if id is the newest message this
// view knows about. JumpTo's fast path (the message is already rendered)
// only repositions the scroll and grabs focus — unlike opening a channel
// fresh, nothing along that path reaches the bottom of the scroll view, so
// the usual "scrolled to bottom" mark-as-read never fires. That left
// notifications, message links and search results landing on the latest
// message without clearing its unread indicator until the channel was
// reopened some other way.
func (v *View) markReadIfLatest(id discord.MessageID) {
	if !v.IsActive() {
		return
	}
	state := gtkcord.FromContext(v.ctx)
	msgs, _ := state.Cabinet.Messages(v.chID)
	if len(msgs) > 0 && msgs[0].ID == id {
		v.MarkRead()
	}
}

// focusRow scrolls to the row and, when asked, highlights it for a moment,
// so the eye lands on the right message.
func (v *View) focusRow(row messageRow, highlight bool) {
	row.ListBoxRow.GrabFocus()
	if !highlight {
		return
	}
	row.message.AddCSSClass("message-jump-target")
	glib.TimeoutSecondsAdd(2, func() {
		row.message.RemoveCSSClass("message-jump-target")
	})
}

// ScrollToMessage scrolls to the message with the given ID.
func (v *View) ScrollToMessage(id discord.MessageID) {
	if !v.List.ActivateAction("messages.scroll-to", gtkcord.NewMessageIDVariant(id)) {
		slog.Error(
			"cannot emit messages.scroll-to signal",
			"id", id)
	}
}

// AddReaction adds an reaction to the message with the given ID.
func (v *View) AddReaction(id discord.MessageID, emoji discord.APIEmoji) {
	state := gtkcord.FromContext(v.ctx)

	emoji = discord.APIEmoji(gtkcord.SanitizeEmoji(string(emoji)))

	gtkutil.Async(v.ctx, func() func() {
		if err := state.React(v.chID, id, emoji); err != nil {
			err = errors.Wrap(err, "Failed to react:")
			return func() {
				toast := adw.NewToast(locale.Get("Cannot react to message"))
				toast.SetTimeout(0)
				toast.SetButtonLabel(locale.Get("Logs"))
				toast.SetActionName("")
			}
		}
		return nil
	})
}

// AddToast adds a toast to the message view.
func (v *View) AddToast(toast *adw.Toast) {
	v.ToastOverlay.AddToast(toast)
}

// ReplyTo starts replying to the message with the given ID.
func (v *View) ReplyTo(id discord.MessageID) {
	v.stopEditingOrReplying()

	row, ok := v.rows[messageKeyID(id)]
	if !ok || row.message == nil || row.message.Message() == nil {
		return
	}

	v.state.row = row
	v.state.replying = true

	row.message.AddCSSClass("message-replying")
	v.Composer.StartReplyingTo(row.message.Message())
}

// Edit starts editing the message with the given ID.
func (v *View) Edit(id discord.MessageID) {
	v.stopEditingOrReplying()

	row, ok := v.rows[messageKeyID(id)]
	if !ok || row.message == nil || row.message.Message() == nil {
		return
	}

	v.state.row = row
	v.state.editing = true

	row.message.AddCSSClass("message-editing")
	v.Composer.StartEditing(row.message.Message())
}

// StopEditing implements composer.Controller.
func (v *View) StopEditing() {
	v.stopEditingOrReplying()
}

// StopReplying implements composer.Controller.
func (v *View) StopReplying() {
	v.stopEditingOrReplying()
}

func (v *View) stopEditingOrReplying() {
	if v.state == (viewState{}) {
		return
	}

	if v.state.editing {
		v.Composer.StopEditing()
		v.state.row.message.RemoveCSSClass("message-editing")
	}

	if v.state.replying {
		v.Composer.StopReplying()
		v.state.row.message.RemoveCSSClass("message-replying")
	}

	v.state = viewState{}
}

// EditLastMessage implements composer.Controller.
func (v *View) EditLastMessage() bool {
	msg := v.lastUserMessage()
	if msg == nil || msg.Message() == nil {
		return false
	}

	v.Edit(msg.Message().ID)
	return true
}

// Delete deletes the message with the given ID. It may prompt the user to
// confirm the deletion.
func (v *View) Delete(id discord.MessageID) {
	if !askBeforeDelete.Value() {
		v.delete(id)
		return
	}

	user := "?" // juuust in case

	row, ok := v.rows[messageKeyID(id)]
	if ok {
		message := row.message.Message()
		state := gtkcord.FromContext(v.ctx)
		user = state.AuthorMarkup(&gateway.MessageCreateEvent{Message: *message},
			author.WithMinimal())
		user = "<b>" + user + "</b>"
	}

	window := app.GTKWindowFromContext(v.ctx)
	dialog := adw.NewMessageDialog(window,
		locale.Get("Delete Message"),
		locale.Sprintf("Are you sure you want to delete %s's message?", user))
	dialog.SetBodyUseMarkup(true)
	dialog.AddResponse("cancel", locale.Get("_Cancel"))
	dialog.AddResponse("delete", locale.Get("_Delete"))
	dialog.SetResponseAppearance("delete", adw.ResponseDestructive)
	dialog.SetDefaultResponse("cancel")
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		switch response {
		case "delete":
			v.delete(id)
		}
	})
	dialog.Present()
}

func (v *View) delete(id discord.MessageID) {
	if msg, ok := v.rows[messageKeyID(id)]; ok {
		// Visual indicator.
		msg.SetSensitive(false)
	}

	state := gtkcord.FromContext(v.ctx)
	go func() {
		// This is a fairly important operation, so ensure it goes through even
		// if the user switches away.
		state = state.WithContext(context.Background())

		if err := state.DeleteMessage(v.chID, id, ""); err != nil {
			app.Error(v.ctx, errors.Wrap(err, "cannot delete message"))
		}
	}()
}

func (v *View) onScrollBottomed() {
	if v.IsActive() {
		v.MarkRead()
	}

	// Try to clean up the top messages.
	// Fast path: check our cache first.
	if len(v.rows) > idealMaxCount {
		var count int

		row, _ := v.List.LastChild().(*gtk.ListBoxRow)
		for row != nil {
			next, _ := row.PrevSibling().(*gtk.ListBoxRow)

			if count < idealMaxCount {
				count++
			} else {
				// Start purging messages.
				v.List.Remove(row)
				delete(v.rows, messageKeyRow(row))
			}

			row = next
		}
	}
}

// MarkRead marks the view's latest messages as read.
func (v *View) MarkRead() {
	state := gtkcord.FromContext(v.ctx)
	// Grab the last message from the state cache, since we sometimes don't even
	// render blocked messages.
	msgs, _ := state.Cabinet.Messages(v.ChannelID())
	if len(msgs) == 0 {
		return
	}

	state.ReadState.MarkRead(v.ChannelID(), msgs[0].ID)

	readState := state.ReadState.ReadState(v.ChannelID())
	if readState != nil {
		slog.Debug(
			"marked messages as read",
			"channel", v.ChannelID(),
			"last_message", msgs[0].ID,
		)
	}
}

// IsActive returns true if View is active and visible. This implies that the
// window is focused.
func (v *View) IsActive() bool {
	win := app.GTKWindowFromContext(v.ctx)
	return win.IsActive() && v.Mapped()
}
