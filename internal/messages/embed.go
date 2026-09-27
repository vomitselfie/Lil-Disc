package messages

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/chatkit/components/embed"
	"github.com/diamondburned/chatkit/md/mdrender"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotkit/app"
	"github.com/diamondburned/gotkit/components/onlineimage"
	"github.com/diamondburned/gotkit/gtkutil"
	"github.com/diamondburned/gotkit/gtkutil/cssutil"
	"github.com/diamondburned/gotkit/gtkutil/imgutil"
	"github.com/diamondburned/ningen/v3/discordmd"
	"github.com/dustin/go-humanize"
	"github.com/dijama/lildisc/internal/gtkcord"
	"github.com/dijama/lildisc/internal/lilcss"
	"github.com/dijama/lildisc/internal/mods"
)

// TODO: allow disable fetching videos.

var trustedCDNHosts = []string{
	"cdn.discordapp.com",
}

var defaultEmbedOpts = embed.Opts{
	Provider:    imgutil.HTTPProvider,
	IgnoreWidth: true,
}

func resizeURL(directURL, proxyURL string, w, h int) string {
	// mod: embeds — fall back to direct URL if proxy is empty (fixupx, etc.)
	if proxyURL == "" {
		proxyURL = directURL
	}
	if w == 0 || h == 0 {
		return proxyURL
	}

	// Grab the maximum scale factor that we've ever seen. Plugging in another
	// monitor while we've already rendered will not update this, but it's good
	// enough. We just don't want to abuse bandwidth for 1x or 2x people.
	scale := gtkutil.ScaleFactor()
	if scale == 1 {
		// Fetching 2x shouldn't be too bad, though.
		scale = 2
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return proxyURL
	}

	if direct, err := url.Parse(directURL); err == nil {
		if slices.Contains(trustedCDNHosts, direct.Host) {
			// Discord CDN: use direct URL with resize params.
			u = direct
		} else if directURL != "" {
			// mod: embeds — external sources (Twitter, fixupx, etc.): use the
			// direct URL as-is. Discord's proxy is often broken/truncated for
			// third-party embeds, and external CDNs don't support resize params.
			// Native app can fetch from any URL — no CORS restrictions.
			return directURL
		}
	}

	q := u.Query()
	// Do we have a size parameter already? We might if the URL is one crafted
	// by us to fetch an emoji.
	if q.Has("size") {
		// If we even have a size, then we can just assume that the size is
		// the larger dimension.
		if w > h {
			q.Set("size", strconv.Itoa(w*scale))
		} else {
			q.Set("size", strconv.Itoa(h*scale))
		}
	} else {
		q.Set("width", strconv.Itoa(w*scale))
		q.Set("height", strconv.Itoa(h*scale))
	}

	u.RawQuery = q.Encode()

	return u.String()
}

var stickerCSS = lilcss.Applier("message-sticker", `
	.message-sticker {
		border-radius: 0;
	}
	.message-sticker picture.thumbnail-embed-image {
		background-color: transparent;
	}
`)

func newSticker(ctx context.Context, sticker *discord.StickerItem) gtk.Widgetter {
	switch sticker.FormatType {
	case discord.StickerFormatAPNG, discord.StickerFormatPNG:
		// mod: sticker rendering — media proxy WebP loads reliably.
		// Animated stickers display as static for now (CDN APNG has
		// loading issues and media proxy strips animation from WebP).
		url := fmt.Sprintf("https://media.discordapp.net/stickers/%s.webp?size=128",
			sticker.ID)
		origURL := sticker.StickerURLWithType(discord.PNGImage)

		image := embed.New(ctx, gtkcord.StickerSize, gtkcord.StickerSize, defaultEmbedOpts)
		image.SetName(sticker.Name)
		image.SetHAlign(gtk.AlignStart)
		image.SetSizeRequest(gtkcord.StickerSize, gtkcord.StickerSize)
		image.SetFromURL(url)
		image.SetOpenURL(func() { app.OpenURI(ctx, origURL) })
		stickerCSS(image)
		return image
	default:
		msg := gtk.NewLabel(fmt.Sprintf("[Lottie sticker: %s]", sticker.Name))
		msg.SetXAlign(0)
		systemContentCSS(msg)
		fixNatWrap(msg)
		return msg
	}
}

var _ = lilcss.WriteCSS(`
	.message-richframe:not(:first-child) {
		margin-top: 4px;
	}
	.message-embed-spoiler .onlineimage {
		filter: blur(45px);
	}
`)

var messageAttachmentCSS = lilcss.Applier("message-attachment", `
	.message-attachment-filename {
		padding-left: 0.35em;
		padding-right: 0.35em;
	}
	.message-attachment-filesize {
		color: @lil_text_dim;
		font-feature-settings: "tnum";
	}
`)

