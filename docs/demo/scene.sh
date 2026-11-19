#!/usr/bin/env bash
# Open one demo scene in Neovim and leave it on screen.
#   docs/demo/scene.sh hover
# Scene "probe" is headless: it checks the language server and exits.
set -eu

scene=${1:?usage: docs/demo/scene.sh SCENE}

here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
root=$(CDPATH= cd -- "$here/../.." && pwd)

case $scene in
hover | completion | definition | references | signature | symbols | probe)
  file=$here/src/alu.vhd
  ;;
diagnostics)
  file=$here/src/lint_me.vhd
  ;;
*)
  echo "demo: unknown scene: $scene" >&2
  exit 2
  ;;
esac

export GOHARD_SCENE=$scene
cd "$root"

args=(-n --noplugin -i NONE -u "$here/init.lua" "$file")
if [ "$scene" = probe ]; then
  args=(--headless "${args[@]}")
fi
exec nvim "${args[@]}"
