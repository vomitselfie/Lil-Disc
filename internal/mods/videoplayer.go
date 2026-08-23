package mods

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
)

var enableMpv = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Use mpv",
	Section:     "Mods",
	Description: "Use mpv for video playback on supported sites. Disable if mpv is not installed.",
})

var enableYtdlp = prefs.NewBool(true, prefs.PropMeta{
	Name:        "Use yt-dlp",
	Section:     "Mods",
	Description: "Use yt-dlp to extract playable URLs from YouTube and other streaming sites for native in-app playback. Disable if yt-dlp is not installed.",
})

// mpvOnlyHosts are domains whose extracted media URLs require headers
// (Referer, User-Agent, cookies) that the native viewer can't easily set.
// For these, hand off to mpv directly — yt-dlp inside mpv applies the
// correct headers when fetching the stream. Subdomains match implicitly.
var mpvOnlyHosts = []string{
	"tiktok.com",
}

// PrefersMpv reports whether the URL belongs to a host where the native
// viewer can't play the extracted stream URL (header-gated CDN), so mpv
// should be used directly without extraction.
func PrefersMpv(rawURL string) bool {
	return hostMatches(rawURL, mpvOnlyHosts)
}

// videoHosts are domains where yt-dlp extraction or mpv+yt-dlp should be
// used. Subdomains match implicitly (www., m., clips., …).
var videoHosts = []string{
	"youtube.com",
	"youtu.be",
	"twitter.com",
	"x.com",
	"twitch.tv",
	"streamable.com",
	"vimeo.com",
	"tiktok.com",
}

// hostMatches reports whether rawURL's hostname is one of the given
// domains or a subdomain of one. Matching on the parsed hostname (rather
// than substring-searching the whole URL) keeps look-alike domains and
// URLs that merely mention a video site in their path or query from
// matching.
func hostMatches(rawURL string, domains []string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Host == "" {
		// Scheme-less input ("youtube.com/watch?v=…") parses as a bare
		// path; reparse so the host lands where we expect it.
		u, err = url.Parse("https://" + rawURL)
		if err != nil {
			return false
		}
	}
	hostname := strings.ToLower(u.Hostname())
	for _, domain := range domains {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return true
		}
	}
	return false
}

// TryPlayVideo attempts to play a URL with mpv. Returns true if handled,
// false if the caller should fall back.
func TryPlayVideo(rawURL string) bool {
	if !enableMpv.Value() {
		return false
	}

	if !IsVideoHost(rawURL) {
		return false
	}

	mpvPath, err := exec.LookPath("mpv")
	if err != nil {
		slog.Debug("mpv not found, falling back")
		return false
	}

	// auto lets mpv pick, which on a machine with an NVIDIA card present means
	// NVDEC — and therefore that card spinning up for a clip. Overridable so a
	// hybrid or eGPU setup can pin decoding to the integrated GPU, e.g.
	// LILDISC_MPV_HWDEC=vaapi. "no" disables hardware decoding entirely.
	hwdec := os.Getenv("LILDISC_MPV_HWDEC")
	if hwdec == "" {
		hwdec = "auto"
	}

	slog.Info("playing video with mpv", "url", rawURL, "hwdec", hwdec)
	args := []string{
		"--force-window=immediate",
		"--ytdl-path=yt-dlp",
		"--title=LilDisc Video",
		"--no-terminal",
		"--hwdec=" + hwdec,
		"--vo=gpu-next",
		// Streaming buffer for network sources
		"--cache=yes",
		"--demuxer-max-bytes=50MiB",
		"--demuxer-max-back-bytes=25MiB",
		// Cap quality to avoid huge downloads
		"--ytdl-raw-options=format=bestvideo[height<=1080]+bestaudio/best[height<=1080]",
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		args = append(args, "--gpu-context=wayland")
	}
	cmd := exec.Command(mpvPath, append(args, rawURL)...)
	if err := cmd.Start(); err != nil {
		slog.Warn("failed to start mpv", "err", err)
		return false
	}

	// Don't wait — let mpv run independently.
	go cmd.Wait()
	return true
}

// ExtractStreamURL uses yt-dlp to get a direct playable URL from a streaming
// site. Blocks until extraction completes — call from a goroutine.
func ExtractStreamURL(rawURL string) (string, error) {
	if !enableYtdlp.Value() {
		return "", fmt.Errorf("yt-dlp disabled")
	}

	ytdlpPath, err := exec.LookPath("yt-dlp")
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	slog.Info("extracting stream URL", "url", rawURL)
	cmd := exec.CommandContext(ctx, ytdlpPath,
		"--get-url",
		"-f", "best[height<=1080]/best",
		rawURL,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("yt-dlp: %w", err)
	}

	// yt-dlp may output multiple lines (video + audio); take the first.
	directURL := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if directURL == "" {
		return "", fmt.Errorf("yt-dlp returned empty URL")
	}

	slog.Info("extracted stream URL", "direct", directURL[:min(len(directURL), 80)])
	return directURL, nil
}

// IsVideoHost reports whether the URL belongs to a streaming host where
// yt-dlp should be used.
func IsVideoHost(rawURL string) bool {
	return hostMatches(rawURL, videoHosts)
}

// VideoLoadingWindow is a small popup with a spinner shown while yt-dlp
// extracts a stream URL.
type VideoLoadingWindow struct {
	*adw.Window
	spinner *gtk.Spinner
	label   *gtk.Label
}

var videoLoadingCSS = cssutil.Applier("video-loading-window", `
	.video-loading-window {
		background: alpha(@theme_bg_color, 0.95);
	}
	.video-loading-box {
		padding: 24px 32px;
	}
	.video-loading-label {
		margin-top: 12px;
		font-size: 1.1em;
		opacity: 0.7;
	}
`)

// NewVideoLoadingWindow creates and shows a loading popup.
func NewVideoLoadingWindow(ctx context.Context) *VideoLoadingWindow {
	w := &VideoLoadingWindow{}

	w.spinner = gtk.NewSpinner()
	w.spinner.SetSizeRequest(48, 48)
	w.spinner.Start()

	w.label = gtk.NewLabel("Loading video...")
	w.label.AddCSSClass("video-loading-label")

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("video-loading-box")
	box.SetVAlign(gtk.AlignCenter)
	box.SetHAlign(gtk.AlignCenter)
	box.Append(w.spinner)
	box.Append(w.label)

	header := adw.NewHeaderBar()
	header.SetShowStartTitleButtons(true)
	header.SetShowEndTitleButtons(true)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(header)
	content.Append(box)
	box.SetVExpand(true)

	w.Window = adw.NewWindow()
	w.SetTitle("LilDisc Video")
	w.SetDefaultSize(360, 200)
	w.SetModal(false)
	w.SetContent(content)

	parent := app.GTKWindowFromContext(ctx)
	if parent != nil {
		w.SetTransientFor(parent)
	}

	videoLoadingCSS(w)
	w.Present()

	return w
}

// SetError updates the label to show an error state and stops the spinner.
func (w *VideoLoadingWindow) SetError(msg string) {
	w.spinner.Stop()
	w.label.SetText(msg)
}
