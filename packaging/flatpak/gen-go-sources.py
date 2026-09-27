#!/usr/bin/env python3
"""Generate go-sources.json, the Flatpak sources for LilDisc's Go modules.

Flatpak builds run without network, so every module the build needs is
listed as a pinned download from proxy.golang.org. The build points GOPROXY
at the directory they are placed in, which is laid out like a module proxy.

Hashes come from the local module cache, so run `go mod download` first.
Rerun this whenever go.mod or go.sum changes:

    go mod download && packaging/flatpak/gen-go-sources.py
"""

import hashlib
import json
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(ROOT, "packaging", "flatpak", "go-sources.json")
PROXY = "https://proxy.golang.org"
DEST = "gomod"  # GOPROXY=file://$PWD/gomod inside the build


def escape(path):
    # Module proxy escaping: uppercase letters become "!" + lowercase.
    return re.sub(r"[A-Z]", lambda m: "!" + m.group(0).lower(), path)


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def main():
    cache = subprocess.run(
        ["go", "env", "GOMODCACHE"], capture_output=True, text=True, check=True, cwd=ROOT
    ).stdout.strip()
    download = os.path.join(cache, "cache", "download")

    # go.sum lists "<module> <version>/go.mod" for every module in the graph
    # and "<module> <version>" for those whose code is needed.
    needs = {}
    with open(os.path.join(ROOT, "go.sum")) as f:
        for line in f:
            parts = line.split()
            if len(parts) != 3:
                continue
            mod, ver = parts[0], parts[1]
            gomod_only = ver.endswith("/go.mod")
            ver = ver.removesuffix("/go.mod")
            key = (mod, ver)
            needs[key] = needs.get(key, False) or not gomod_only

    sources, missing = [], []
    for (mod, ver), want_zip in sorted(needs.items()):
        rel = f"{escape(mod)}/@v"
        exts = ["mod"] + (["zip"] if want_zip else [])
        for ext in exts:
            local = os.path.join(download, rel, f"{escape(ver)}.{ext}")
            if not os.path.exists(local):
                missing.append(local)
                continue
            sources.append({
                "type": "file",
                "url": f"{PROXY}/{rel}/{escape(ver)}.{ext}",
                "sha256": sha256(local),
                "dest": f"{DEST}/{rel}",
                "dest-filename": f"{escape(ver)}.{ext}",
            })
        sources.append({
            "type": "inline",
            "contents": json.dumps({"Version": ver}),
            "dest": f"{DEST}/{rel}",
            "dest-filename": f"{escape(ver)}.info",
        })

    if missing:
        print("missing from the module cache; run `go mod download`:", file=sys.stderr)
        for m in missing:
            print("  " + m, file=sys.stderr)
        # go.sum keeps /go.mod hashes for modules that are never fetched;
        # a missing .mod for one of those is harmless, a missing zip is not.
        if any(m.endswith(".zip") for m in missing):
            sys.exit(1)

    with open(OUT, "w") as f:
        json.dump(sources, f, indent=2)
        f.write("\n")
    print(f"wrote {len(sources)} sources to {os.path.relpath(OUT, ROOT)}")


if __name__ == "__main__":
    main()
