package gtkcord

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/utils/httputil/httpdriver"
	"github.com/diamondburned/arikawa/v3/utils/ws"
	"github.com/diamondburned/chatkit/components/author"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/locale"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/diamondburned/ningen/v3"
	"github.com/diamondburned/ningen/v3/discordmd"
	"github.com/dijama/lildisc/internal/colorhash"
	"github.com/dijama/lildisc/internal/discordident"
	"github.com/dijama/lildisc/internal/signaling"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// InitClientIdentity installs the identity LilDisc presents to Discord: the
// REST User-Agent and the gateway IDENTIFY properties.
//
// This used to be an init function that announced the client by name and
// reported the gateway device as "Arikawa". That pair is the shape Discord's
// abuse tooling uses to separate automated clients from the official app, and
// it is scored hardest on attachment uploads, where it surfaced as ordinary
// image uploads being treated as spam. See package discordident.
//
// It must run before any state is constructed, and after main has loaded
// ~/.config/lildisc/env, since that file can override the version constants.
// It is therefore called explicitly from main rather than from an init.
func InitClientIdentity() {
	ident := discordident.Get()

	api.UserAgent = ident.UserAgent

	if !ident.Enabled {
		// Honest mode. Keep arikawa's own shape rather than half-spoofing:
		// a partial disguise is a stronger tell than none.
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "PC"
		}
		gateway.DefaultIdentity = gateway.IdentifyProperties{
			gateway.IdentifyOS:      runtime.GOOS,
			gateway.IdentifyDevice:  "Arikawa",
			gateway.IdentifyBrowser: "LilDisc on " + hostname,
		}
		return
	}

	// The socket must report what the HTTP header reports, so both are built
	// from the one payload rather than from two hand-maintained literals.
	identity := gateway.IdentifyProperties{}
	for key, value := range ident.Properties {
		identity[gateway.IdentifyPropertyKey(key)] = value
	}
	gateway.DefaultIdentity = identity
}

// AllowedChannelTypes are the channel types that are shown.
var AllowedChannelTypes = []discord.ChannelType{
	discord.GuildText,
	discord.GuildCategory,
	discord.GuildPublicThread,
	discord.GuildPrivateThread,
	discord.GuildForum,
	discord.GuildAnnouncement,
	discord.GuildAnnouncementThread,
	discord.GuildVoice,
	discord.GuildStageVoice,
}

type ctxKey uint8

const (
	_ ctxKey = iota
	stateKey
)

// State extends the Discord state controller.
type State struct {
	*MainThreadHandler
	*ningen.State
}

// FromContext gets the Discord state controller from the given context.
func FromContext(ctx context.Context) *State {
	state, _ := ctx.Value(stateKey).(*State)
	if state != nil {
		return state.WithContext(ctx)
	}
	return nil
}