func newAttachment(ctx context.Context, attachment *discord.Attachment) gtk.Widgetter {
	var mimeType string
	if attachment.ContentType != "" {
		mimeType, _, _ = strings.Cut(attachment.ContentType, "/")
	}

	// mod: audioplayer — inline playback for voice messages and audio files.
	if mods.IsAudioAttachment(attachment) {
		if player := mods.NewAudioPlayer(ctx, attachment); player != nil {
			return player
		}
	}

	switch mimeType {
	case "image", "video":
		// Make this attachment like an image embed.
		opts := defaultEmbedOpts

		switch {
		case attachment.ContentType == "image/gif":
			opts.Type = embed.EmbedTypeGIF
			opts.Autoplay = mods.AutoAnimateGIFs.Value() // mod: embeds
		case mimeType == "image":
			opts.Type = embed.EmbedTypeImage
		case mimeType == "video":
			opts.Type = embed.EmbedTypeVideo
			opts.Autoplay = mods.AutoPlayVideos.Value() // mod: embeds
			// Use FFmpeg for video so we can get the thumbnail.
			opts.Provider = imgutil.FFmpegProvider
		}

		name := fmt.Sprintf(
			"%s (%s)",
			attachment.Filename,
			humanize.Bytes(attachment.Size),
		)

		image := embed.New(ctx, maxEmbedWidth.Value(), maxImageHeight.Value(), opts)
		image.AddCSSClass("message-richframe")
		image.SetHExpand(false)
		image.SetVExpand(false)
		image.SetHAlign(gtk.AlignStart)
		image.SetName(name)

		image.SetOpenURL(func() {
			openViewer(ctx, attachment.URL, opts, int(attachment.Width), int(attachment.Height))
		})

		// mod: embedmenu — right-click to save/copy/open
		mods.AttachEmbedContextMenu(ctx, image, attachment.URL, attachment.Filename)

		if strings.HasPrefix(attachment.Filename, "SPOILER_") {
			image.AddCSSClass("message-embed-spoiler")
		}

		if attachment.Width > 0 && attachment.Height > 0 {
			origW := int(attachment.Width)
			origH := int(attachment.Height)

			// Work around to prevent GTK from rendering the image at its
			// original size, which tanks performance on Cairo renderers.
			w, h := imgutil.MaxSize(
				origW, origH,
				maxEmbedWidth.Value(), maxImageHeight.Value(),
			)

			image.SetSizeRequest(w, h)
			// Height only. Requesting a width here would bypass the
			// IgnoreWidth clamp that Embed.SetSizeRequest applies above and
			// pin a minimum width of up to maxEmbedWidth on every image,
			// which propagates through the message scroller (it uses
			// PropagateNaturalWidth) and becomes a floor on the whole
			// window's minimum width. The thumbnail has CanShrink set, so
			// it scales down to whatever width it is given.
			image.Thumbnail.Picture.SetSizeRequest(-1, h)

			if mimeType == "image" {
				scale := gtkutil.ScaleFactor()
				w *= scale
				h *= scale

				url := resizeURL(attachment.URL, attachment.Proxy, w, h)
				// mod: lazyload — defer fetch until scrolled into view
				mods.LazyLoad(image, func() { image.SetFromURL(url) })
			} else {
				// Video attachments use FFmpegProvider for thumbnail
				// extraction — skip lazy load so the embed's internal
				// click/play handler initializes immediately.
				image.SetFromURL(attachment.Proxy)
			}
		} else {
			if mimeType == "video" {
				image.SetFromURL(attachment.Proxy)
			} else {
				proxy := attachment.Proxy
				// mod: lazyload
				mods.LazyLoad(image, func() { image.SetFromURL(proxy) })
			}
		}

		return image
	default:
		icon := gtk.NewImageFromIconName(mimeIcon(mimeType))
		icon.AddCSSClass("message-attachment-icon")
		icon.SetIconSize(gtk.IconSizeNormal)

		filename := gtk.NewLabel("")
		filename.AddCSSClass("message-attachment-filename")
		filename.SetMarkup(fmt.Sprintf(
			`<a href="%s">%s</a>`,
			html.EscapeString(attachment.URL),
			html.EscapeString(attachment.Filename),
		))
		filename.SetEllipsize(pango.EllipsizeEnd)
		filename.SetXAlign(0)

		filesize := gtk.NewLabel(humanize.Bytes(attachment.Size))
		filesize.AddCSSClass("message-attachment-filesize")
		filesize.SetXAlign(0)

		box := gtk.NewBox(gtk.OrientationHorizontal, 0)
		box.SetTooltipText(attachment.Filename)
		box.Append(icon)
		box.Append(filename)
		box.Append(filesize)
		messageAttachmentCSS(box)

		return box
	}
}

