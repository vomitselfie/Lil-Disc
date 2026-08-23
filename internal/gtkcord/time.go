package gtkcord

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// Relative timestamp formats, in glib.DateTime.Format syntax.
//
// These mirror gotkit's locale.TimeAgo but pin the clock to 24-hour time.
// gotkit uses %X, the locale's preferred time representation, which on an
// en_US locale expands to "3:45:12 PM" — seconds and a meridiem indicator the
// message header has no room for. %H:%M drops both.
const (
	timeAgoToday     = "Today at %H:%M"
	timeAgoYesterday = "Yesterday at %H:%M"
	timeAgoWeek      = "%A at %H:%M"
	timeAgoDefault   = "%Y-%m-%d %H:%M"
)

// TimeAgo renders a timestamp relative to now: the time alone for today and
// yesterday, the weekday for the past week, and a full date before that.
func TimeAgo(timestamp time.Time) string {
	timestamp = timestamp.Local()
	now := time.Now().Local()

	day := truncateDay(timestamp)
	today := truncateDay(now)

	switch {
	case day.Equal(today):
		return formatTime(timestamp, timeAgoToday)
	case day.Equal(truncateDay(now.AddDate(0, 0, -1))):
		return formatTime(timestamp, timeAgoYesterday)
	case now.Sub(timestamp) < 7*24*time.Hour && timestamp.Before(now):
		return formatTime(timestamp, timeAgoWeek)
	default:
		return formatTime(timestamp, timeAgoDefault)
	}
}

// TimeAgoShort renders just the clock time, for places that already establish
// which day they are talking about.
func TimeAgoShort(timestamp time.Time) string {
	return formatTime(timestamp.Local(), "%H:%M")
}

// formatTime goes through glib rather than Go's time package so that the
// weekday name in timeAgoWeek is localised.
func formatTime(t time.Time, format string) string {
	return glib.NewDateTimeFromGo(t).Format(format)
}

// truncateDay returns the start of t's day in t's own location. It differs
// from time.Truncate, which works in UTC and so lands mid-day for most zones.
func truncateDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