// Wrap wraps the given state.
func Wrap(state *state.State) *State {
	c := state.Client.Client

	// Arikawa sets Authorization and User-Agent, and nothing else. Discord's
	// own clients also send X-Super-Properties and X-Discord-Locale on every
	// call, and a user token without them reads as automated. See the
	// discordident package for what goes in them and how to refresh it.
	identHeader := discordident.Get().Header()
	c.OnRequest = append(c.OnRequest, func(r httpdriver.Request) error {
		r.AddHeader(identHeader)
		return nil
	})
	c.OnResponse = append(c.OnResponse, func(dreq httpdriver.Request, dresp httpdriver.Response) error {
		req := (*http.Request)(dreq.(*httpdriver.DefaultRequest))
		if dresp == nil {
			return nil
		}

		resp := (*http.Response)(dresp.(*httpdriver.DefaultResponse))
		if resp.StatusCode >= 400 {
			slog.Warn(
				"Discord API returned HTTP error",
				"method", req.Method,
				"path", req.URL.Path,
				"status", resp.Status)
		}

		return nil
	})

	state.StateLog = func(err error) {
		slog.Error(
			"unexpected Discord state error occured",
			"err", err)
	}

	if os.Getenv("DISSENT_DEBUG_DUMP_ALL_EVENTS_PLEASE") == "1" {
		dir := filepath.Join(os.TempDir(), "gtkcord4-events")
		slog.Warn(
			"ATTENTION: DISSENT_DEBUG_DUMP_ALL_EVENTS_PLEASE is set to 1, dumping all raw events",
			"dir", dir)
		dumpRawEvents(state, dir)
	}

	ningen := ningen.FromState(state)

	// mod: friend list — maintain our own friend cache from live
	// relationship events. The pinned ningen RelationshipState only
	// exposes relationship *types*, not the full User object or
	// nickname, so we duplicate the minimal state we need for the DM
	// sidebar dropdown and author label overrides.
	ningen.AddHandler(func(ev *gateway.RelationshipAddEvent) {
		if ev.Type != discord.FriendRelationship {
			return
		}
		rec := FriendRecord{User: ev.User}
		if ev.Nickname != nil {
			rec.Nickname = *ev.Nickname
		}
		friendCacheInstance.mu.Lock()
		if friendCacheInstance.friends == nil {
			friendCacheInstance.friends = make(map[discord.UserID]FriendRecord)
		}
		friendCacheInstance.friends[ev.UserID] = rec
		friendCacheInstance.mu.Unlock()
		glib.IdleAdd(FriendCacheRefreshed.Signal)
	})
	ningen.AddHandler(func(ev *gateway.RelationshipRemoveEvent) {
		friendCacheInstance.mu.Lock()
		delete(friendCacheInstance.friends, ev.UserID)
		friendCacheInstance.mu.Unlock()
		glib.IdleAdd(FriendCacheRefreshed.Signal)
	})

	return &State{
		MainThreadHandler: NewMainThreadHandler(ningen.Handler),
		State:             ningen,
	}
}

// Token returns the Discord auth token used by this session.
// mod: stickerpicker — needed for raw API calls that arikawa doesn't support.
func (s *State) Token() string {
	return s.Client.Token
}

// FetchMeFromAPI fetches the current user from Discord's REST API directly.
// This bypasses the cabinet cache to get the full user data including Avatar.
// Safe to call from a goroutine.
func (s *State) FetchMeFromAPI() *discord.User {
	req, err := http.NewRequest("GET", "https://discord.com/api/v10/users/@me", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", s.Token())
	discordident.Get().Apply(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("FetchMeFromAPI: request failed", "err", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		slog.Warn("FetchMeFromAPI: bad status", "status", resp.StatusCode)
		return nil
	}

	var me discord.User
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		slog.Warn("FetchMeFromAPI: decode failed", "err", err)
		return nil
	}

	slog.Info("FetchMeFromAPI: got avatar", "id", me.ID, "avatar", me.Avatar, "url", me.AvatarURL())
	return &me
}

// --- mod: friend nicknames + friend list ---
// The pinned ningen version's RelationshipState only exposes
// RelationshipType — it doesn't carry the User object or nickname. We
// fetch the full relationship list from the Discord REST API and keep our
// own cache with richer entries so:
//   - chat author labels can substitute personal nicknames (MemberMarkup,
//     userName)
//   - the DM sidebar "Friends" dropdown (mods.FriendsExpander) can list
//     friends who don't have an active DM, with avatars and names

// FriendRecord is a cached friend: the full Discord User plus the
// personal nickname the local user assigned to them (empty if none).
type FriendRecord struct {
	User     discord.User `json:"user"`
	Nickname string       `json:"nickname,omitempty"`
}

type friendCache struct {
	mu      sync.RWMutex
	friends map[discord.UserID]FriendRecord
}

var friendCacheInstance friendCache

// FriendCacheRefreshed fires on the GTK main thread whenever the friend
// cache is repopulated (from disk on startup, from the REST fetch, or
// from live gateway events later). UI components that render friend data
// — the DM sidebar Friends expander in particular — subscribe to this
// so they can reflect changes without a restart.
var FriendCacheRefreshed signaling.Signaler

func (c *friendCache) nickname(id discord.UserID) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if rec, ok := c.friends[id]; ok {
		return rec.Nickname
	}
	return ""
}

func (c *friendCache) loaded() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.friends != nil
}