func mimeIcon(mimePrefix string) string {
	switch mimePrefix {
	case "audio":
		return "audio-x-generic-symbolic"
	case "image":
		return "image-x-generic-symbolic"
	case "video":
		return "video-x-generic-symbolic"
	case "application":
		return "application-x-executable-symbolic"
	default:
		return "text-x-generic-symbolic"
	}
}

var normalEmbedCSS = lilcss.Applier("message-normalembed", `
	@define-color lildisc_embed_background @lil_surface_raised;

	/* The accent stripe is drawn as a gradient rather than a border, so the
	   padding has to leave room for it whether or not the embed has a colour.
	   Reserving it unconditionally keeps coloured and uncoloured embeds on
	   the same left edge; they used to differ by 4px. */
	.message-normalembed {
		border: 1px solid @lil_border;
		border-radius: {$radius_lg};
		padding: {$space_lg};
		padding-left: {$space_xl};
		background-color: @lildisc_embed_background;
	}
	.message-normalembed-body > *:not(:last-child) {
		margin-bottom: 0.5em;
	}
	.message-normalembed-body > .thumbnail-embed-bin {
		margin-top: 0.5em;
	}
	.message-embed-author,
	.message-embed-description {
		font-size: 0.9em;
	}
	.message-embed-author-icon,
	.message-embed-footer-icon {
		margin-right: 0.5em;
	}
	.message-embed-footer {
		color: @lil_text_faint;
		font-size: {$font_small};
	}
`)

const embedColorCSSf = `
	.message-normalembed {
		background: linear-gradient(to right,
			%s 4px,
			@lildisc_embed_background 0px,
			@lildisc_embed_background 100%%
		);
	}
`

func newEmbed(ctx context.Context, msg *discord.Message, embed *discord.Embed) gtk.Widgetter {
	return newNormalEmbed(ctx, msg, embed)
}

// mod: embeds — render a group of embeds sharing the same URL as a single card
// with a 2-column image grid (e.g. Twitter multi-image posts).
func newEmbedGroup(ctx context.Context, msg *discord.Message, group []discord.Embed) gtk.Widgetter {
	if len(group) <= 1 {
		return newNormalEmbed(ctx, msg, &group[0])
	}

	// Render the first embed's card WITHOUT its own image — all images go
	// into a side-by-side grid inside the card body.
	cardEmbed := group[0]
	cardEmbed.Image = nil

	// Collect ALL images from every embed in the group into a 2-col grid.
	grid := gtk.NewGrid()
	grid.SetColumnSpacing(4)
	grid.SetRowSpacing(4)
	grid.SetColumnHomogeneous(true)
	grid.SetRowHomogeneous(true)
	grid.SetHExpand(true)

	cols := 2
	imgIdx := 0
	for i := range group {
		e := &group[i]
		if e.Image == nil {
			continue
		}

		img := *e.Image
		imgOpts := defaultEmbedOpts
		imgOpts.CanHide = true // hide broken images in grid instead of showing X
		imgOpts.Type = mods.TypeFromURL(img.URL)
		if imgOpts.Type == embed.EmbedTypeGIF {
			imgOpts.Autoplay = mods.AutoAnimateGIFs.Value()
		}

		halfW := maxEmbedWidth.Value() / 2
		halfH := maxImageHeight.Value() / 2
		image := embed.New(ctx, halfW, halfH, imgOpts)
		image.SetHExpand(true)
		image.SetVExpand(true)
		// mod: embeds — fill grid cell: let the Picture scale to cover
		// available space instead of constraining to intrinsic size.
		image.Thumbnail.SetContentFit(gtk.ContentFitCover)
		imgViewURL := string(img.Proxy)
		if imgViewURL == "" {
			imgViewURL = string(img.URL)
		}
		image.SetOpenURL(func() {
			openViewer(ctx, imgViewURL, imgOpts, int(img.Width), int(img.Height))
		})
		imgURL := resizeURL(img.URL, img.Proxy, int(img.Width), int(img.Height))
		// mod: lazyload
		mods.LazyLoad(image, func() { image.SetFromURL(imgURL) })
		image.SetName(mods.MediaFilename(img.URL, ".jpg"))

		mods.AttachEmbedContextMenu(ctx, image, img.URL, mods.MediaFilename(img.URL, ".jpg"))

		grid.Attach(image, imgIdx%cols, imgIdx/cols, 1, 1)
		imgIdx++
	}

	// Pass the grid into the card body so it renders inside the colored border.
	return newNormalEmbed(ctx, msg, &cardEmbed, grid)
}

