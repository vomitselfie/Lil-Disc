// Package history keeps a bounded local archive of messages LilDisc has seen,
// so Ctrl+Shift+F can search more than what happens to be loaded.
//
// It is deliberately small: one JSON file per channel under
// ~/.local/state/lildisc/history, capped at PerChannelLimit messages each,
// searched by scanning. A scan over a few hundred thousand short entries
// takes milliseconds, and staying on the standard library keeps SQLite, and
// the dependency and Nix packaging it would bring, out of the build.
//
// The archive stores message text on disk, so it is opt-in; see the mods
// preference that enables it.
package history

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/vomitselfie/Lil-Disc/internal/lilpath"
)

// PerChannelLimit is how many messages are kept per channel. Older ones are
// dropped first.
const PerChannelLimit = 5000

// Flags records what a message carries besides text, for has: filters.
type Flags uint8

const (
	HasImage Flags = 1 << iota
	HasVideo
	HasFile
	HasLink
	HasEmbed
)

// Entry is one archived message, reduced to what search needs.
type Entry struct {
	ID        discord.MessageID `json:"i"`
	ChannelID discord.ChannelID `json:"c"`
	GuildID   discord.GuildID   `json:"g,omitempty"`
	AuthorID  discord.UserID    `json:"a"`
	// Author holds the names the author is known by, lowercased and
	// space-separated, for from: filters.
	Author  string `json:"n"`
	Content string `json:"t"`
	Flags   Flags  `json:"f,omitempty"`
}

// EntryFromMessage reduces a message to an archive entry.
func EntryFromMessage(m *discord.Message) Entry {
	e := Entry{
		ID:        m.ID,
		ChannelID: m.ChannelID,
		GuildID:   m.GuildID,
		AuthorID:  m.Author.ID,
		Author:    strings.ToLower(strings.TrimSpace(m.Author.Username + " " + m.Author.DisplayName)),
		Content:   m.Content,
	}
	for _, a := range m.Attachments {
		switch {
		case strings.HasPrefix(a.ContentType, "image/"):
			e.Flags |= HasImage
		case strings.HasPrefix(a.ContentType, "video/"):
			e.Flags |= HasVideo
		}
		e.Flags |= HasFile
	}
	if len(m.Embeds) > 0 {
		e.Flags |= HasEmbed
	}
	if strings.Contains(m.Content, "https://") || strings.Contains(m.Content, "http://") {
		e.Flags |= HasLink
	}
	return e
}

// Message turns an entry back into a message, with what a search result
// row shows.
func (e Entry) Message() discord.Message {
	name := e.Author
	if i := strings.IndexByte(name, ' '); i >= 0 {
		name = name[:i]
	}
	return discord.Message{
		ID:        e.ID,
		ChannelID: e.ChannelID,
		GuildID:   e.GuildID,
		Author:    discord.User{ID: e.AuthorID, Username: name},
		Content:   e.Content,
		Timestamp: discord.Timestamp(e.ID.Time()),
	}
}

// Archive is the on-disk message archive. Its methods are safe for
// concurrent use.
type Archive struct {
	dir string

	mu       sync.Mutex
	channels map[discord.ChannelID]*channelLog
	scanned  bool // every channel file has been loaded
}

type channelLog struct {
	entries []Entry // sorted by ID, oldest first
	dirty   bool
}

// Open returns the archive stored in dir. Nothing is read until it is used.
func Open(dir string) *Archive {
	return &Archive{dir: dir, channels: make(map[discord.ChannelID]*channelLog)}
}

// Default is the archive in LilDisc's state directory.
func Default() *Archive { return Open(lilpath.StateDir("history")) }

func (a *Archive) path(ch discord.ChannelID) string {
	return filepath.Join(a.dir, ch.String()+".json")
}

// channel returns the channel's log, loading it from disk on first use.
// a.mu must be held.
func (a *Archive) channel(ch discord.ChannelID) *channelLog {
	if log, ok := a.channels[ch]; ok {
		return log
	}
	log := &channelLog{}
	if b, err := os.ReadFile(a.path(ch)); err == nil {
		if err := json.Unmarshal(b, &log.entries); err != nil {
			slog.Warn("history: unreadable channel archive, starting it over",
				"channel", ch, "err", err)
			log.entries = nil
		}
	}
	a.channels[ch] = log
	return log
}

// Add records messages, replacing any already archived with the same ID.
func (a *Archive) Add(msgs ...discord.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range msgs {
		m := &msgs[i]
		if !m.ID.IsValid() || !m.ChannelID.IsValid() {
			continue
		}
		log := a.channel(m.ChannelID)
		e := EntryFromMessage(m)

		at, found := slices.BinarySearchFunc(log.entries, e.ID, func(x Entry, id discord.MessageID) int {
			switch {
			case x.ID < id:
				return -1
			case x.ID > id:
				return 1
			}
			return 0
		})
		if found {
			if log.entries[at] == e {
				continue
			}
			log.entries[at] = e
		} else {
			log.entries = slices.Insert(log.entries, at, e)
			if over := len(log.entries) - PerChannelLimit; over > 0 {
				log.entries = slices.Delete(log.entries, 0, over)
			}
		}
		log.dirty = true
	}
}

// Remove forgets a deleted message.
func (a *Archive) Remove(ch discord.ChannelID, ids ...discord.MessageID) {
	a.mu.Lock()
	defer a.mu.Unlock()

	log := a.channel(ch)
	before := len(log.entries)
	log.entries = slices.DeleteFunc(log.entries, func(e Entry) bool {
		return slices.Contains(ids, e.ID)
	})
	if len(log.entries) != before {
		log.dirty = true
	}
}

// Flush writes channels changed since the last flush.
func (a *Archive) Flush() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for ch, log := range a.channels {
		if !log.dirty {
			continue
		}
		b, err := json.Marshal(log.entries)
		if err != nil {
			continue
		}
		if err := lilpath.WriteFile(a.path(ch), b); err != nil {
			slog.Warn("history: cannot write channel archive", "channel", ch, "err", err)
			continue
		}
		log.dirty = false
	}
}

// Clear deletes the whole archive, in memory and on disk.
func (a *Archive) Clear() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.channels = make(map[discord.ChannelID]*channelLog)
	a.scanned = false
	if err := os.RemoveAll(a.dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Each calls f for every archived entry, loading all channels on first use,
// until f returns false.
func (a *Archive) Each(f func(Entry) bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.scanned {
		files, _ := os.ReadDir(a.dir)
		for _, file := range files {
			name := strings.TrimSuffix(file.Name(), ".json")
			if name == file.Name() {
				continue
			}
			if id, err := discord.ParseSnowflake(name); err == nil {
				a.channel(discord.ChannelID(id))
			}
		}
		a.scanned = true
	}

	for _, log := range a.channels {
		for _, e := range log.entries {
			if !f(e) {
				return
			}
		}
	}
}
