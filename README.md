# LilDisc

A native GTK4/Go Discord client for Linux. Wayland-first, lightweight, with a modular feature system inspired by Vesktop.

LilDisc is a standalone application — not a wrapper around Discord's web UI. It speaks directly to Discord's gateway via [arikawa](https://github.com/diamondburned/arikawa) and renders everything natively in GTK4 / libadwaita.

## Features

All optional features are independently toggleable in the Mods preferences pane and live as self-contained modules under `internal/mods/`.

### Embed & Media
- **Auto-animate GIFs** inline instead of requiring click
- **Full-size Twitter/X images** — renders images at proper size inside embed cards, not as tiny thumbnails
- **Direct URL loading** — bypasses Discord's broken external proxy for fixupx, fxtwitter, vxtwitter and similar services
- **Multi-image grid** — Twitter multi-image posts render in a 2-column grid inside the embed card
- **Prefer GIF over MP4** — loads actual .gif for Giphy embeds instead of video
- **Full-width embed cards** — rich embeds fill the message area
- **Content-sized viewer** — image/video popup matches media dimensions

### Right-Click on Media
- **Save As** — download source file with proper filename
- **Copy URL** — copy original source URL
- **Open in Browser** — open in default browser

### Sidebar
- **Resizable sidebar** — draggable divider between sidebar and chat
- **Compact mode** — narrows below 180px, collapses to avatar-only
- **Presence badges** — colored status dots overlaid on avatar corners (online/idle/DND/offline)
- **Friend nicknames** — personal nicknames set via Discord Relationships are used everywhere (DM list, group DM names, chat author labels, quick switcher)
- **More dropdown in DMs** — collapsible "More (N)" section under the active DM list showing friends who don't currently have an open DM; click to start a new conversation

### Composer
- **Drag-and-drop upload** — drop files onto the message input
- **Clipboard image paste** — Ctrl+V works for images copied from browsers
- **Reply preview bar** — shows what you're replying to above the input
- **GIF picker** — Tenor search with inline previews
- **Sticker picker** — server stickers and default sticker packs, grouped by source
- **Oversize upload fallback** — files too large for Discord's attachment limit are uploaded to [0x0.st](https://0x0.st) (512 MiB cap) and the resulting URL is pasted into the composer in place of the attachment. The free-tier limit that triggers this is configurable, since Discord has changed it more than once

### Channels
- **Channel context menu** — right-click for mark-as-read and copy-channel-ID

### Other
- **Custom CSS** — loads `~/.config/lildisc/custom.css`
- **Prefer integrated GPU** — keeps rendering and video decoding off a discrete or external GPU (see [GPU selection](#gpu-selection))
- **Startup environment** — loads `KEY=VALUE` lines from `~/.config/lildisc/env` before GTK initialises, for settings that must be decided before the app starts
- **System tray** — minimize to tray on close
- **Message search** — Ctrl+F within channels
- **Keyboard shortcuts** — Ctrl+/ for help overlay
- **Avatar cache refresh** — clears stale avatars when profiles change
- **Server emoji picker** — all server emojis with fuzzy search
- **Notification controls** — notify-all and mute-respect options
- **mpv video player** — plays videos via mpv instead of the built-in player; yt-dlp extraction for streaming hosts (YouTube, Twitter/X, Twitch, TikTok, …)
- **Inline audio player** — voice messages and audio attachments play inline with seek controls instead of a download link
- **Cache auto-clean** — periodically clears stale image cache entries
- **Lazy load embeds** — defers loading embed images until they scroll into view

## Building

```bash
# Prerequisites (Arch/Manjaro)
sudo pacman -S go gtk4 gobject-introspection

# Build
go build -v -o lildisc .

# Run
./lildisc
```

First build takes ~20 minutes (CGo/GTK4 bindings). Incremental builds are fast.

Requires Go 1.24+.

## Architecture

All features are in `internal/mods/` as independent files. Each mod has a preference toggle and hooks into the host app via minimal integration points marked with `// mod: feature-name` comments.

```
internal/mods/
  mods.go            Init()/HookState() entry points
  apicache.go        Shared on-disk JSON cache helper
  audioplayer.go     Inline audio/voice-message player
  avatarcache.go     Avatar cache busting + manual-refresh signaler
  cacheclean.go      Cache auto-clean
  channelmenu.go     Channel context menu
  compactsidebar.go  Compact avatar-only sidebar mode
  customcss.go       Custom CSS loading
  dragdrop.go        Drag-and-drop file upload
  embedmenu.go       Right-click save/copy/open on media
  embeds.go          GIF/video autoplay, GIFV-to-GIF, embed improvements
  emojipicker.go     Server emoji picker
  friendlist.go      Collapsible "More" friends dropdown in DM sidebar
  gifpicker.go       Tenor-backed GIF picker
  keybinds.go        Keyboard shortcuts
  lazyload.go        Lazy load embeds
  mediahost.go       Oversize-upload fallback (0x0.st)
  notifications.go   Notification improvements
  presence.go        Status indicator dots
  replypreview.go    Reply preview bar
  search.go          Message search
  stickerpicker.go   Sticker picker
  tray.go            System tray
  videoplayer.go     mpv video player
```

## GPU selection

On a hybrid or eGPU machine, LilDisc may wake a discrete GPU that isn't the
one driving your display. Two separate things cause it:

- **GTK's renderer.** GTK 4.14+ defaults to Vulkan, and its device selection
  prefers a discrete GPU when it finds one — even an external card that isn't
  attached to any monitor. That happens when the window opens, with no video
  involved.
- **Hardware video decoding.** Video and GIFV embeds decode through GStreamer,
  and mpv playback through `--hwdec`. Both will pick NVDEC when an NVIDIA card
  is present.

Turn on **Preferences → Graphics → Prefer Integrated GPU**. It applies at the
next launch, because both settings are consumed before the app has a settings
window to read: `GSK_RENDERER` when the renderer is created, and
`GST_PLUGIN_FEATURE_RANK` as a decoding pipeline is built. The toggle is read
straight out of `prefs.json` at startup for that reason.

For anything the toggle doesn't cover — pinning Vulkan to a specific device,
say — `~/.config/lildisc/env` is applied at startup too, and wins over the
toggle:

```sh
# Keep Vulkan, pinned to a specific device (vendorID:deviceID, from
# `lspci -nn`) instead of falling back to the GL renderer.
VK_LOADER_DEVICE_SELECT=1002:150e
GSK_RENDERER=vulkan
```

Anything already set in the environment wins, so you can still override a line
for one run from a shell. To check which device GTK chose, run it with
`GDK_DEBUG=vulkan`.

## Logging In

Use your Discord user token:

1. Open Discord web app, press F12 for Inspector
2. Go to Network tab, press F5 to refresh
3. Filter for `discord api`, click any request
4. Copy the `Authorization` header value
5. Paste into the Token field in LilDisc

**IMPORTANT:** Using an unofficial client is against Discord's Terms of Service
and may cause your account to be banned! While LilDisc tries its best to not
use the REST API at all unless necessary to reduce the risk of abuse, it is
still possible that Discord may ban your account for using it. Please use
LilDisc at your own risk!

## Credits

LilDisc began as a fork of [Dissent](https://github.com/diamondburned/dissent) by diamondburned, and still shares its rendering core.

- [Dissent](https://github.com/diamondburned/dissent) — the original native GTK4 Discord client this project descends from
- [Vesktop](https://github.com/Vencord/Vesktop) — design inspiration for the modular feature system
- [arikawa](https://github.com/diamondburned/arikawa), [gotk4](https://github.com/diamondburned/gotk4), [gotkit](https://github.com/diamondburned/gotkit), [chatkit](https://github.com/diamondburned/chatkit), [ningen](https://github.com/diamondburned/ningen) — core libraries by diamondburned

## License

GPL-3.0.
