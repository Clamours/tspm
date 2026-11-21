#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DEST="${1:-$HOME/.local/bin}"
SOURCE="$SCRIPT_DIR/tspm"
if [ ! -f "$SOURCE" ]; then
  case "$(uname -m)" in
    x86_64|amd64) CANDIDATE="$SCRIPT_DIR/dist/tspm-linux-amd64" ;;
    aarch64|arm64) CANDIDATE="$SCRIPT_DIR/dist/tspm-linux-arm64" ;;
    *) CANDIDATE="" ;;
  esac
  if [ -n "$CANDIDATE" ] && [ -f "$CANDIDATE" ]; then SOURCE="$CANDIDATE"; fi
fi

if [ ! -f "$SOURCE" ]; then
  echo "install.sh: no binary found. Use the Linux platform archive or build from source." >&2
  exit 1
fi

mkdir -p "$DEST"
install -m 755 "$SOURCE" "$DEST/tspm"
printf 'Installed tspm to %s/tspm\n' "$DEST"
case ":$PATH:" in
  *":$DEST:"*) ;;
  *) echo "Add this to your shell profile: export PATH=\"$DEST:\$PATH\"" ;;
esac
