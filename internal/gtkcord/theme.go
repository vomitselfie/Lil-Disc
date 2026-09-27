package gtkcord

import (
	"github.com/diamondburned/gotkit/gtkutil/cssutil"

	"github.com/vomitselfie/Lil-Disc/internal/lilcss"
)

// This file holds LilDisc's design tokens. Widget stylesheets are expected to
// reference these rather than inventing values, so that spacing, corner radii
// and type sizes stay on one scale across the app.
//
// There are two kinds of token, because GTK CSS supports only one of them
// natively:
//
//   - Colours are real CSS colours declared with @define-color, so they
//     resolve at runtime, follow the light/dark colour scheme, and can be
//     overridden by the user's ~/.config/lildisc/custom.css.
//   - Lengths and font sizes are Go-side template substitutions ({$name}),
//     because GTK CSS has no variables for non-colour values. They are
//     expanded once, when cssutil renders the global stylesheet.

// Spacing scale. Prefer these over literal pixel values.
//
// Before this existed the codebase used seventeen distinct pixel values for
// padding and margin (3, 5, 10, 11, 14, 15, 18, 20 and so on), which is the
// main reason the layout read as slightly arbitrary: nothing lined up with
// anything else. Five steps cover every real case.
const (
	SpaceHair = 2  // hairline separation inside a control
	SpaceXS   = 4  // between tightly related items
	SpaceSM   = 6  // default inside compact rows
	SpaceMD   = 8  // default inside comfortable rows
	SpaceLG   = 12 // between groups
	SpaceXL   = 16 // section padding
	SpaceXXL  = 24 // page-level breathing room
)

// Corner radii. Pill is deliberately large rather than a percentage so that
// it stays circular on non-square widgets.
const (
	RadiusSM   = 4   // chips, badges, inline media
	RadiusMD   = 6   // rows, buttons
	RadiusLG   = 8   // cards, embeds, inputs
	RadiusXL   = 12  // popovers, boxed lists, floating surfaces
	RadiusPill = 999 // fully rounded
)

// Row metrics, shared by every selectable list row in the sidebar.
//
// The sidebar previously ran two different row systems side by side in the
// same column: the channel tree used em-based padding with no minimum height,
// square corners and full-bleed highlights, while the DM and friend lists
// used a fixed 36px height, a 6px radius and an inset margin. One column, two
// design languages. These are the single set both now use.
const (
	RowHeight = 34 // comfortable for a 32px avatar plus breathing room
	RowInset  = 6  // horizontal inset, so highlights float rather than bleed
)

// Type scale, in em so it tracks the user's font size. Six steps replace the
// sixteen ad-hoc sizes previously in use, which included a mix of em, px and
// pt units.
const (
	FontMicro = "0.75em" // counts, badges
	FontSmall = "0.85em" // timestamps, secondary metadata
	FontBody  = "1em"    // message text and everything else by default
	FontTitle = "1.05em" // header and row titles
	FontLarge = "1.2em"  // section headings
	FontJumbo = "2em"    // emoji-only messages
)

// Line heights. Message text is the one thing in this app people actually
// read at length, so it gets loose leading; everything else stays tight so
// the UI does not sprawl.
//
// GTK gained the line-height CSS property in 4.12; on anything older it is
// ignored with a warning rather than failing to parse.
const (
	LineTight = "1.25"
	LineBody  = "1.45"
)

// Semantic colours.
//
// Every colour is derived from the libadwaita palette rather than hardcoded,
// so the app follows the system (and Discord's) light/dark preference instead
// of only looking right in one of them. Widgets should use these names, not
// @theme_* directly, so that a change here reaches the whole app.
//
// These are the fallback used when LilDisc's own theme (palette.go) is turned
// off. They go straight to cssutil rather than through lilcss, because lilcss
// reinstalls its stylesheets above the theme's palette, and these
// definitions would then override it.
var _ = cssutil.WriteCSS(`
	/* Surfaces, from furthest back to closest to the user. */
	@define-color lil_rail             mix(@theme_bg_color, black, 0.24);
	@define-color lil_surface          @theme_bg_color;
	@define-color lil_surface_sunken   mix(@theme_bg_color, black, 0.16);
	@define-color lil_surface_raised   mix(@theme_bg_color, white, 0.05);
	@define-color lil_surface_overlay  mix(@theme_bg_color, white, 0.08);

	/* Text, in descending prominence. Metadata uses a dimmer colour rather
	   than a smaller size wherever possible, so it stays legible. */
	@define-color lil_text        @theme_fg_color;
	@define-color lil_text_dim    alpha(@theme_fg_color, 0.7);
	@define-color lil_text_faint  alpha(@theme_fg_color, 0.45);

	/* Lines. Deliberately soft: this design separates regions with spacing
	   and surface level first, and only falls back to a rule when it must. */
	@define-color lil_border       alpha(@borders, 0.5);
	@define-color lil_border_strong @borders;

	/* Interaction states, layered on whatever surface they sit on. */
	@define-color lil_hover     alpha(@theme_fg_color, 0.06);
	@define-color lil_active    alpha(@theme_fg_color, 0.10);
	@define-color lil_selected  alpha(@theme_selected_bg_color, 0.22);

	@define-color lil_accent      @theme_selected_bg_color;
	@define-color lil_accent_text @theme_selected_bg_color;
	@define-color lil_accent_fg   @theme_selected_fg_color;
	@define-color lil_shadow      alpha(black, 0.35);
	@define-color lil_mention   alpha(@theme_selected_bg_color, 0.18);

	/* Discord's status palette. Defined here rather than in the presence mod
	   so that everything drawing a status uses one set of values. */
	@define-color lil_status_online   #3ba55d;
	@define-color lil_status_idle     #faa81a;
	@define-color lil_status_dnd      #ed4245;
	@define-color lil_status_offline  #80848e;

	/* Unread/mention red. Previously declared inside the guild pill's
	   stylesheet but consumed by the channel list and the message row, which
	   left those packages silently depending on the sidebar being linked in. */
	@define-color mentioned #ed4245;
`)

func init() {
	lilcss.AddCSSVariables(map[string]string{
		"space_hair": px(SpaceHair),
		"space_xs":   px(SpaceXS),
		"space_sm":   px(SpaceSM),
		"space_md":   px(SpaceMD),
		"space_lg":   px(SpaceLG),
		"space_xl":   px(SpaceXL),
		"space_xxl":  px(SpaceXXL),

		"radius_sm":   px(RadiusSM),
		"radius_md":   px(RadiusMD),
		"radius_lg":   px(RadiusLG),
		"radius_xl":   px(RadiusXL),
		"radius_pill": px(RadiusPill),

		"row_height": px(RowHeight),
		"row_inset":  px(RowInset),

		"font_micro": FontMicro,
		"font_small": FontSmall,
		"font_body":  FontBody,
		"font_title": FontTitle,
		"font_large": FontLarge,
		"font_jumbo": FontJumbo,

		"line_tight": LineTight,
		"line_body":  LineBody,
	})
}