func (c *friendCache) each(fn func(FriendRecord) (stop bool)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, rec := range c.friends {
		if fn(rec) {
			return
		}
	}
}

func (c *friendCache) set(friends map[discord.UserID]FriendRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.friends = friends
}

// EachFriend iterates cached friend records. Read-only; the callback
// returns true to stop iteration early. Safe to call from any goroutine.
func (s *State) EachFriend(fn func(FriendRecord) (stop bool)) {
	friendCacheInstance.each(fn)
}

// friendCacheFile returns the path of the on-disk friend cache.
func friendCacheFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "lildisc", "api_cache", "friend_cache.json")
}

// FetchFriendNicknames populates the friend cache from the on-disk cache
// first, then falls back to the Discord REST API. Kept under the old
// name so mods.HookState doesn't need to change. Safe to call from a
// goroutine. Fires FriendCacheRefreshed on the main thread on success.
func (s *State) FetchFriendNicknames() {
	if friendCacheInstance.loaded() {
		return
	}

	// Try the on-disk cache first.
	if path := friendCacheFile(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var cached map[discord.UserID]FriendRecord
			if json.Unmarshal(data, &cached) == nil && len(cached) > 0 {
				friendCacheInstance.set(cached)
				slog.Info("friend cache: loaded from disk", "count", len(cached))
				glib.IdleAdd(FriendCacheRefreshed.Signal)
				return
			}
		}
	}

	// Fetch from REST. /users/@me/relationships returns the full list
	// with each entry carrying a nested User object and a nullable
	// nickname string.
	req, err := http.NewRequest("GET", "https://discord.com/api/v10/users/@me/relationships", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", s.Token())
	discordident.Get().Apply(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("friend cache: REST request failed", "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		slog.Warn("friend cache: REST bad status", "status", resp.StatusCode)
		return
	}

	var rels []struct {
		ID       discord.UserID `json:"id"`
		Type     int            `json:"type"`
		Nickname *string        `json:"nickname"`
		User     discord.User   `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		slog.Warn("friend cache: decode failed", "err", err)
		return
	}

	// Type 1 = friend (see discord.FriendRelationship). We only care
	// about that; blocked / pending / outgoing requests are excluded.
	const typeFriend = 1
	cached := make(map[discord.UserID]FriendRecord)
	for _, r := range rels {
		if r.Type != typeFriend {
			continue
		}
		rec := FriendRecord{User: r.User}
		if r.Nickname != nil {
			rec.Nickname = *r.Nickname
		}
		// Prefer the nested user ID but fall back to the top-level id
		// for robustness if the API ever omits one.
		if !rec.User.ID.IsValid() {
			rec.User.ID = r.ID
		}
		cached[rec.User.ID] = rec
	}

	friendCacheInstance.set(cached)

	if path := friendCacheFile(); path != "" {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if data, err := json.Marshal(cached); err == nil {
			os.WriteFile(path, data, 0o644)
		}
	}

	slog.Info("friend cache: loaded from REST", "count", len(cached))
	glib.IdleAdd(FriendCacheRefreshed.Signal)
}

var rawEventsOnce sync.Once

func dumpRawEvents(state *state.State, dir string) {
	rawEventsOnce.Do(func() {
		ws.EnableRawEvents = true
	})

	os.RemoveAll(dir)

	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		slog.Error(
			"cannot mkdir -p for debug event logging, not logging events",
			"dir", dir,
			"err", err)
		return
	}

	var atom uint64
	state.AddHandler(func(ev *ws.RawEvent) {
		id := atomic.AddUint64(&atom, 1)

		f, err := os.Create(filepath.Join(
			dir,
			fmt.Sprintf("%05d-%d-%s.json", id, ev.OriginalCode, ev.OriginalType),
		))
		if err != nil {
			slog.Error(
				"cannot create file to log one debug event",
				"event_code", ev.OriginalCode,
				"event_type", ev.OriginalType,
				"err", err)
			return
		}
		defer f.Close()

		if _, err := f.Write(ev.Raw); err != nil {
			slog.Error(
				"cannot write file to log one debug event",
				"event_code", ev.OriginalCode,
				"event_type", ev.OriginalType,
				"err", err)
			return
		}
	})
}

// InjectState injects the given state to a new context.
func InjectState(ctx context.Context, state *State) context.Context {
	return context.WithValue(ctx, stateKey, state)
}

// Offline creates a copy of State with a new offline state.
func (s *State) Offline() *State {
	s2 := *s
	s2.State = s.State.Offline()
	return &s2
}

// Online creates a copy of State with a new online state.
func (s *State) Online() *State {
	s2 := *s
	s2.State = s.State.Online()
	return &s2
}

// WithContext creates a copy of State with a new context.
func (s *State) WithContext(ctx context.Context) *State {
	s2 := *s
	s2.State = s.State.WithContext(ctx)
	return &s2
}

// BindHandler is similar to BindWidgetHandler, except the lifetime of the
// handler is bound to the context.
func (s *State) BindHandler(ctx gtkutil.Cancellable, fn func(gateway.Event), filters ...gateway.Event) {
	eventTypes := make([]reflect.Type, len(filters))
	for i, filter := range filters {
		eventTypes[i] = reflect.TypeOf(filter)
	}
	ctx.OnRenew(func(context.Context) func() {
		return s.AddSyncHandler(func(ev gateway.Event) {
			// Optionally filter out events.
			if len(eventTypes) > 0 {
				evType := reflect.TypeOf(ev)

				for _, typ := range eventTypes {
					if typ == evType {
						goto filtered
					}
				}

				return
			}

		filtered:
			glib.IdleAddPriority(glib.PriorityDefault, func() { fn(ev) })
		})
	})
}

// BindWidget is similar to BindHandler, except it doesn't rely on contexts.
func (s *State) BindWidget(w gtk.Widgetter, fn func(gateway.Event), filters ...gateway.Event) {
	eventTypes := make([]reflect.Type, len(filters))
	for i, filter := range filters {
		eventTypes[i] = reflect.TypeOf(filter)
	}

	ref := coreglib.NewWeakRef(w)

	var unbind func()
	bind := func() {
		if unbind != nil {
			return
		}

		w := ref.Get()
		slog.Debug(
			"binding state handler lifetime to widget",
			"widget_type", fmt.Sprintf("%T", w),
			"event_types", eventTypes)

		unbind = s.AddSyncHandler(func(ev gateway.Event) {
			// Optionally filter out events.
			if len(eventTypes) > 0 {
				evType := reflect.TypeOf(ev)

				for _, typ := range eventTypes {
					if typ == evType {
						goto filtered
					}
				}

				return
			}

		filtered:
			glib.IdleAddPriority(glib.PriorityDefault, func() { fn(ev) })
		})
	}

	bind()

	base := gtk.BaseWidget(w)
	base.NotifyProperty("parent", func() {
		if base.Parent() != nil {
			return
		}

		if unbind != nil {
			unbind()
			unbind = nil

			slog.Debug(
				"widget unparented, unbinded handler",
				"func", "BindWidget",
				"widget_type", gtk.BaseWidget(w).Type())
		}
	})
	base.ConnectDestroy(func() {
		if unbind != nil {
			unbind()
			unbind = nil

			slog.Debug(
				"widget destroyed, unbinded handler",
				"func", "BindWidget",
				"widget_type", gtk.BaseWidget(w).Type())
		}
	})
}

// AddHandler adds a handler to the state. The handler is removed when the
// returned function is called.
func (s *State) AddHandler(fns ...any) func() {
	if len(fns) == 1 {
		return s.MainThreadHandler.AddHandler(fns[0])
	}

	unbinds := make([]func(), 0, len(fns))
	for _, fn := range fns {
		unbind := s.MainThreadHandler.AddHandler(fn)
		unbinds = append(unbinds, unbind)
	}

	return func() {
		for _, unbind := range unbinds {
			unbind()
		}
		unbinds = unbinds[:0]
	}
}

// AddHandlerForWidget replaces BindWidget and provides a way to bind a handler
// that only receives events as long as the widget is mapped. As soon as the
// widget is unmapped, the handler is unbound.
func (s *State) AddHandlerForWidget(w gtk.Widgetter, fns ...any) func() {
	unbinds := make([]func(), 0, len(fns))

	unbind := func() {
		for _, unbind := range unbinds {
			unbind()
		}
		unbinds = unbinds[:0]
	}

	bind := func() {
		for _, fn := range fns {
			unbind := s.AddHandler(fn)
			unbinds = append(unbinds, unbind)
		}
	}

	bind()

	base := gtk.BaseWidget(w)
	base.NotifyProperty("parent", func() {
		unbind()
		if base.Parent() != nil {
			bind()
		} else {
			slog.Debug(
				"widget unparented, unbinding handler",
				"func", "AddHandlerForWidget",
				"widget_type", gtk.BaseWidget(w).Type())
		}
	})

	return unbind
}

// AuthorMarkup renders the markup for the message author's name. It makes no
// API calls.
func (s *State) AuthorMarkup(m *gateway.MessageCreateEvent, mods ...author.MarkupMod) string {
	user := &discord.GuildUser{User: m.Author, Member: m.Member}
	return s.MemberMarkup(m.GuildID, user, mods...)
}

// UserMarkup is like AuthorMarkup but for any user optionally inside a guild.
func (s *State) UserMarkup(gID discord.GuildID, u *discord.User, mods ...author.MarkupMod) string {
	user := &discord.GuildUser{User: *u}
	return s.MemberMarkup(gID, user, mods...)
}

// UserIDMarkup gets the User markup from just the channel and user IDs.
func (s *State) UserIDMarkup(chID discord.ChannelID, uID discord.UserID, mods ...author.MarkupMod) string {
	chs, err := s.Cabinet.Channel(chID)
	if err != nil {
		return html.EscapeString(uID.Mention())
	}

	if chs.GuildID.IsValid() {
		member, err := s.Cabinet.Member(chs.GuildID, uID)
		if err != nil {
			return html.EscapeString(uID.Mention())
		}

		return s.MemberMarkup(chs.GuildID, &discord.GuildUser{
			User:   member.User,
			Member: member,
		}, mods...)
	}

	for _, recipient := range chs.DMRecipients {
		if recipient.ID == uID {
			return s.UserMarkup(0, &recipient)
		}
	}

	return html.EscapeString(uID.Mention())
}

var overrideMemberColors = prefs.NewBool(false, prefs.PropMeta{
	Name:        "Override Member Colors",
	Section:     "Discord",
	Description: "Use generated colors instead of role colors for members.",
})

// MemberMarkup is like AuthorMarkup but for any member inside a guild.
func (s *State) MemberMarkup(gID discord.GuildID, u *discord.GuildUser, mods ...author.MarkupMod) string {
	name := u.DisplayOrUsername()

	var suffix string
	var prefixMods []author.MarkupMod

	// mod: friend nicknames — if the user set a personal nickname for this
	// person (via Relationships), use it as the primary display name.
	hasFriendNick := false
	if nick := friendCacheInstance.nickname(u.ID); nick != "" {
		suffix += fmt.Sprintf(
			` <span weight="normal">(%s)</span>`,
			html.EscapeString(name),
		)
		name = nick
		hasFriendNick = true
	}

	if gID.IsValid() {
		if u.Member == nil {
			u.Member, _ = s.Cabinet.Member(gID, u.ID)
		}

		if u.Member == nil {
			s.MemberState.RequestMember(gID, u.ID)
			goto noMember
		}

		// Guild nickname — only override if no friend nickname was already applied.
		if u.Member != nil && u.Member.Nick != "" && !hasFriendNick {
			name = u.Member.Nick
			suffix += fmt.Sprintf(
				` <span weight="normal">(%s)</span>`,
				html.EscapeString(u.Member.User.Tag()),
			)
		}

		if !overrideMemberColors.Value() {
			c, ok := state.MemberColor(u.Member, func(id discord.RoleID) *discord.Role {
				role, _ := s.Cabinet.Role(gID, id)
				return role
			})
			if ok {
				prefixMods = append(prefixMods, author.WithColor(c.String()))
			}
		}
	}

	if overrideMemberColors.Value() {
		prefixMods = append(prefixMods, author.WithColor(hashUserColor(&u.User)))
	}

noMember:
	if u.Bot {
		bot := "bot"
		if u.Discriminator == "0000" {
			bot = "webhook"
		}
		suffix += ` <span color="#6f78db" weight="normal">(` + bot + `)</span>`
	}

	if suffix != "" {
		suffix = strings.TrimSpace(suffix)
		prefixMods = append(prefixMods, author.WithSuffixMarkup(suffix))
	}

	return author.Markup(name, append(prefixMods, mods...)...)
}

func hashUserColor(user *discord.User) string {
	input := user.Tag()
	color := colorhash.DefaultHasher().Hash(input)
	return colorhash.RGBHex(color)
}

// blockquoteConcatFix matches a "> " blockquote marker that directly
// follows non-whitespace. The ningen BasicRenderer emits "> " at the
// start of each blockquote paragraph but skips paragraph-exit
// newlines, so multi-line quoted source collapses into
// "line1> line2". The replacement reinserts the missing separator so
// the preview reads as two lines instead of showing a stray ">"
// mid-sentence.
var blockquoteConcatFix = regexp.MustCompile(`(\S)> `)

// MessagePreview renders the message into a short content string.
func (s *State) MessagePreview(msg *discord.Message) string {
	b := strings.Builder{}
	b.Grow(len(msg.Content))

	src := []byte(msg.Content)
	node := discordmd.ParseWithMessage(src, *s.Cabinet, msg, true)
	discordmd.DefaultRenderer.Render(&b, src, node)

	preview := strings.TrimRight(b.String(), "\n")
	preview = blockquoteConcatFix.ReplaceAllString(preview, "$1\n> ")
	if preview != "" {
		return preview
	}

	if len(msg.Attachments) > 0 {
		for _, attachment := range msg.Attachments {
			preview += fmt.Sprintf("%s, ", attachment.Filename)
		}
		preview = strings.TrimSuffix(preview, ", ")
		return preview
	}

	if len(msg.Embeds) > 0 {
		return "[embed]"
	}

	return ""
}

// InjectAvatarSize calls InjectSize with size being 64px.
func InjectAvatarSize(urlstr string) string {
	return InjectSize(urlstr, 64)
}

// InjectSize injects the size query parameter into the URL. Size is
// automatically scaled up to 2x or more.
func InjectSize(urlstr string, size int) string {
	if urlstr == "" {
		return ""
	}

	if scale := gtkutil.ScaleFactor(); scale > 2 {
		size *= scale
	} else {
		size *= 2
	}

	return InjectSizeUnscaled(urlstr, size)
}

// InjectSizeUnscaled is like InjectSize, except the size is not scaled
// according to the scale factor.
func InjectSizeUnscaled(urlstr string, size int) string {
	// Round size up to the nearest power of 2.
	size = roundSize(size)

	u, err := url.Parse(urlstr)
	if err != nil {
		return urlstr
	}

	q := u.Query()
	q.Set("size", strconv.Itoa(size))
	u.RawQuery = q.Encode()

	return u.String()
}

func roundSize(size int) int {
	// Round size up to the nearest power of 2.
	return int(math.Pow(2, math.Ceil(math.Log2(float64(size)))))
}

// EmojiURL returns a sized emoji URL.
func EmojiURL(emojiID string, gif bool) string {
	return InjectSize(discordmd.EmojiURL(emojiID, gif), 64)
}

// WindowTitleFromID returns the window title from the channel with the given
// ID.
func WindowTitleFromID(ctx context.Context, id discord.ChannelID) string {
	state := FromContext(ctx)
	ch, _ := state.Cabinet.Channel(id)
	if ch == nil {
		return ""
	}

	title := ChannelName(ch)
	if ch.GuildID.IsValid() {
		guild, _ := state.Cabinet.Guild(ch.GuildID)
		if guild != nil {
			title += " - " + guild.Name
		}
	}

	return title
}

// ChannelNameFromID returns the channel's name in plain text from the channel
// with the given ID.
func ChannelNameFromID(ctx context.Context, id discord.ChannelID) string {
	state := FromContext(ctx)
	ch, _ := state.Cabinet.Channel(id)
	return ChannelName(ch)
}

// ChannelName returns the channel's name in plain text.
func ChannelName(ch *discord.Channel) string {
	return channelName(ch, true)
}

// ChannelNameWithoutHash returns the channel's name in plain text without the
// hash.
func ChannelNameWithoutHash(ch *discord.Channel) string {
	return channelName(ch, false)
}

func channelName(ch *discord.Channel, hash bool) string {
	if ch == nil {
		return locale.Get("Unknown channel")
	}
	switch ch.Type {
	case discord.DirectMessage:
		if len(ch.DMRecipients) == 0 {
			return RecipientNames(ch)
		}
		return userName(&ch.DMRecipients[0])
	case discord.GroupDM:
		if ch.Name != "" {
			return ch.Name
		}
		return RecipientNames(ch)
	case discord.GuildPublicThread, discord.GuildPrivateThread:
		return ch.Name
	default:
		if hash {
			return "#" + ch.Name
		}
		return ch.Name
	}
}

// RecipientNames formats the string for the list of recipients inside the given
// channel.
func RecipientNames(ch *discord.Channel) string {
	name := func(ix int) string { return userName(&ch.DMRecipients[ix]) }

	// TODO: localize

	switch len(ch.DMRecipients) {
	case 0:
		return "Empty channel"
	case 1:
		return name(0)
	case 2:
		return name(0) + " and " + name(1)
	default:
		var str strings.Builder
		for _, u := range ch.DMRecipients[:len(ch.DMRecipients)-1] {
			str.WriteString(userName(&u))
			str.WriteString(", ")
		}
		str.WriteString(" and ")
		str.WriteString(userName(&ch.DMRecipients[len(ch.DMRecipients)-1]))
		return str.String()
	}
}

// UserNameParts splits a user's label into the name to show prominently and
// the handle that belongs underneath it, if any.
//
// secondary is empty whenever there is nothing worth adding: the user has no
// separate display name, it matches their username, or a personal nickname is
// in use. Callers that want a single string should use userName, which joins
// these in parentheses; callers laying the two out separately (the DM sidebar)
// use the parts directly.
func UserNameParts(u *discord.User) (primary, secondary string) {
	// mod: friend nicknames — if the user set a personal nickname for this
	// person via Relationships, prefer it. This propagates through to:
	//   - sidebar DM list (channelName -> userName for DirectMessage)
	//   - sidebar group DM list (RecipientNames -> userName for each member)
	//   - chat window title bar (ChannelName for the active DM)
	//   - quick switcher results (which call ChannelName)
	// keeping it consistent with how MemberMarkup overrides display names
	// in message author labels.
	if nick := friendCacheInstance.nickname(u.ID); nick != "" {
		return nick, ""
	}
	if u.DisplayName == "" {
		return u.Username, ""
	}
	if strings.EqualFold(u.DisplayName, u.Username) {
		return u.DisplayName, ""
	}
	return u.DisplayName, u.Username
}

func userName(u *discord.User) string {
	primary, secondary := UserNameParts(u)
	if secondary == "" {
		return primary
	}
	return fmt.Sprintf("%s (%s)", primary, secondary)
}

// SnowflakeVariant is the variant type for a [discord.Snowflake].
var SnowflakeVariant = glib.NewVariantType("x")

// NewSnowflakeVariant creates a new Snowflake variant.
func NewSnowflakeVariant(snowflake discord.Snowflake) *glib.Variant {
	return glib.NewVariantInt64(int64(snowflake))
}

// NewChannelIDVariant creates a new ChannelID variant.
func NewChannelIDVariant(id discord.ChannelID) *glib.Variant {
	return glib.NewVariantInt64(int64(id))
}

// NewGuildIDVariant creates a new GuildID variant.
func NewGuildIDVariant(id discord.GuildID) *glib.Variant {
	return glib.NewVariantInt64(int64(id))
}

// NewMessageIDVariant creates a new MessageID variant.
func NewMessageIDVariant(id discord.MessageID) *glib.Variant {
	return glib.NewVariantInt64(int64(id))
}
