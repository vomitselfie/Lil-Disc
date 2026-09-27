<p align="center">
  <img src=".github/logo.png" alt="LilDisc logo: a blurple MiniDisc cartridge" width="160">
</p>

<h1 align="center">LilDisc</h1>

<p align="center">A small, fast Discord app for Linux that doesn't run a web browser under the hood.</p>

## What is this?

LilDisc is a Discord client. You log in with your Discord account and get your
servers, DMs, friends, images, GIFs, stickers and notifications in a normal
desktop window.

The difference from the official Discord app is what's inside. Discord's app is
a website wrapped in a browser, which is why it's heavy and slow to start.
LilDisc is a real Linux program built with GTK, the same toolkit as GNOME apps.
It starts in a second, uses far less memory, looks like it belongs on your
desktop, and follows your light or dark theme.

It's Wayland-first, works fine on X11, and every extra feature is a switch you
can turn on or off in Preferences.

> **Read this before you use it.** Discord's Terms of Service forbid
> third-party clients. LilDisc goes out of its way to behave like the official
> app and avoids unnecessary API calls, but Discord could still suspend or ban
> an account for using it. Use it with an account you can afford to lose.

## Getting it

There are no packages yet, so you build it yourself. It's three commands.

**1. Install the tools.** On Arch or Manjaro:

```bash
sudo pacman -S go gtk4 libadwaita gobject-introspection
```

On other distributions, install Go 1.24 or newer plus the GTK4 and libadwaita
development packages from your package manager.

**2. Build it.** From a clone of this repository:

```bash
go build -o lildisc .
```

The first build compiles the GTK bindings and takes a while, sometimes 20
minutes. Every build after that takes seconds.

**3. Run it.**

```bash
./lildisc
```

To get LilDisc into your app launcher with its icon, run `./install-local.sh`
once. It links the binary into `~/.local/bin` and installs the desktop entry
and icon for your user only. Run it with `--uninstall` to undo that.

Nix users: the repository is a flake.

## Logging in

LilDisc needs your Discord token. That's the secret string the official app
uses to prove it's you. Never share it with anyone: it is as good as your
password.

