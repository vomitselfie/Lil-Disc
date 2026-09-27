package mods

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"

	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableGifPicker = prefs.NewBool(true, prefs.PropMeta{
	Name:        "GIF Picker",
	Section:     "Mods",
	Description: "GIF search picker using Tenor, same as Discord's built-in GIF tab.",
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

// Tenor API v2. Discord uses Tenor, so we do too.
// The public key is the same one Discord's client uses.
const (
	tenorBaseURL = "https://tenor.googleapis.com/v2"
	tenorAPIKey  = "AIzaSyAyimkuYQYF_FXVALexPuGQctUWRURdCYQ"
)

type tenorResult struct {
	URL     string // Tenor page URL (sent as message — Discord auto-embeds as GIFV)
	Preview string // small preview URL (for grid thumbnails)
	Title   string
}

type tenorResponse struct {
	Results []struct {
		URL                string `json:"url"` // Tenor page URL
		ContentDescription string `json:"content_description"`
		MediaFormats       struct {
			GIF struct {
				URL string `json:"url"`
			} `json:"gif"`
			TinyGIF struct {
				URL string `json:"url"`
			} `json:"tinygif"`
			NanoGIF struct {
				URL string `json:"url"`
			} `json:"nanogif"`
			MediumGIF struct {
				URL string `json:"url"`
			} `json:"mediumgif"`
		} `json:"media_formats"`
	} `json:"results"`
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// maxJSONResponseSize bounds API JSON payloads (Tenor, Discord) so a hostile
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

func tenorSearch(query string, limit int) ([]tenorResult, error) {
	endpoint := tenorBaseURL + "/search"
	params := url.Values{
		"key":         {tenorAPIKey},
		"q":           {query},
		"limit":       {tenorLimit(limit)},
		"media_filter": {"gif,tinygif"},
		"contentfilter": {"medium"},
	}

	resp, err := httpClient.Get(endpoint + "?" + params.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data tenorResponse
	if err := decodeJSONResponse(resp, &data); err != nil {
		return nil, err
	}

	return parseTenorResults(data), nil
}

func tenorTrending(limit int) ([]tenorResult, error) {
	endpoint := tenorBaseURL + "/featured"
	params := url.Values{
		"key":         {tenorAPIKey},
		"limit":       {tenorLimit(limit)},
		"media_filter": {"gif,tinygif"},
		"contentfilter": {"medium"},
	}

	resp, err := httpClient.Get(endpoint + "?" + params.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data tenorResponse
	if err := decodeJSONResponse(resp, &data); err != nil {
		return nil, err
	}

	return parseTenorResults(data), nil
}

func parseTenorResults(data tenorResponse) []tenorResult {
	results := make([]tenorResult, 0, len(data.Results))
	for _, r := range data.Results {
		// Send the Tenor page URL, not the raw GIF file URL.
		// Discord recognizes Tenor page URLs and auto-embeds them as
		// animated GIFVs, just like the official client.
		sendURL := r.URL
		if sendURL == "" {
			// Fallback to raw GIF if no page URL.
			sendURL = r.MediaFormats.GIF.URL
		}
		preview := r.MediaFormats.TinyGIF.URL
		if preview == "" {
			preview = r.MediaFormats.NanoGIF.URL
		}
		if sendURL == "" {
			continue
		}
		results = append(results, tenorResult{
			URL:     sendURL,
			Preview: preview,
			Title:   r.ContentDescription,
		})
	}
	return results
}

// Tenor API calls use limit as a string param.
func tenorLimit(n int) string { return fmt.Sprintf("%d", n) }

// NewGifPickerPopover creates a GIF picker popover for the composer.
// onPick receives the full GIF URL to be sent as message content.
func NewGifPickerPopover(ctx context.Context, onPick func(string)) *gtk.Popover {
	if !enableGifPicker.Value() {
		return nil
	}

	search := gtk.NewSearchEntry()
	search.AddCSSClass("mod-gif-search")
	search.SetPlaceholderText("Search Tenor...")

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

	populateResults := func(results []tenorResult, gen int) {
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
			if gif.Preview != "" {
				img.SetURL(gif.Preview)
			} else {
				img.SetURL(gif.URL)
			}

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
			var results []tenorResult
			var err error

			if query == "" {
				results, err = tenorTrending(30)
			} else {
				results, err = tenorSearch(query, 30)
			}

			if err != nil {
				slog.Warn("tenor search failed", "err", err, "query", query)
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

			glib.IdleAdd(func() { populateResults(results, gen) })
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
