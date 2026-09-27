# Packaging

Release binaries for Linux (x86_64, aarch64), Windows and macOS are built by
`.github/workflows/publish.yml` and attached to a GitHub release on every push
to `master`. This directory holds the rest.

## AUR

- `aur/lildisc` builds a tagged release. After tagging, set `pkgver` to the tag
  without its `v`, then run `updpkgsums` and `makepkg --printsrcinfo > .SRCINFO`.
- `aur/lildisc-git` builds the latest `master`; its version comes from
  `git describe`. Regenerate `.SRCINFO` only when dependencies change.

Both install the binary, desktop entry, D-Bus service, AppStream metainfo and
icons. `gst-libav` is an optional dependency, but without it most videos and
voice messages will not play.

## Flatpak

`flatpak/io.github.vomitselfie.lildisc.json` builds against the GNOME 49
runtime, adding libspelling, which the runtime lacks.

Flatpak builds have no network, so every Go module is listed in
`flatpak/go-sources.json` as a pinned download from proxy.golang.org, and the
build points `GOPROXY` at them. CI regenerates the file whenever `go.mod`
changes; to do it by hand:

    go mod download && packaging/flatpak/gen-go-sources.py

To build and install locally:

    flatpak-builder --user --install --force-clean build-dir \
        packaging/flatpak/io.github.vomitselfie.lildisc.json

The manifest builds from the checkout (`"type": "dir"`). A Flathub submission
needs that replaced with a `git` source pinned to a tag and commit.

Inside the sandbox, mpv and yt-dlp are not available, so streaming-site videos
open in LilDisc's own player or the browser instead.

## Nix

`nix/gomod2nix.toml` pins the Go modules for the Nix build and is regenerated
by CI alongside `go.sum`.