1. Open [discord.com/app](https://discord.com/app) in your browser and log in.
2. Press **F12** to open the developer tools, then open the **Network** tab.
3. Press **F5** to reload the page.
4. In the filter box type `api`, then click any request in the list.
5. Under **Request Headers**, find **Authorization** and copy its value.
6. Paste it into the Token field in LilDisc.

Tick **Remember me** and LilDisc keeps the token in your system keyring, or in
a file encrypted with a password you choose, so you only do this once.

## What it can do

Everything below is optional. Open **Preferences → Mods** and flip switches.

**Pictures, GIFs and video**
- GIFs play on their own instead of waiting for a click.
- Images from Twitter/X show at full size, and multi-image posts appear in a grid.
- Links to fixupx, fxtwitter, vxtwitter and similar fixer sites load directly, sidestepping Discord's often-broken proxy.
- Giphy embeds load the actual GIF rather than a silent video.
- Embed cards can fill the width of the chat, and the image viewer sizes itself to the picture.
- Right-click any image or video to **save it**, **copy its link**, or **open it in your browser**.
- Videos can play in [mpv](https://mpv.io) instead of the built-in player, with [yt-dlp](https://github.com/yt-dlp/yt-dlp) handling YouTube, Twitter/X, Twitch, TikTok and friends.
- Voice messages and audio files play inline with a seek bar.

**Sending messages**
- Drag files onto the message box to upload them.
- Paste images from the clipboard with Ctrl+V, including ones copied from a browser.
- A preview bar shows what you're replying to.
- Built-in GIF picker (Tenor), sticker picker and server emoji picker with fuzzy search.
- Files too big for Discord's free upload limit go to [0x0.st](https://0x0.st) instead, and the link is dropped into your message. The size limit is adjustable, because Discord keeps changing theirs.

**Finding your way around**
- Drag the divider to resize the sidebar. Below a certain width it collapses to avatars only.
- Coloured status dots on avatars: online, idle, do not disturb, offline.
- The nicknames you've given friends on Discord are used everywhere.
- A collapsible **More** list under your DMs shows friends you don't have a conversation open with. Click to start one.
- Right-click a channel to mark it read or copy its ID.
- **Ctrl+F** searches messages, **Ctrl+K** jumps to any channel, **Ctrl+/** shows all the shortcuts.

**Desktop integration**
- Minimise to the system tray on close.
- Notification controls: notify for everything, or respect your Discord mute settings.
- Load your own styling from `~/.config/lildisc/custom.css`.
- Keep rendering and video decoding on the integrated GPU on laptops with a discrete card. See [GPU selection](#gpu-selection).
- Old cached images are cleaned up automatically, and embeds load only when scrolled into view.

## Troubleshooting

### My uploads keep getting treated as spam

Discord scores how much a client looks like software rather than a person, and
it scores hardest on attachments, because uploads are how malware and
unsolicited imagery get spread. Two things used to push LilDisc over that line,
and both are fixed:

- **How the client introduced itself.** Every request said it was LilDisc, and
  the gateway reported the device as a Go library. The real app sends a browser
  user agent and an `X-Super-Properties` header describing the build. LilDisc
  now sends both, on every request, including the raw ones that skip the
  library.
- **What uploaded files were called.** With **Randomize Upload Filenames** on,
  every image was renamed to sixteen random characters, which is exactly what
  bulk spam looks like. It now produces names a real device would write, like
  `IMG_4821.jpg` or `Screenshot_20260920_143211.png`. Pasted images are called
  `image.png`, which is what Discord's own clients call them.

The version numbers LilDisc reports go stale as Discord ships updates, and a
build number far behind the current release is itself conspicuous. Refresh them
in `~/.config/lildisc/env` without rebuilding:

```sh
LILDISC_CLIENT_VERSION=0.0.140
LILDISC_CLIENT_BUILD_NUMBER=421337
LILDISC_NATIVE_BUILD_NUMBER=70201
LILDISC_CHROME_VERSION=152.0.7014.35
LILDISC_ELECTRON_VERSION=39.1.2
```

To read the current values, open the official client or the web app with
developer tools, pick any request to `discord.com/api`, and copy its user agent
and the `X-Super-Properties` value. Setting `LILDISC_CLIENT_IDENTITY=off`
returns to identifying honestly as LilDisc, which is the flagged behaviour.

### The switches in Preferences are invisible (Manjaro Sway with a Matcha theme)

Manjaro's Sway edition copies the active GTK theme's `gtk-4.0/gtk.css` into
`~/.config/gtk-4.0/`, and GTK applies that file on top of libadwaita. Matcha
draws switches entirely from PNGs in an `assets` directory that the theme
ships as a relative symlink, which dangles once copied out of
`/usr/share/themes`. The switches then have no background, so you cannot tell
whether a setting is on or off. Point the missing directory at the theme:

```bash
ln -sfn /usr/share/themes/Matcha-dark-aliz/gtk-3.0/assets ~/.config/gtk-3.0/assets
```

Substitute your Matcha variant. This makes the copied `../gtk-3.0/assets` link
resolve, so it survives the theme script re-running at login.

### GPU selection

On a hybrid or eGPU machine, LilDisc may wake a discrete GPU that isn't the
one driving your display. Two separate things cause it:

- **GTK's renderer.** GTK 4.14+ defaults to Vulkan, and its device selection
  prefers a discrete GPU when it finds one, even an external card that isn't
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

For anything the toggle doesn't cover, such as pinning Vulkan to a specific
device, `~/.config/lildisc/env` is applied at startup too and wins over the
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

## For developers

Every optional feature is one file in `internal/mods/`, with its own
preference switch. Each hooks into the host app through small integration
points marked with `// mod: feature-name` comments, so a feature can be read,
changed or deleted on its own.

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

LilDisc talks to Discord's gateway through
[arikawa](https://github.com/diamondburned/arikawa) and renders with
[gotk4](https://github.com/diamondburned/gotk4) and libadwaita. The app icon
source is `internal/icons/io.github.vomitselfie.lildisc.Source.svg`; after
changing it, run `go generate ./internal/icons/` to rebuild the icon bundle.

## Credits

LilDisc began as a fork of [Dissent](https://github.com/diamondburned/dissent)
by diamondburned, and still shares its rendering core.

- [Dissent](https://github.com/diamondburned/dissent) — the original native GTK4 Discord client this project descends from
- [Vesktop](https://github.com/Vencord/Vesktop) — design inspiration for the modular feature system
- [arikawa](https://github.com/diamondburned/arikawa), [gotk4](https://github.com/diamondburned/gotk4), [gotkit](https://github.com/diamondburned/gotkit), [chatkit](https://github.com/diamondburned/chatkit), [ningen](https://github.com/diamondburned/ningen) — core libraries by diamondburned

## License

GPL-3.0.
