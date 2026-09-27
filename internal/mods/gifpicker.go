package mods

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"

	"github.com/vomitselfie/Lil-Disc/internal/discordident"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableGifPicker = prefs.NewBool(true, prefs.PropMeta{
	Name:        "GIF Picker",
	Section:     "Mods",
	Description: "GIF search picker backed by Discord's own GIF search, like the built-in GIF tab.",
})

var gifPickerCSS = lilcss.Applier("mod-gif-picker", `
	.mod-gif-picker {
		min-width: 380px;
		min-height: 420px;
	}
	.mod-gif-search {
		margin: {$space_md};
	}
	.mod-gif-grid {
		padding: {$space_xs};
	}
	.mod-gif-item {
		padding: {$space_xs};
		border-radius: {$radius_md};
	}
	.mod-gif-item:hover {
		background: @lil_hover;
	}
	.mod-gif-item .onlineimage {
		border-radius: {$radius_sm};
		background: transparent;
	}
	.mod-gif-section-header {
		font-weight: bold;
		font-size: {$font_micro};
		color: @lil_text_faint;
		padding: {$space_md} {$space_md} {$space_xs} {$space_md};
	}
`)

const gifPreviewSize = 100

// GIFs come from Discord's own endpoints, as they do in the official client.
// Discord proxies whichever provider it currently uses (it moved off Tenor),
// so the picker follows provider changes without a code change, never holds
// a third-party API key, and never makes a request the real client would
// not make. The provider name is still a query parameter, so it can be
// overridden if Discord moves again before this default is updated.
const (
	gifAPIBase         = "https://discord.com/api/v9/gifs"
	gifDefaultProvider = "klipy"
	envGifProvider     = "LILDISC_GIF_PROVIDER"
)

