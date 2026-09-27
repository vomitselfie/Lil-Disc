package sidebutton

import (
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/dijama/lildisc/internal/lilcss"
)

// MentionsIndicator is a small indicator that shows the mention count.
type MentionsIndicator struct {
	*gtk.Revealer
	Label *gtk.Label

	count  int
	reveal bool
}

var mentionCSS = lilcss.Applier("sidebar-mention", `
	.sidebar-mention {
		background: none;
	}
	.sidebar-mention.sidebar-mention-active,
	.sidebar-mention.sidebar-mention-active label {
		border-radius: {$radius_pill};
		background-color: @lil_rail;
	}
	.sidebar-mention.sidebar-mention-active label {
		color: white;
		background-color: @mentioned;
		min-width:  16px;
		min-height: 16px;
		padding: 0 {$space_xs};
		margin: 3px;
		font-size: {$font_micro};
		font-weight: 700;
		font-feature-settings: "tnum";
	}
`)

// NewMentionsIndicator creates a new mention indicator.
func NewMentionsIndicator() *MentionsIndicator {
	m := &MentionsIndicator{
		Revealer: gtk.NewRevealer(),
		Label:    gtk.NewLabel(""),
		reveal:   true,
	}

	m.SetChild(m.Label)
	m.SetHAlign(gtk.AlignEnd)
	m.SetVAlign(gtk.AlignEnd)
	m.SetTransitionType(gtk.RevealerTransitionTypeCrossfade)
	m.SetTransitionDuration(100)

	m.update()
	mentionCSS(m)
	return m
}

// SetCount sets the mention count.
func (m *MentionsIndicator) SetCount(count int) {
	if count == m.count {
		return
	}

	m.count = count
	m.update()
}

// Count returns the mention count.
func (m *MentionsIndicator) Count() int {
	return m.count
}

// SetRevealChild sets whether the indicator should be revealed.
// This lets the user hide the indicator even if there are mentions.
func (m *MentionsIndicator) SetRevealChild(reveal bool) {
	m.reveal = reveal
	m.update()
}

func (m *MentionsIndicator) update() {
	if m.count == 0 {
		m.RemoveCSSClass("sidebar-mention-active")
		m.Revealer.SetRevealChild(false)
		return
	}

	m.AddCSSClass("sidebar-mention-active")
	m.Label.SetText(strconv.Itoa(m.count))
	m.Revealer.SetRevealChild(m.reveal && m.count > 0)
}
