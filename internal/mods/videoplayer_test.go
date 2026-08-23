package mods

import "testing"

func TestHostMatches(t *testing.T) {
	domains := []string{"youtube.com", "youtu.be", "tiktok.com"}

	tests := []struct {
		url  string
		want bool
	}{
		// Exact and subdomain matches.
		{"https://youtube.com/watch?v=abc", true},
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://m.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://vm.tiktok.com/ZMabc/", true},
		// Scheme-less input still resolves to a host.
		{"youtube.com/watch?v=abc", true},
		{"www.tiktok.com/@user/video/1", true},
		// Case-insensitive.
		{"https://YouTube.com/watch?v=abc", true},

		// Look-alike domains must not match.
		{"https://notyoutube.com/watch?v=abc", false},
		{"https://youtube.com.evil.test/watch", false},
		{"https://tiktok.com.phish.example/x", false},
		// A different host merely mentioning the domain in its path or
		// query must not match.
		{"https://example.com/youtube.com/watch", false},
		{"https://example.com/redirect?to=youtube.com", false},
		// Unrelated hosts.
		{"https://cdn.discordapp.com/attachments/1/2/clip.mp4", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := hostMatches(tt.url, domains); got != tt.want {
			t.Errorf("hostMatches(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