// newNormalEmbed renders a rich embed card. Optional extraBody widgets are
// appended to the body (inside the colored border) before the card is wrapped.
func newNormalEmbed(ctx context.Context, msg *discord.Message, msgEmbed *discord.Embed, extraBody ...gtk.Widgetter) gtk.Widgetter {
	bodyBox := gtk.NewBox(gtk.OrientationVertical, 0)
	bodyBox.SetHAlign(gtk.AlignFill)
	bodyBox.SetHExpand(true)
	bodyBox.AddCSSClass("message-normalembed-body")

	// Track whether or not we have an embed body. An embed body should have any
	// kind of text in it. If we don't have a body but do have a thumbnail, then
	// the thumbnail should be big and on its own.
	hasBody := false

	if msgEmbed.Author != nil {
		box := gtk.NewBox(gtk.OrientationHorizontal, 0)
		box.AddCSSClass("message-embed-author")

		if msgEmbed.Author.ProxyIcon != "" {
			img := onlineimage.NewAvatar(ctx, imgutil.HTTPProvider, 18)
			img.AddCSSClass("message-embed-author-icon")
			img.SetFromURL(msgEmbed.Author.ProxyIcon)

			box.Append(img)
		}

		if msgEmbed.Author.Name != "" {
			author := gtk.NewLabel(msgEmbed.Author.Name)
			author.SetUseMarkup(true)
			author.SetSingleLineMode(true)
			author.SetEllipsize(pango.EllipsizeEnd)
			author.SetTooltipText(msgEmbed.Author.Name)
			author.SetXAlign(0)

			if msgEmbed.Author.URL != "" {
				author.SetMarkup(fmt.Sprintf(
					`<a href="%s">%s</a>`,
					html.EscapeString(msgEmbed.Author.URL), html.EscapeString(msgEmbed.Author.Name),
				))
			}

			box.Append(author)
		}

		bodyBox.Append(box)
		hasBody = true
	}

	if msgEmbed.Title != "" {
		title := `<span weight="heavy">` + html.EscapeString(msgEmbed.Title) + `</span>`
		if msgEmbed.URL != "" {
			title = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(msgEmbed.URL), title)
		}

		label := gtk.NewLabel("")
		label.AddCSSClass("message-embed-title")
		label.SetWrap(true)
		label.SetWrapMode(pango.WrapWordChar)
		label.SetXAlign(0)
		label.SetMarkup(title)
		fixNatWrap(label)

		bodyBox.Append(label)
		hasBody = true
	}

	if msgEmbed.Description != "" {
		state := gtkcord.FromContext(ctx)
		// mod: embeds — strip markdown backslash escapes from embed descriptions.
		// Discord's API sends escaped periods (e.g. "example\.com") to prevent
		// auto-linking, but discordmd renders the raw backslash. Remove common
		// escapes before parsing.
		desc := stripMarkdownEscapes(msgEmbed.Description)
		edesc := []byte(desc)
		mnode := discordmd.ParseWithMessage(edesc, *state.Cabinet, msg, false)

		v := mdrender.NewMarkdownViewer(ctx, edesc, mnode)
		v.AddCSSClass("message-embed-description")
		v.SetHExpand(false)

		bodyBox.Append(v)
		hasBody = true
	}

	if len(msgEmbed.Fields) > 0 {
		fields := gtk.NewGrid()
		fields.AddCSSClass("message-embed-fields")
		fields.SetRowSpacing(uint(7))
		fields.SetColumnSpacing(uint(14))

		bodyBox.Append(fields)
		hasBody = true

		col, row := 0, 0

		for _, field := range msgEmbed.Fields {
			text := gtk.NewLabel("")
			text.SetEllipsize(pango.EllipsizeEnd)
			text.SetXAlign(0.0)
			text.SetMarkup(fmt.Sprintf(
				`<span weight="heavy">%s</span>`+"\n"+`<span weight="light">%s</span>`,
				html.EscapeString(field.Name),
				html.EscapeString(field.Value),
			))
			text.SetTooltipText(field.Name + "\n" + field.Value)

			// I have no idea what this does. It's just improvised.
			if field.Inline && col < 3 {
				fields.Attach(text, col, row, 1, 1)
				col++
			} else {
				if col > 0 {
					row++
				}

				col = 0
				fields.Attach(text, col, row, 1, 1)

				if !field.Inline {
					row++
				} else {
					col++
				}
			}
		}
	}

	if msgEmbed.Footer != nil || msgEmbed.Timestamp.IsValid() {
		footer := gtk.NewBox(gtk.OrientationHorizontal, 0)
		footer.AddCSSClass("message-embed-footer")

		if msgEmbed.Footer != nil {
			if msgEmbed.Footer.ProxyIcon != "" {
				img := onlineimage.NewAvatar(ctx, imgutil.HTTPProvider, 18)
				img.AddCSSClass("message-embed-footer-icon")

				footer.Append(img)
			}

			if msgEmbed.Footer.Text != "" {
				text := gtk.NewLabel(msgEmbed.Footer.Text)
				text.SetVAlign(gtk.AlignStart)
				text.SetSingleLineMode(true)
				text.SetEllipsize(pango.EllipsizeEnd)
				text.SetTooltipText(msgEmbed.Footer.Text)
				text.SetXAlign(0)

				footer.Append(text)
			}
		}

		if msgEmbed.Timestamp.IsValid() {
			time := gtkcord.TimeAgo(msgEmbed.Timestamp.Time())

			text := gtk.NewLabel(time)
			text.AddCSSClass("message-embed-timestamp")
			if msgEmbed.Footer != nil {
				text.SetText(" - " + time)
			}

			footer.Append(text)
		}

		bodyBox.Append(footer)
		hasBody = true
	}

	// mod: embeds — append extra body widgets (e.g. multi-image grid)
	for _, w := range extraBody {
		bodyBox.Append(w)
		hasBody = true
	}

	embedBox := bodyBox
	if hasBody {
		// bodyBox.SetHAlign(gtk.AlignFill)
		// bodyBox.SetHExpand(false)

		embedBox = gtk.NewBox(gtk.OrientationHorizontal, 0)
		embedBox.SetHAlign(gtk.AlignFill) // mod: embeds — fill message width
		embedBox.Append(bodyBox)
		normalEmbedCSS(embedBox)

		if msgEmbed.Color != discord.NullColor {
			cssutil.Applyf(embedBox, embedColorCSSf, msgEmbed.Color.String())
		}
	}

	if msgEmbed.Thumbnail != nil {
		thumb := msgEmbed.Thumbnail
		big := !hasBody ||
			msgEmbed.Type == discord.GIFVEmbed ||
			msgEmbed.Type == discord.ImageEmbed ||
			msgEmbed.Type == discord.VideoEmbed ||
			msgEmbed.Type == discord.ArticleEmbed ||
			// mod: embeds — rich embeds with video or large thumbnails
			// (where the thumbnail IS the media) should display full-size.
			// Don't include Image here — those render separately below.
			msgEmbed.Video != nil ||
			(thumb.Width >= 300) ||
			// mod: embeds — when thumbnail is the ONLY media (no Image or
			// Video field), it IS the content. Covers fixupx, fxtwitter, etc.
			(msgEmbed.Image == nil && msgEmbed.Video == nil)

		maxW := 80
		maxH := 80
		if big {
			maxW = maxEmbedWidth.Value()
			maxH = maxImageHeight.Value()
		}

		opts := defaultEmbedOpts
		switch msgEmbed.Type {
		case discord.NormalEmbed, discord.ImageEmbed:
			// mod: embeds — rich embeds with a video field.
			// Twitter/fxtwitter "GIFs" have "tweet_video" in the URL and
			// are short loops — treat as GIFV for inline autoplay.
			// Other video embeds get click-to-play.
			if msgEmbed.Video != nil {
				videoURL := string(msgEmbed.Video.URL)
				if strings.Contains(videoURL, "tweet_video") ||
					strings.HasSuffix(videoURL, ".mp4") ||
					strings.HasSuffix(videoURL, ".webm") {
					opts.Type = embed.EmbedTypeGIFV
					opts.Autoplay = true
				} else {
					opts.Type = embed.EmbedTypeVideo
					opts.Autoplay = mods.AutoPlayVideos.Value()
				}
			} else {
				opts.Type = mods.TypeFromURL(thumb.Proxy)
				if opts.Type == embed.EmbedTypeGIF {
					opts.Autoplay = mods.AutoAnimateGIFs.Value() // mod: embeds
				}
			}
		case discord.VideoEmbed:
			opts.Type = embed.EmbedTypeVideo
			opts.Autoplay = mods.AutoPlayVideos.Value() // mod: embeds
		case discord.GIFVEmbed:
			// GIFV embeds are short looped videos — always auto-play them.
			// The chatkit embed downloads the .mp4 and plays via MediaFile,
			// which only triggers when Autoplay is true.
			opts.Autoplay = true
			opts.Type = embed.EmbedTypeGIFV
		}

		// mod: embeds — prefer real .gif over .mp4 for Tenor/Giphy
		var gifOverrideURL string
		if msgEmbed.Type == discord.GIFVEmbed {
			if u, ok := mods.GIFVToGIF(msgEmbed); ok {
				gifOverrideURL = u
				opts.Type = embed.EmbedTypeGIF
			}
		}

		image := embed.New(ctx, maxW, maxH, opts)
		image.SetVAlign(gtk.AlignStart)
		if thumb.Width > 0 && thumb.Height > 0 {
			// Enforce this image's own dimensions if possible.
			image.ShrinkMaxSize(int(thumb.Width), int(thumb.Height))
			image.SetSizeRequest(int(thumb.Width), int(thumb.Height))
		}

		// mod: embeds — for GIFV embeds, load the video URL directly instead
		// of the thumbnail. Chatkit's SetFromURL re-classifies by URL extension,
		// so passing a .jpg thumbnail makes it render as a static image even
		// when opts.Type is GIFV. Passing the .mp4 URL triggers the video
		// download + autoplay path in chatkit.
		if opts.Type == embed.EmbedTypeGIFV && msgEmbed.Video != nil {
			// mod: embeds — for GIFV, prefer the direct Video.URL over
			// Discord's proxy. The proxy URL is a Discord CDN redirect
			// that may not be playable. The source URL from fxtwitter/
			// fixupx is a direct media file.
			videoURL := string(msgEmbed.Video.URL)
			if videoURL == "" {
				videoURL = string(msgEmbed.Video.Proxy)
			}
			// Animated webp can't be played by GTK — swap to mp4.
			// Twitter serves both at the same path.
			if strings.HasSuffix(videoURL, ".webp") {
				videoURL = strings.TrimSuffix(videoURL, ".webp") + ".mp4"
			}
			mods.LazyLoad(image, func() { image.SetFromURL(videoURL) })
		} else if gifOverrideURL != "" {
			url := gifOverrideURL
			mods.LazyLoad(image, func() { image.SetFromURL(url) })
		} else if msgEmbed.Image == nil && msgEmbed.Video == nil {
			// mod: embeds — sole-media thumbnail: try proxy first if it looks
			// valid (has a path beyond /external/), otherwise use direct URL.
			// Discord's proxy works for most sites but is broken/truncated
			// for fixupx, fxtwitter, etc.
			thumbLoadURL := string(thumb.Proxy)
			if thumbLoadURL == "" || mods.IsBrokenProxy(thumbLoadURL) {
				thumbLoadURL = string(thumb.URL)
			}
			mods.LazyLoad(image, func() { image.SetFromURL(thumbLoadURL) })
		} else {
			url := resizeURL(thumb.URL, thumb.Proxy, int(thumb.Width), int(thumb.Height))
			mods.LazyLoad(image, func() { image.SetFromURL(url) })
		}

		// mod: embedmenu — use MediaFilename for sane viewer titles
		switch {
		case gifOverrideURL != "":
			image.SetName(mods.MediaFilename(gifOverrideURL, ".gif"))
		case msgEmbed.Image != nil:
			image.SetName(mods.MediaFilename(msgEmbed.Image.URL, ".jpg"))
		case msgEmbed.Video != nil:
			image.SetName(mods.MediaFilename(msgEmbed.Video.URL, ".mp4"))
		default:
			image.SetName(mods.MediaFilename(thumb.URL, ".jpg"))
		}

		switch {
		case msgEmbed.Image != nil:
			image.SetOpenURL(func() {
				openViewer(ctx, msgEmbed.Image.Proxy, opts, int(msgEmbed.Image.Width), int(msgEmbed.Image.Height))
			})
		case msgEmbed.Video != nil:
			image.SetOpenURL(func() {
				// mod: embeds — GIF override: open the GIF in viewer directly
				if gifOverrideURL != "" {
					openViewer(ctx, gifOverrideURL, opts, int(thumb.Width), int(thumb.Height))
					return
				}

				// mod: embeds — resolve video URL. For GIFV (fxtwitter etc.),
				// prefer direct URL over proxy since proxy may redirect to
				// Discord CDN. For regular videos, prefer proxy then direct.
				var videoURL string
				if opts.Type == embed.EmbedTypeGIFV {
					videoURL = string(msgEmbed.Video.URL)
					if videoURL == "" {
						videoURL = string(msgEmbed.Video.Proxy)
					}
				} else {
					videoURL = string(msgEmbed.Video.Proxy)
					if videoURL == "" {
						videoURL = string(msgEmbed.Video.URL)
					}
				}
				// Animated webp → mp4 swap for Twitter GIFs.
				if strings.HasSuffix(videoURL, ".webp") {
					videoURL = strings.TrimSuffix(videoURL, ".webp") + ".mp4"
				}
				if msgEmbed.URL != "" && mods.IsVideoHost(videoURL) {
					videoURL = msgEmbed.URL
				}

				switch opts.Type {
				case embed.EmbedTypeGIFV:
					// Play GIFVs in the embed. The image library will handle
					// rendering the GIFV like a GIF.
					image.SetFromURL(videoURL)
					image.ActivateDefault()
					// Override the next click to open the video in the viewer.
					image.SetOpenURL(func() {
						openViewer(ctx, videoURL, opts, int(msgEmbed.Video.Width), int(msgEmbed.Video.Height))
					})
				case embed.EmbedTypeVideo:
					// mod: videoplayer — for hosts whose extracted URLs are
					// header-gated (TikTok etc.), the native viewer can't fetch
					// the stream. Hand off to mpv directly — yt-dlp inside mpv
					// applies the right headers.
					if mods.PrefersMpv(videoURL) && mods.TryPlayVideo(videoURL) {
						return
					}
					// mod: videoplayer — for streaming hosts (YouTube etc.),
					// show a loading popup while yt-dlp extracts the stream URL,
					// then open the native viewer with the direct URL.
					if mods.IsVideoHost(videoURL) {
						w, h := int(msgEmbed.Video.Width), int(msgEmbed.Video.Height)
						vURL, vOpts := videoURL, opts
						loading := mods.NewVideoLoadingWindow(ctx)
						go func() {
							directURL, err := mods.ExtractStreamURL(vURL)
							if err == nil {
								glib.IdleAdd(func() {
									loading.Close()
									openViewer(ctx, directURL, vOpts, w, h)
								})
								return
							}
							// Extraction failed — try mpv, else show error.
							glib.IdleAdd(func() {
								if mods.TryPlayVideo(vURL) {
									loading.Close()
								} else {
									loading.SetError("Failed to load video")
								}
							})
						}()
						return
					}
					// Direct media URL — try mpv then viewer.
					if mods.TryPlayVideo(videoURL) {
						return
					}
					openViewer(ctx, videoURL, opts, int(msgEmbed.Video.Width), int(msgEmbed.Video.Height))
				}
			})
		default:
			// mod: embeds — prefer proxy URL for viewer. The inline thumbnail
			// loads via proxy successfully, but direct URLs from sites like
			// Wikipedia reject plain HTTP fetches (no User-Agent/Referer).
			// Only fall back to direct if proxy is empty or broken.
			viewURL := string(msgEmbed.Thumbnail.Proxy)
			if viewURL == "" || mods.IsBrokenProxy(viewURL) {
				viewURL = string(msgEmbed.Thumbnail.URL)
			}
			image.SetOpenURL(func() {
				openViewer(ctx, viewURL, opts, int(thumb.Width), int(thumb.Height))
			})
		}

		// mod: embedmenu — right-click to save/copy/open
		switch {
		case gifOverrideURL != "":
			mods.AttachEmbedContextMenu(ctx, image, gifOverrideURL, mods.MediaFilename(gifOverrideURL, ".gif"))
		case msgEmbed.Video != nil:
			mods.AttachEmbedContextMenu(ctx, image, msgEmbed.Video.URL, mods.MediaFilename(msgEmbed.Video.URL, ".mp4"))
		case msgEmbed.Image != nil:
			mods.AttachEmbedContextMenu(ctx, image, msgEmbed.Image.URL, mods.MediaFilename(msgEmbed.Image.URL, ".jpg"))
		default:
			mods.AttachEmbedContextMenu(ctx, image, thumb.URL, mods.MediaFilename(thumb.URL, ".jpg"))
		}

		if big {
			image.SetHAlign(gtk.AlignStart)
			bodyBox.Append(image)
		} else {
			image.SetHAlign(gtk.AlignEnd)
			embedBox.Append(image)
		}
	}

	// mod: embeds — always render the Image field full-size below the body,
	// even when a thumbnail exists (e.g. Twitter/X: thumbnail is the X logo,
	// Image is the actual tweet photo). This also handles multi-image tweets
	// since Discord creates separate embeds per image.
	if msgEmbed.Image != nil {
		opts := defaultEmbedOpts
		img := *msgEmbed.Image
		opts.Type = mods.TypeFromURL(msgEmbed.Image.URL)
		if opts.Type == embed.EmbedTypeGIF {
			opts.Autoplay = mods.AutoAnimateGIFs.Value() // mod: embeds
		}

		image := embed.New(ctx, maxEmbedWidth.Value(), maxImageHeight.Value(), opts)
		image.SetSizeRequest(int(img.Width), int(img.Height))
		image.SetOpenURL(func() {
			openViewer(ctx, img.Proxy, opts, int(img.Width), int(img.Height))
		})
		url := resizeURL(img.URL, img.Proxy, int(img.Width), int(img.Height))
		// mod: lazyload
		mods.LazyLoad(image, func() { image.SetFromURL(url) })

		// mod: embedmenu — right-click to save/copy/open
		mods.AttachEmbedContextMenu(ctx, image, msgEmbed.Image.URL, mods.MediaFilename(msgEmbed.Image.URL, ".jpg"))

		bodyBox.Append(image)
	}

	// Render video full-size when no thumbnail already handles it.
	if msgEmbed.Thumbnail == nil && msgEmbed.Video != nil {
		opts := defaultEmbedOpts
		img := (discord.EmbedImage)(*msgEmbed.Video)
		opts.Type = embed.EmbedTypeVideo
		opts.Provider = imgutil.FFmpegProvider
		opts.Autoplay = mods.AutoPlayVideos.Value() // mod: embeds

		image := embed.New(ctx, maxEmbedWidth.Value(), maxImageHeight.Value(), opts)
		image.SetSizeRequest(int(img.Width), int(img.Height))
		image.SetOpenURL(func() {
			openViewer(ctx, img.URL, opts, int(img.Width), int(img.Height))
		})
		// Video embeds use FFmpegProvider — skip lazy load so the
		// embed's click/play handler initializes immediately.
		image.SetFromURL(string(img.Proxy))

		// mod: embedmenu — right-click to save/copy/open
		mods.AttachEmbedContextMenu(ctx, image, msgEmbed.Video.URL, mods.MediaFilename(msgEmbed.Video.URL, ".mp4"))

		bodyBox.Append(image)
	}

	embedBox.AddCSSClass("message-richframe")
	return embedBox
}

