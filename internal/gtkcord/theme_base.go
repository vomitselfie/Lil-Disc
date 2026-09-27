package gtkcord

// baseCSS styles GTK's stock widgets for LilDisc's own theme.
//
// It is installed above USER priority, so it has to be complete for every
// property a host theme might set. A GTK theme copied into
// ~/.config/gtk-4.0/gtk.css paints gradients, text shadows, border images
// and PNG assets onto stock widgets, and anything left unset here shows
// through. That is why most rules reset background-image, box-shadow and
// text-shadow explicitly, and why switches and check boxes draw themselves
// from symbolic icons rather than theme assets (Matcha's assets are what made
// switches invisible in the first place).
//
// Component stylesheets are installed above this layer, so a component's own
// rule for a button still wins over the generic one here.
const baseCSS = `
	/* ── Type ─────────────────────────────────────────────────────────── */

	window, popover, tooltip {
		font-family: "Inter Variable", "Inter", system-ui, sans-serif;
		font-feature-settings: "cv11", "ss01", "ss03";
	}

	/* ── Surfaces ─────────────────────────────────────────────────────── */

	window, window.background, .background, dialog, .dialog-contents {
		background-color: @lil_surface;
		background-image: none;
		color: @lil_text;
		text-shadow: none;
		-gtk-icon-shadow: none;
	}
	/* A popover's own node carries .background too, but only its contents
	   should paint: filling the node fills the rectangle around the rounded
	   bubble and its shadow, which composites as a black box. */
	popover.background, popover.menu.background {
		background-color: transparent;
		background-image: none;
		box-shadow: none;
		border: none;
	}
	window.csd {
		border-radius: {$radius_xl};
		box-shadow: 0 0 0 1px @lil_border_strong,
		            0 8px 28px @lil_shadow;
	}
	window.maximized, window.fullscreen, window.tiled,
	window.tiled-top, window.tiled-bottom, window.tiled-left, window.tiled-right {
		border-radius: 0;
		box-shadow: none;
	}

	headerbar, .titlebar, .top-bar {
		background-color: @lil_surface;
		background-image: none;
		color: @lil_text;
		border: none;
		box-shadow: inset 0 -1px @lil_border;
		text-shadow: none;
	}
	headerbar:backdrop {
		background-color: @lil_surface;
	}
	headerbar .title, .title-label {
		font-weight: 650;
	}
	headerbar .subtitle {
		color: @lil_text_dim;
	}

	separator {
		background-color: @lil_border;
		background-image: none;
		min-width: 1px;
		min-height: 1px;
	}
	paned > separator {
		background-color: @lil_border;
		background-image: none;
		box-shadow: none;
	}

	selection, text selection, label selection, entry selection {
		background-color: alpha(@lil_accent, 0.35);
		color: @lil_text;
	}

	*:focus-visible {
		outline-color: alpha(@lil_accent, 0.6);
	}

	/* ── Buttons ──────────────────────────────────────────────────────── */

	button, menubutton > button, dropdown > button, spinbutton > button,
	splitbutton > button, splitbutton > menubutton > button {
		background-color: alpha(@lil_text, 0.07);
		background-image: none;
		border: none;
		border-radius: {$radius_md};
		box-shadow: none;
		color: @lil_text;
		text-shadow: none;
		-gtk-icon-shadow: none;
		transition: background-color 150ms cubic-bezier(0.25, 0.46, 0.45, 0.94),
		            color 150ms cubic-bezier(0.25, 0.46, 0.45, 0.94);
	}
	button:hover {
		background-color: alpha(@lil_text, 0.11);
	}
	button:active {
		background-color: alpha(@lil_text, 0.16);
	}
	button:checked {
		background-color: @lil_selected;
		color: @lil_accent_text;
	}
	button:disabled {
		background-color: alpha(@lil_text, 0.04);
		color: @lil_text_faint;
	}

	button.flat, menubutton.flat > button, button.image-button.flat,
	headerbar button, .toolbar button, popover button.flat,
	windowcontrols button {
		background-color: transparent;
	}
	button.flat:hover, menubutton.flat > button:hover,
	headerbar button:hover, .toolbar button:hover {
		background-color: @lil_hover;
	}
	button.flat:active, menubutton.flat > button:active,
	headerbar button:active, .toolbar button:active {
		background-color: @lil_active;
	}
	button.flat:checked, headerbar button:checked {
		background-color: @lil_selected;
	}

	button.suggested-action, button.default {
		background-color: @lil_accent;
		color: @lil_accent_fg;
	}
	button.suggested-action:hover, button.default:hover {
		background-color: mix(@lil_accent, white, 0.1);
	}
	button.suggested-action:active, button.default:active {
		background-color: mix(@lil_accent, black, 0.1);
	}
	button.destructive-action {
		background-color: @destructive_bg_color;
		color: white;
	}
	button.circular, button.pill {
		border-radius: {$radius_pill};
	}

	windowcontrols button {
		border-radius: {$radius_pill};
		min-width: 24px;
		min-height: 24px;
		padding: 0;
		margin: 0 2px;
	}
	windowcontrols button image {
		background: none;
		padding: 3px;
	}

	/* ── Text entry ───────────────────────────────────────────────────── */

	/* Inset rather than raised, so an entry reads as a well to type into
	   whether it sits on the window or on a card. */
	entry, spinbutton, searchbar entry, .search entry {
		background-color: @lil_surface_sunken;
		background-image: none;
		border: 1px solid @lil_border;
		border-radius: {$radius_lg};
		box-shadow: none;
		color: @lil_text;
		caret-color: @lil_accent_text;
		transition: border-color 150ms ease, box-shadow 150ms ease;
	}
	entry:focus-within, spinbutton:focus-within {
		border-color: alpha(@lil_accent, 0.7);
		box-shadow: 0 0 0 3px alpha(@lil_accent, 0.18);
		outline: none;
	}
	entry > text > placeholder {
		color: @lil_text_faint;
	}
	textview, textview > text, text {
		background-color: transparent;
		background-image: none;
		color: @lil_text;
		caret-color: @lil_accent_text;
	}

	/* ── Popovers and menus ───────────────────────────────────────────── */

	popover > contents, popover.menu > contents, popover > arrow {
		background-color: @lil_surface_overlay;
		background-image: none;
		border: 1px solid @lil_border_strong;
		color: @lil_text;
	}
	popover > contents {
		border-radius: {$radius_xl};
		box-shadow: 0 10px 32px @lil_shadow, 0 2px 6px alpha(black, 0.12);
		padding: {$space_sm};
	}
	popover.menu modelbutton, popover modelbutton {
		border-radius: {$radius_md};
		padding: {$space_sm} {$space_md};
		background-image: none;
		color: @lil_text;
	}
	popover.menu modelbutton:hover, popover modelbutton:hover,
	popover.menu modelbutton:selected {
		background-color: @lil_hover;
	}
	popover.menu separator {
		margin: {$space_xs} {$space_sm};
	}

	tooltip, tooltip.background {
		background-color: @lil_surface_overlay;
		background-image: none;
		border: 1px solid @lil_border_strong;
		border-radius: {$radius_md};
		box-shadow: 0 4px 14px @lil_shadow;
		color: @lil_text;
		text-shadow: none;
	}

	/* ── Lists ────────────────────────────────────────────────────────── */

	list, listview, gridview, columnview, .view, treeview, iconview {
		background-color: transparent;
		background-image: none;
		color: @lil_text;
	}
	list > row, listview > row {
		background-image: none;
		box-shadow: none;
		text-shadow: none;
	}
	list > row:hover, listview > row:hover {
		background-color: @lil_hover;
	}
	list > row:selected, listview > row:selected {
		background-color: @lil_selected;
		color: @lil_text;
	}

	list.boxed-list, listview.boxed-list, .card {
		background-color: @lil_surface_raised;
		border: 1px solid @lil_border;
		border-radius: {$radius_xl};
		box-shadow: none;
	}
	list.boxed-list > row {
		border-bottom: 1px solid @lil_border;
	}
	list.boxed-list > row:first-child {
		border-top-left-radius: {$radius_xl};
		border-top-right-radius: {$radius_xl};
	}
	list.boxed-list > row:last-child {
		border-bottom: none;
		border-bottom-left-radius: {$radius_xl};
		border-bottom-right-radius: {$radius_xl};
	}
	row .subtitle, row .dim-label, .dim-label {
		color: @lil_text_dim;
		opacity: 1;
	}

	/* ── Switches, checks, sliders ───────────────────────────────────── */

	switch {
		background-color: alpha(@lil_text, 0.18);
		background-image: none;
		border: none;
		border-radius: {$radius_pill};
		box-shadow: none;
		min-width: 40px;
		min-height: 22px;
		padding: 3px;
		transition: background-color 180ms ease;
	}
	switch:checked {
		background-color: @lil_accent;
	}
	switch > image {
		-gtk-icon-source: none;
		color: transparent;
	}
	switch > slider {
		background-color: white;
		background-image: none;
		border: none;
		border-radius: {$radius_pill};
		box-shadow: 0 1px 3px alpha(black, 0.3);
		min-width: 20px;
		min-height: 20px;
		margin: 0;
		-gtk-icon-source: none;
	}
	switch:disabled {
		opacity: 0.5;
	}

	check, radio {
		background-color: transparent;
		background-image: none;
		border: 2px solid alpha(@lil_text, 0.3);
		box-shadow: none;
		color: @lil_accent_fg;
		min-width: 14px;
		min-height: 14px;
		padding: 1px;
		-gtk-icon-source: none;
		-gtk-icon-size: 14px;
	}
	check {
		border-radius: 5px;
	}
	radio {
		border-radius: {$radius_pill};
	}
	check:hover, radio:hover {
		border-color: alpha(@lil_text, 0.45);
	}
	check:checked, radio:checked, check:indeterminate {
		background-color: @lil_accent;
		border-color: @lil_accent;
	}
	check:checked {
		-gtk-icon-source: -gtk-icontheme("object-select-symbolic");
	}
	check:indeterminate {
		-gtk-icon-source: -gtk-icontheme("list-remove-symbolic");
	}
	radio:checked {
		-gtk-icon-source: -gtk-icontheme("media-record-symbolic");
	}

	scale > trough, progressbar > trough, levelbar > trough {
		background-color: alpha(@lil_text, 0.14);
		background-image: none;
		border: none;
		border-radius: {$radius_pill};
		box-shadow: none;
	}
	scale > trough > highlight, progressbar > trough > progress {
		background-color: @lil_accent;
		background-image: none;
		border: none;
		border-radius: {$radius_pill};
	}
	scale > trough > slider {
		background-color: white;
		background-image: none;
		border: none;
		border-radius: {$radius_pill};
		box-shadow: 0 1px 3px alpha(black, 0.3);
		min-width: 16px;
		min-height: 16px;
		margin: -6px;
	}

	/* ── Scrollbars ───────────────────────────────────────────────────── */

	scrollbar {
		background-color: transparent;
		background-image: none;
		border: none;
		box-shadow: none;
	}
	scrollbar > range > trough {
		background-color: transparent;
		border: none;
	}
	scrollbar > range > trough > slider {
		background-color: alpha(@lil_text, 0.22);
		background-image: none;
		border: 2px solid transparent;
		border-radius: {$radius_pill};
		box-shadow: none;
		min-width: 6px;
		min-height: 6px;
		background-clip: padding-box;
	}
	scrollbar > range > trough > slider:hover {
		background-color: alpha(@lil_text, 0.38);
	}
	scrollbar.overlay-indicator:not(.hovering) > range > trough > slider {
		min-width: 3px;
		min-height: 3px;
	}

	/* ── Tabs and view switchers ─────────────────────────────────────── */

	notebook, notebook > stack, notebook > header, frame, frame > border {
		background-color: transparent;
		background-image: none;
		border: none;
		box-shadow: none;
	}
	notebook > header {
		padding: {$space_xs};
		margin-bottom: {$space_sm};
		border-radius: {$radius_lg};
		background-color: alpha(@lil_text, 0.06);
	}
	notebook > header tab {
		background-color: transparent;
		background-image: none;
		border: none;
		border-radius: {$radius_md};
		box-shadow: none;
		color: @lil_text_dim;
		padding: {$space_xs} {$space_lg};
		min-height: 0;
		transition: background-color 150ms ease, color 150ms ease;
	}
	notebook > header tab:hover {
		color: @lil_text;
		background-color: @lil_hover;
	}
	notebook > header tab:checked {
		background-color: @lil_surface_overlay;
		color: @lil_text;
		box-shadow: 0 1px 3px alpha(black, 0.15);
	}
	stackswitcher > button, .linked > button {
		border-radius: 0;
	}
	stackswitcher > button:first-child, .linked > button:first-child {
		border-top-left-radius: {$radius_md};
		border-bottom-left-radius: {$radius_md};
	}
	stackswitcher > button:last-child, .linked > button:last-child {
		border-top-right-radius: {$radius_md};
		border-bottom-right-radius: {$radius_md};
	}

	/* ── Toasts, banners, badges ─────────────────────────────────────── */

	toast {
		background-color: @lil_surface_overlay;
		border: 1px solid @lil_border_strong;
		border-radius: {$radius_pill};
		box-shadow: 0 6px 20px @lil_shadow;
		color: @lil_text;
	}
	banner > revealer > widget {
		background-color: @lil_mention;
		background-image: none;
	}
`
