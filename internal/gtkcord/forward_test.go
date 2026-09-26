package gtkcord

import (
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"
)

func snapshotOf(content string) []discord.MessageSnapshot {
	return []discord.MessageSnapshot{{
		Message: discord.MessageSnapshotMessage{Content: content},
	}}
}

func TestForwardedSnapshot(t *testing.T) {
	tests := []struct {
		name string
		msg  discord.Message
		want string // "" means no snapshot expected
	}{
		{
			name: "plain message",
			msg:  discord.Message{Content: "hello"},
		},
		{
			name: "reply carries a reference but no snapshot",
			msg: discord.Message{
				Content: "hello",
				Reference: &discord.MessageReference{
					Type: discord.MessageReferenceTypeDefault,
				},
			},
		},
		{
			name: "forward: content lives in the snapshot",
			msg: discord.Message{
				Content: "",
				Reference: &discord.MessageReference{
					Type: discord.MessageReferenceTypeForward,
				},
				MessageSnapshots: snapshotOf("the forwarded text"),
			},
			want: "the forwarded text",
		},
		{
			// Defensive: a forward that arrives without a reference should
			// still render, since the snapshot is the thing that matters.
			name: "snapshot without a reference",
			msg: discord.Message{
				MessageSnapshots: snapshotOf("orphaned snapshot"),
			},
			want: "orphaned snapshot",
		},
		{
			// A default-type reference alongside a snapshot is not a forward,
			// so the outer message's own content is what should render.
			name: "snapshot under a default reference is not a forward",
			msg: discord.Message{
				Content: "a reply",
				Reference: &discord.MessageReference{
					Type: discord.MessageReferenceTypeDefault,
				},
				MessageSnapshots: snapshotOf("not this"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ForwardedSnapshot(&tt.msg)

			if tt.want == "" {
				if got != nil {
					t.Fatalf("ForwardedSnapshot returned %q, want nil", got.Content)
				}
				return
			}

			if got == nil {
				t.Fatal("ForwardedSnapshot returned nil, want a snapshot")
			}
			if got.Content != tt.want {
				t.Errorf("snapshot content = %q, want %q", got.Content, tt.want)
			}
		})
	}
}

// The first snapshot is the forwarded message. Discord sends the field as an
// array but has only ever populated one entry.
func TestForwardedSnapshotTakesTheFirst(t *testing.T) {
	msg := discord.Message{
		Reference: &discord.MessageReference{
			Type: discord.MessageReferenceTypeForward,
		},
		MessageSnapshots: []discord.MessageSnapshot{
			{Message: discord.MessageSnapshotMessage{Content: "first"}},
			{Message: discord.MessageSnapshotMessage{Content: "second"}},
		},
	}

	got := ForwardedSnapshot(&msg)
	if got == nil || got.Content != "first" {
		t.Fatalf("ForwardedSnapshot did not return the first snapshot: %+v", got)
	}
}

// The returned pointer must address the caller's message rather than a copy,
// so callers reading large embed slices off it do not pay for a clone.
func TestForwardedSnapshotAliasesTheMessage(t *testing.T) {
	msg := discord.Message{
		Reference: &discord.MessageReference{
			Type: discord.MessageReferenceTypeForward,
		},
		MessageSnapshots: snapshotOf("original"),
	}

	got := ForwardedSnapshot(&msg)
	if got != &msg.MessageSnapshots[0].Message {
		t.Error("ForwardedSnapshot returned a copy, want a pointer into the message")
	}
}