// openViewer opens media in a viewer window. If contentW/contentH are provided,
// the window is sized to fit the content (with padding for controls).
func openViewer(ctx context.Context, uri string, opts embed.Opts, dims ...int) {
	// mod: videoplayer — chatkit's TypeFromURL uses path.Ext on the raw URL
	// string, which misclassifies video URLs that lack a clean extension:
	// yt-dlp URLs (googlevideo.com/videoplayback?...) have no extension,
	// and Discord proxy URLs (...video.mp4?width=500) have query params
	// that make path.Ext return ".mp4?width=500" instead of ".mp4".
	// Appending a #v.mp4 fragment fixes detection without affecting the
	// HTTP request (fragments are stripped by net/http).
	if opts.Type == embed.EmbedTypeVideo {
		lower := strings.ToLower(uri)
		if !strings.HasSuffix(lower, ".mp4") && !strings.HasSuffix(lower, ".webm") && !strings.HasSuffix(lower, ".mov") {
			if parsed, err := url.Parse(uri); err == nil {
				parsed.Fragment = "v.mp4"
				uri = parsed.String()
			}
		}
	}

	embedViewer, err := embed.NewViewer(ctx, uri, opts)
	if err != nil {
		app.Error(ctx, err)
		return
	}

	// mod: videoplayer — allow interacting with main window while viewer is open
	embedViewer.SetModal(false)

	// mod: embeds — size viewer to match content dimensions, capped to
	// the active monitor's usable area so oversized media doesn't open
	// a window taller or wider than the screen.
	if len(dims) >= 2 && dims[0] > 0 && dims[1] > 0 {
		w, h := dims[0], dims[1]
		const headerH = 48
		maxW, maxH := viewerMaxSize()
		maxH -= headerH
		if w > maxW {
			h = h * maxW / w
			w = maxW
		}
		if h > maxH {
			w = w * maxH / h
			h = maxH
		}
		embedViewer.SetDefaultSize(w, h+headerH)
	}

	embedViewer.Show()
}

