#!/usr/bin/env bash
# Regenerate docs/images from the current gohard binary.
# Prefer `nix run .#demo-screenshots`, which sets PATH and GOHARD_BIN.
# A local run needs nvim, vhs, ttyd and ffmpeg on PATH, and either
# GOHARD_BIN or a `gohard` binary on PATH.
set -eu

here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
root=$(CDPATH= cd -- "$here/../.." && pwd)
cd "$root"

if [ -z "${GOHARD_BIN:-}" ]; then
  GOHARD_BIN=$(command -v gohard || true)
fi
if [ -z "$GOHARD_BIN" ] || [ ! -x "$GOHARD_BIN" ]; then
  echo "demo: set GOHARD_BIN to the gohard binary to capture" >&2
  exit 1
fi
export GOHARD_BIN
export TERM="${TERM:-xterm-256color}"
export COLORTERM="${COLORTERM:-truecolor}"

need() {
  if ! command -v "$1" >/dev/null; then
    echo "demo: '$1' is not on PATH" >&2
    exit 1
  fi
}
need nvim
need vhs
need ttyd
need ffmpeg

echo "demo: $($GOHARD_BIN version | tr '\n' ' ')"
mkdir -p docs/images docs/demo/.cache

echo "demo: probing the language server"
"$here/scene.sh" probe

echo "demo: recording"
vhs "$here/demo.tape"

python3 - "$root" <<'PY'
import struct, sys
from pathlib import Path
root = Path(sys.argv[1])
names = [
    "hover.png",
    "completion.png",
    "definition.png",
    "references.png",
    "signature.png",
    "diagnostics.png",
]
bad = False
for name in names:
    path = root / "docs" / "images" / name
    data = path.read_bytes()
    if data[1:4] != b"PNG":
        print(f"demo: {path} is not a PNG", file=sys.stderr)
        bad = True
        continue
    w, h = struct.unpack(">II", data[16:24])
    print(f"demo: {name} {w}x{h} {len(data)} bytes")
    if w < 400 or h < 200 or len(data) < 8000:
        print(f"demo: {path} looks empty", file=sys.stderr)
        bad = True
if bad:
    sys.exit(1)
PY
