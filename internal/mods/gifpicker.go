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
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotkit/app/prefs"

	"github.com/vomitselfie/Lil-Disc/internal/asyncop"
	"github.com/vomitselfie/Lil-Disc/internal/components/pickergrid"
	"github.com/vomitselfie/Lil-Disc/internal/discordident"
	"github.com/vomitselfie/Lil-Disc/internal/gtkcord"
	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

var enableGifPicker = prefs.NewBool(true, prefs.PropMeta{
	Name:        "GIF Picker",
	Section:     "Mods",
	Description: "GIF search picker backed by Discord's own GIF search, like the built-in GIF tab. Applies to channels opened afterwards.",
})

var gifPickerCSS = lilcss.Applier("mod-gif-picker", `
	.mod-gif-picker {
		min-width: 380px;
		min-height: 420px;
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

func gifRequest(ctx context.Context, method, endpoint, token string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
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
func fetchGifs(ctx context.Context, token, path string, query url.Values) ([]gifResult, error) {
	resp, err := gifRequest(ctx, "GET", gifAPIBase+path+"?"+query.Encode(), token, nil)
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

func gifSearch(ctx context.Context, token, query string) ([]gifResult, error) {
	return fetchGifs(ctx, token, "/search", gifQuery(url.Values{"q": {query}}))
}

func gifTrending(ctx context.Context, token string) ([]gifResult, error) {
	return fetchGifs(ctx, token, "/trending-gifs", gifQuery(nil))
}

// gifSelected tells Discord which result was picked for which query, as the
// client does. It is best effort: a failure only loses the report.
func gifSelected(token string, gif gifResult, query string) {
	if gif.ID == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"id": gif.ID, "q": query})
	resp, err := gifRequest(context.Background(), "POST", gifAPIBase+"/select", token, bytes.NewReader(body))
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

	grid := pickergrid.New(ctx, pickergrid.Options{
		Columns:        3,
		CellSize:       gifPreviewSize,
		AnimateOnHover: true,
		Class:          "mod-gif-grid",
	})
	search, popover := newPickerPopover(grid, "Search GIFs...", "mod-gif-picker", 380, 420)
	gifPickerCSS(popover)

	var (
		latest     asyncop.Latest
		lastSearch string
		shownOnce  bool
	)

	doSearch := func(query string) {
		lastSearch = query
		opCtx, gen := latest.Begin(ctx)
		grid.SetMessage("Loading...")

		go func() {
			var results []gifResult
			var err error
			if query == "" {
				results, err = gifTrending(opCtx, token)
			} else {
				results, err = gifSearch(opCtx, token, query)
			}

			glib.IdleAdd(func() {
				if !latest.IsCurrent(gen) {
					return
				}
				if err != nil {
					slog.Warn("gif search failed", "err", err, "query", query, "provider", gifProvider())
					grid.SetMessage("Search failed")
					return
				}
				if len(results) == 0 {
					grid.SetMessage("No results")
					return
				}

				items := make([]pickergrid.Item, len(results))
				for i, gif := range results {
					gif := gif
					items[i] = pickergrid.Item{
						ImageURL: gif.thumbnail(),
						Label:    gif.Title,
						Activate: func() {
							onPick(gif.URL)
							go gifSelected(token, gif, query)
						},
					}
				}
				grid.SetSections([]pickergrid.Section{{Items: items}})
			})
		}()
	}

	// A network search per keystroke; the debounce keeps it to one per
	// pause, and Latest cancels the previous request when a new one starts.
	var debounce glib.SourceHandle
	search.ConnectSearchChanged(func() {
		if debounce != 0 {
			glib.SourceRemove(debounce)
		}
		debounce = glib.TimeoutAdd(300, func() {
			debounce = 0
			doSearch(strings.TrimSpace(search.Text()))
		})
	})

	// Load trending the first time the picker opens.
	popover.ConnectShow(func() {
		if !shownOnce || lastSearch == "" {
			shownOnce = true
			doSearch(lastSearch)
		}
	})
	popover.ConnectHide(latest.Cancel)

	return popover
}