// viewerMaxSize returns the largest dimensions the media viewer should
// open at — the smallest connected monitor's geometry minus a margin so
// window decorations and panels don't push it off-screen on whichever
// display the user happens to drag it to.
func viewerMaxSize() (w, h int) {
	const fallbackW, fallbackH = 1600, 1000
	const margin = 80

	display := gdk.DisplayGetDefault()
	if display == nil {
		return fallbackW, fallbackH
	}

	w, h = -1, -1
	gtkutil.EachList(display.Monitors(), func(mon *gdk.Monitor) {
		geom := mon.Geometry()
		if w < 0 || geom.Width() < w {
			w = geom.Width()
		}
		if h < 0 || geom.Height() < h {
			h = geom.Height()
		}
	})

	w -= margin
	h -= margin
	if w <= 0 || h <= 0 {
		return fallbackW, fallbackH
	}
	return w, h
}

// stripMarkdownEscapes removes backslash escapes from embed text.
// Discord's API sends "example\.com" to prevent auto-linking; the
// backslashes should not be rendered literally.
func stripMarkdownEscapes(s string) string {
	// Only strip backslashes before common punctuation that Discord escapes.
	// Don't strip \\n, \\t, etc.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			// Standard markdown escapable characters.
			if (next >= '!' && next <= '/') || // !"#$%&'()*+,-./
				(next >= ':' && next <= '@') || // :;<=>?@
				(next >= '[' && next <= '`') || // [\]^_`
				(next >= '{' && next <= '~') { // {|}~
				i++ // skip backslash, emit next char
				b.WriteByte(next)
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func fixNatWrap(label *gtk.Label) {
	if err := gtk.CheckVersion(4, 6, 0); err == "" {
		label.SetObjectProperty("natural-wrap-mode", 1) // NaturalWrapNone
	}
}
