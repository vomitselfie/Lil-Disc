package history

import (
	"testing"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
)

func msgAt(id uint64, ch uint64, author, content string) discord.Message {
	return discord.Message{
		ID:        discord.MessageID(id),
		ChannelID: discord.ChannelID(ch),
		Author:    discord.User{ID: 1, Username: author},
		Content:   content,
	}
}

func TestParseQuery(t *testing.T) {
	q := ParseQuery("Cat PICS from:@Alice in:#general has:image has:bogus before:2026-02-01 after:2026-01-01 x:y")

	if want := []string{"cat", "pics", "has:bogus", "x:y"}; !equal(q.Terms, want) {
		t.Errorf("Terms = %q, want %q", q.Terms, want)
	}
	if !equal(q.From, []string{"alice"}) || !equal(q.In, []string{"general"}) {
		t.Errorf("From = %q, In = %q", q.From, q.In)
	}
	if q.Has != HasImage {
		t.Errorf("Has = %v, want image", q.Has)
	}
	if q.Before.Format("2006-01-02") != "2026-02-01" || q.After.Format("2006-01-02") != "2026-01-02" {
		t.Errorf("Before = %v, After = %v", q.Before, q.After)
	}
	if !ParseQuery("   ").Empty() {
		t.Error("blank query is not empty")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMatch(t *testing.T) {
	jan15 := time.Date(2026, 1, 15, 12, 0, 0, 0, time.Local)
	m := msgAt(uint64(discord.NewSnowflake(jan15)), 5, "Alice", "look at this Cat https://example.com")
	m.Attachments = []discord.Attachment{{ContentType: "image/png"}}
	e := EntryFromMessage(&m)
	name := func(Entry) string { return "#general-chat" }

	cases := map[string]bool{
		"cat":                   true,
		"cat dog":               false,
		"from:ali":              true,
		"from:bob":              false,
		"from:bob from:alice":   true,
		"in:general":            true,
		"in:random":             false,
		"has:image":             true,
		"has:link":              true,
		"has:video":             false,
		"has:image has:file":    true,
		"after:2026-01-14":      true,
		"after:2026-01-15":      false,
		"before:2026-01-16":     true,
		"before:2026-01-15":     false,
		"on:2026-01-15":         true,
		"during:2026-01-16":     false,
		"cat from:alice in:gen": true,
	}
	for query, want := range cases {
		if got := ParseQuery(query).Match(e, name); got != want {
			t.Errorf("%q: match = %v, want %v", query, got, want)
		}
	}
}

func TestArchivePersistsAndReplaces(t *testing.T) {
	dir := t.TempDir()
	a := Open(dir)
	a.Add(msgAt(10, 1, "a", "first"), msgAt(20, 1, "a", "second"), msgAt(15, 2, "b", "other"))
	a.Add(msgAt(20, 1, "a", "second, edited"))
	a.Remove(1, 10)
	a.Flush()

	b := Open(dir)
	var got []string
	b.Each(func(e Entry) bool {
		got = append(got, e.Content)
		return true
	})
	if len(got) != 2 {
		t.Fatalf("reloaded entries = %q, want 2", got)
	}
	for _, c := range got {
		if c == "first" || c == "second" {
			t.Errorf("stale entry %q survived", c)
		}
	}
}

func TestArchiveCapsEachChannel(t *testing.T) {
	a := Open(t.TempDir())
	msgs := make([]discord.Message, PerChannelLimit+10)
	for i := range msgs {
		msgs[i] = msgAt(uint64(i+1), 1, "a", "x")
	}
	a.Add(msgs...)

	var n int
	var oldest discord.MessageID
	a.Each(func(e Entry) bool {
		if n == 0 || e.ID < oldest {
			oldest = e.ID
		}
		n++
		return true
	})
	if n != PerChannelLimit {
		t.Errorf("kept %d entries, want %d", n, PerChannelLimit)
	}
	if oldest != 11 {
		t.Errorf("oldest kept = %d, want the 10 oldest dropped", oldest)
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	a := Open(dir)
	a.Add(msgAt(1, 1, "a", "x"))
	a.Flush()
	if err := a.Clear(); err != nil {
		t.Fatal(err)
	}
	var n int
	Open(dir).Each(func(Entry) bool { n++; return true })
	if n != 0 {
		t.Errorf("%d entries after Clear", n)
	}
}