// gifResult is one entry of /gifs/search or /gifs/trending-gifs.
type gifResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`     // provider page URL; sent as the message, Discord embeds it
	Src     string `json:"src"`     // media in the requested media_format
	GIFSrc  string `json:"gif_src"` // animated GIF rendition
	Preview string `json:"preview"` // still frame, small
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

// thumbnail returns the lightest URL that shows the GIF in the grid.
func (g gifResult) thumbnail() string {
	for _, u := range []string{g.Preview, g.GIFSrc, g.Src} {
		if u != "" {
			return u
		}
	}
	return g.URL
}

func gifProvider() string {
	if p := os.Getenv(envGifProvider); p != "" {
		return p
	}
	return gifDefaultProvider
}

// gifQuery builds the query string the client sends with every GIF request.
func gifQuery(extra url.Values) url.Values {
	q := url.Values{
		"media_format": {"mp4"},
		"provider":     {gifProvider()},
		"locale":       {discordident.Get().Locale},
	}
	for k, v := range extra {
		q[k] = v
	}
	return q
}

func gifRequest(method, endpoint, token string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	discordident.Get().Apply(req)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, fmt.Errorf("%s %s: %s", method, endpoint, resp.Status)
	}
	return resp, nil
}

// fetchGifs reads a list of GIFs from a Discord /gifs endpoint.
func fetchGifs(token, path string, query url.Values) ([]gifResult, error) {
	resp, err := gifRequest("GET", gifAPIBase+path+"?"+query.Encode(), token, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var results []gifResult
	if err := decodeJSONResponse(resp, &results); err != nil {
		return nil, err
	}

	// Drop entries with nothing to send; the grid cannot use them.
	kept := results[:0]
	for _, r := range results {
		if r.URL != "" {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

func gifSearch(token, query string) ([]gifResult, error) {
	return fetchGifs(token, "/search", gifQuery(url.Values{"q": {query}}))
}

func gifTrending(token string) ([]gifResult, error) {
	return fetchGifs(token, "/trending-gifs", gifQuery(nil))
}

// gifSelected tells Discord which result was picked for which query, as the
// client does. It is best effort: a failure only loses the report.
func gifSelected(token string, gif gifResult, query string) {
	if gif.ID == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"id": gif.ID, "q": query})
	resp, err := gifRequest("POST", gifAPIBase+"/select", token, bytes.NewReader(body))
	if err != nil {
		slog.Debug("gif select report failed", "err", err)
		return
	}
	resp.Body.Close()
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// maxJSONResponseSize bounds API JSON payloads so a hostile
// or runaway endpoint can't OOM the client. 4 MiB is ~30x larger than any
// legitimate response we've seen.
const maxJSONResponseSize = 4 << 20

func decodeJSONResponse(resp *http.Response, dest interface{}) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponseSize+1))
	if err != nil {
		return err
	}
	if len(body) > maxJSONResponseSize {
		return fmt.Errorf("response exceeds %d byte limit", maxJSONResponseSize)
	}
	return json.Unmarshal(body, dest)
}

// NewGifPickerPopover creates a GIF picker popover for the composer.
// onPick receives the full GIF URL to be sent as message content.
func NewGifPickerPopover(ctx context.Context, onPick func(string)) *gtk.Popover {
	if !enableGifPicker.Value() {
		return nil
	}

	state := gtkcord.FromContext(ctx)
	if state == nil {
		return nil
	}
	token := state.Token()

	search := gtk.NewSearchEntry()
	search.AddCSSClass("mod-gif-search")
	search.SetPlaceholderText("Search GIFs...")

	gifBox := gtk.NewBox(gtk.OrientationVertical, 0)
	gifBox.AddCSSClass("mod-gif-grid")

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(gifBox)
	scroll.SetVExpand(true)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(search)
	content.Append(scroll)
	gifPickerCSS(content)

	popover := gtk.NewPopover()
	popover.AddCSSClass("mod-gif-picker")
	popover.SetChild(content)
	popover.SetSizeRequest(380, 420)

	var (
		searchMu   sync.Mutex
		searchGen  int
		lastSearch string
	)

	populateResults := func(results []gifResult, query string, gen int) {
		searchMu.Lock()
		if gen != searchGen {
			searchMu.Unlock()
			return // stale result
		}
		searchMu.Unlock()

		clearBox(gifBox)

		if len(results) == 0 {
			label := gtk.NewLabel("No results")
			label.AddCSSClass("mod-gif-section-header")
			gifBox.Append(label)
			return
		}

		flow := gtk.NewFlowBox()
		flow.SetSelectionMode(gtk.SelectionNone)
		flow.SetMaxChildrenPerLine(3)
		flow.SetMinChildrenPerLine(2)
		flow.SetHomogeneous(true)

		for _, gif := range results {
			gif := gif
			img := onlineimage.NewPicture(ctx, imgutil.HTTPProvider)
			img.EnableAnimation().OnHover()
			img.SetSizeRequest(gifPreviewSize, gifPreviewSize)
			img.SetContentFit(gtk.ContentFitContain)
			img.SetURL(gif.thumbnail())

			box := gtk.NewBox(gtk.OrientationVertical, 0)
			box.AddCSSClass("mod-gif-item")
			box.Append(img)
			if gif.Title != "" {
				box.SetTooltipText(gif.Title)
			}

			click := gtk.NewGestureClick()
			click.ConnectReleased(func(n int, x, y float64) {
				onPick(gif.URL)
				popover.Popdown()
				go gifSelected(token, gif, query)
			})
			box.AddController(click)
			flow.Append(box)
		}
		gifBox.Append(flow)
	}

	doSearch := func(query string) {
		searchMu.Lock()
		searchGen++
		gen := searchGen
		lastSearch = query
		searchMu.Unlock()

		// Show loading indicator
		clearBox(gifBox)
		label := gtk.NewLabel("Loading...")
		label.AddCSSClass("mod-gif-section-header")
		gifBox.Append(label)

		go func() {
			var results []gifResult
			var err error

			if query == "" {
				results, err = gifTrending(token)
			} else {
				results, err = gifSearch(token, query)
			}

			if err != nil {
				slog.Warn("gif search failed", "err", err, "query", query, "provider", gifProvider())
				glib.IdleAdd(func() {
					searchMu.Lock()
					if gen != searchGen {
						searchMu.Unlock()
						return
					}
					searchMu.Unlock()
					clearBox(gifBox)
					errLabel := gtk.NewLabel("Search failed")
					errLabel.AddCSSClass("mod-gif-section-header")
					gifBox.Append(errLabel)
				})
				return
			}

			glib.IdleAdd(func() { populateResults(results, query, gen) })
		}()
	}

	// Debounce search input
	var debounceHandle glib.SourceHandle
	search.ConnectSearchChanged(func() {
		if debounceHandle > 0 {
			glib.SourceRemove(debounceHandle)
		}
		debounceHandle = glib.TimeoutAdd(300, func() {
			debounceHandle = 0
			doSearch(search.Text())
		})
	})

	// Load trending on first show
	popover.ConnectShow(func() {
		searchMu.Lock()
		last := lastSearch
		searchMu.Unlock()
		if last == "" {
			doSearch("")
		}
	})

	return popover
}
