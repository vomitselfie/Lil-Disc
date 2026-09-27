package history

import (
	"strings"
	"time"
	"unicode"
)

// Query is a parsed search, in the filter syntax of Discord's own search:
//
//	cat pictures from:alice in:general has:image after:2026-01-01
//
// Free words must all appear in the text. Each filter kind matches if any
// of its values does; different kinds must all match.
type Query struct {
	Terms  []string
	From   []string // author name substrings
	In     []string // channel name substrings
	Has    Flags
	Before time.Time // exclusive; zero means unbounded
	After  time.Time // exclusive; zero means unbounded
}

var hasNames = map[string]Flags{
	"image":  HasImage,
	"images": HasImage,
	"video":  HasVideo,
	"videos": HasVideo,
	"file":   HasFile,
	"files":  HasFile,
	"link":   HasLink,
	"links":  HasLink,
	"embed":  HasEmbed,
	"embeds": HasEmbed,
}

// ParseQuery parses a search string. Unknown filters and unparseable dates
// are treated as plain words, so a typo still finds something.
func ParseQuery(s string) Query {
	var q Query
	for _, field := range strings.FieldsFunc(s, unicode.IsSpace) {
		key, val, ok := strings.Cut(field, ":")
		key = strings.ToLower(key)
		val = strings.ToLower(strings.TrimSpace(val))
		if !ok || val == "" {
			q.Terms = append(q.Terms, strings.ToLower(field))
			continue
		}
		switch key {
		case "from":
			q.From = append(q.From, strings.TrimPrefix(val, "@"))
		case "in":
			q.In = append(q.In, strings.TrimPrefix(val, "#"))
		case "has":
			if f, ok := hasNames[val]; ok {
				q.Has |= f
			} else {
				q.Terms = append(q.Terms, strings.ToLower(field))
			}
		case "before", "after", "during", "on":
			day, err := time.ParseInLocation("2006-01-02", val, time.Local)
			if err != nil {
				q.Terms = append(q.Terms, strings.ToLower(field))
				continue
			}
			switch key {
			case "before":
				q.Before = day
			case "after":
				q.After = day.AddDate(0, 0, 1)
			default: // during, on: that whole day
				q.After = day
				q.Before = day.AddDate(0, 0, 1)
			}
		default:
			q.Terms = append(q.Terms, strings.ToLower(field))
		}
	}
	return q
}

// Empty reports whether the query has nothing to match on.
func (q Query) Empty() bool {
	return len(q.Terms) == 0 && len(q.From) == 0 && len(q.In) == 0 &&
		q.Has == 0 && q.Before.IsZero() && q.After.IsZero()
}

// Match reports whether e satisfies the query. channelName resolves a
// channel to its name for in: filters.
func (q Query) Match(e Entry, channelName func(Entry) string) bool {
	if q.Has != 0 && e.Flags&q.Has != q.Has {
		return false
	}
	if !q.Before.IsZero() || !q.After.IsZero() {
		t := e.ID.Time()
		if !q.Before.IsZero() && !t.Before(q.Before) {
			return false
		}
		if !q.After.IsZero() && t.Before(q.After) {
			return false
		}
	}
	if len(q.From) > 0 && !containsAny(e.Author, q.From) {
		return false
	}
	if len(q.In) > 0 {
		name := ""
		if channelName != nil {
			name = strings.ToLower(strings.TrimPrefix(channelName(e), "#"))
		}
		if !containsAny(name, q.In) {
			return false
		}
	}
	if len(q.Terms) > 0 {
		text := strings.ToLower(e.Content)
		for _, term := range q.Terms {
			if !strings.Contains(text, term) {
				return false
			}
		}
	}
	return true
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
