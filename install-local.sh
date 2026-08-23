#!/bin/sh
# Installs LilDisc into the current user's XDG directories so it shows up in
# desktop app launchers (wofi/fuzzel/rofi drun, GNOME, ...).
#
# The binary is symlinked rather than copied, so `go build -o lildisc .` in
# this directory immediately updates what the launcher runs.
#
# Uninstall: run this script with `--uninstall`.
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
appid=io.github.dijama.lildisc

bin=${XDG_BIN_HOME:-$HOME/.local/bin}/lildisc
desktop=${XDG_DATA_HOME:-$HOME/.local/share}/applications/$appid.desktop
service=${XDG_DATA_HOME:-$HOME/.local/share}/dbus-1/services/$appid.service
icon=${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/scalable/apps/$appid.svg

if [ "${1:-}" = "--uninstall" ]; then
	rm -fv "$bin" "$desktop" "$service" "$icon"
	update-desktop-database "$(dirname "$desktop")" 2>/dev/null || true
	exit 0
fi

if [ ! -x "$repo/lildisc" ]; then
	echo "no binary at $repo/lildisc — run: go build -o lildisc ." >&2
	exit 1
fi

for d in "$bin" "$desktop" "$service" "$icon"; do
	mkdir -p "$(dirname "$d")"
done

ln -sfn "$repo/lildisc" "$bin"

# Exec must be absolute: launchers don't necessarily inherit a PATH
# containing ~/.local/bin.
sed "s|^Exec=lildisc$|Exec=$bin|" "$repo/nix/$appid.desktop" >"$desktop"
sed "s|^Exec=.*|Exec=$bin --gapplication-service|" "$repo/nix/$appid.service" >"$service"
cp "$repo/internal/icons/hicolor/scalable/apps/$appid.svg" "$icon"

update-desktop-database "$(dirname "$desktop")" 2>/dev/null || true
gtk-update-icon-cache -qtf "${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor" 2>/dev/null || true

echo "installed:"
echo "  $bin -> $repo/lildisc"
echo "  $desktop"
echo "  $service"
echo "  $icon"
